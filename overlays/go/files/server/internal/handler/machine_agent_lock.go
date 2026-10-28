package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
)

var machineAgentLocks = struct {
	sync.Mutex
	counts map[*pgxpool.Pool]int
}{counts: make(map[*pgxpool.Pool]int)}

// Keep long-lived locks to a small fraction of the pool: the CLI still needs
// other connections for its normal agent and runtime requests.
func reserveMachineAgentLock(h *Handler) (func(), bool) {
	pool, _ := h.TxStarter.(*pgxpool.Pool)
	limit := 2
	if pool != nil {
		maxConns := int(pool.Stat().MaxConns())
		if maxConns < 3 {
			return nil, false
		}
		limit = maxConns / 3
		if limit < 1 {
			limit = 1
		}
		if limit > 4 {
			limit = 4
		}
	}
	machineAgentLocks.Lock()
	if machineAgentLocks.counts[pool] >= limit {
		machineAgentLocks.Unlock()
		return nil, false
	}
	machineAgentLocks.counts[pool]++
	machineAgentLocks.Unlock()
	return func() {
		machineAgentLocks.Lock()
		defer machineAgentLocks.Unlock()
		machineAgentLocks.counts[pool]--
		if machineAgentLocks.counts[pool] == 0 {
			delete(machineAgentLocks.counts, pool)
		}
	}, true
}

// Hold a transaction-scoped database lock for the lifetime of an authenticated
// CLI operation. The stream is kept open until the client closes it; a crash or
// disconnect releases the lock without leaving a stale lease behind.
func (h *Handler) MachineAgentLock(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var input struct {
		WorkspaceID string `json:"workspace_id"`
		AgentName   string `json:"agent_name"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid agent lock request")
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid agent lock request")
		return
	}
	input.AgentName = strings.TrimSpace(input.AgentName)
	if input.AgentName == "" || !utf8.ValidString(input.AgentName) || utf8.RuneCountInString(input.AgentName) > 128 || strings.IndexFunc(input.AgentName, unicode.IsControl) >= 0 {
		writeError(w, http.StatusBadRequest, "invalid agent name")
		return
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, input.WorkspaceID, "workspace_id")
	if !ok {
		return
	}
	actorID, ok := parseUUIDOrBadRequest(w, userID, "user_id")
	if !ok {
		return
	}
	if _, err := h.getWorkspaceMember(r.Context(), userID, input.WorkspaceID); err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "agent lock streaming unavailable")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "agent lock unavailable")
		return
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(ctx)
	}()
	if _, err := tx.Exec(r.Context(), `SET LOCAL idle_in_transaction_session_timeout = 0`); err != nil {
		writeError(w, http.StatusServiceUnavailable, "agent lock unavailable")
		return
	}
	var acquired bool
	key := "tmesh-agent:" + input.WorkspaceID + ":" + input.AgentName
	if err := tx.QueryRow(r.Context(), `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, key).Scan(&acquired); err != nil {
		writeError(w, http.StatusServiceUnavailable, "agent lock unavailable")
		return
	}
	if !acquired {
		writeError(w, http.StatusConflict, "another machine is preparing this agent; retry after it finishes")
		return
	}
	// The normal agent list hides other users' private agents. Check the name
	// server-side without revealing their identities or other configuration.
	var otherOwner bool
	if err := tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM agent WHERE workspace_id=$1 AND name=$2 AND owner_id IS DISTINCT FROM $3)`, workspaceID, input.AgentName, actorID).Scan(&otherOwner); err != nil {
		writeError(w, http.StatusServiceUnavailable, "agent name validation failed")
		return
	}
	if otherOwner {
		writeError(w, http.StatusConflict, "an agent with this name belongs to another owner")
		return
	}
	releaseSlot, ok := reserveMachineAgentLock(h)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "agent coordination capacity is exhausted")
		return
	}
	defer releaseSlot()
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	if _, err := fmt.Fprintln(w, `{"locked":true}`); err != nil {
		return
	}
	flusher.Flush()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	timer := time.NewTimer(2 * time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
			return
		case <-ticker.C:
			if _, err := tx.Exec(r.Context(), `SELECT 1`); err != nil {
				return
			}
			if _, err := fmt.Fprintln(w, `{"heartbeat":true}`); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
