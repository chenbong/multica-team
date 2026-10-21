package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Runtime-local agent memory (multica-team overlay).
//
// "Import memory from the runtime machine" is a two-step, heartbeat-carried
// exchange modeled on runtime local skills:
//
//  1. kind=list — the daemon enumerates well-known memory/instruction files
//     on its machine (~/.claude/CLAUDE.md, ~/.claude/projects/*/memory/*.md,
//     ~/.codex/AGENTS.md, ~/.gemini/GEMINI.md, …) and reports summaries.
//  2. kind=read — the browser asks for the contents of selected discovery
//     keys. The daemon re-runs discovery and only reads keys it found itself,
//     so a request can never name an arbitrary path on the machine.
//
// The contents go back to the browser, which adds them to the agent's memory
// draft; nothing is persisted until the user saves the Memory tab. Both steps
// are runtime-owner only because they expose files from the owner's machine.

const (
	RuntimeLocalMemoryKindList = "list"
	RuntimeLocalMemoryKindRead = "read"

	// A memory-aware daemon claims the request on the heartbeat triggered by
	// the pending-work hint, so a long pending phase almost always means the
	// daemon predates this feature. Fail fast with an upgrade hint.
	runtimeLocalMemoryPendingTimeout = 45 * time.Second
	runtimeLocalMemoryRunningTimeout = 60 * time.Second
	runtimeLocalMemoryStoreRetention = 5 * time.Minute

	runtimeLocalMemoryMaxReadKeys   = 64
	runtimeLocalMemoryMaxKeyLength  = 1024
	runtimeLocalMemoryMaxEntries    = 500
	runtimeLocalMemoryMaxReadBytes  = 4 << 20
	runtimeLocalMemoryMaxReportBody = 12 << 20
	maxLocalMemoryBatch             = 4
)

type RuntimeLocalMemorySummary struct {
	Key           string `json:"key"`
	Tool          string `json:"tool"`
	Scope         string `json:"scope"`
	Label         string `json:"label,omitempty"`
	SourcePath    string `json:"source_path"`
	SuggestedPath string `json:"suggested_path"`
	SizeBytes     int64  `json:"size_bytes"`
	ModifiedAt    string `json:"modified_at,omitempty"`
	TooLarge      bool   `json:"too_large,omitempty"`
}

type RuntimeLocalMemoryFile struct {
	Key           string `json:"key"`
	SourcePath    string `json:"source_path"`
	SuggestedPath string `json:"suggested_path"`
	Content       string `json:"content"`
	SizeBytes     int    `json:"size_bytes"`
}

type RuntimeLocalMemoryReadError struct {
	Key   string `json:"key"`
	Error string `json:"error"`
}

type RuntimeLocalMemoryRequest struct {
	ID        string                         `json:"id"`
	RuntimeID string                         `json:"runtime_id"`
	Provider  string                         `json:"provider,omitempty"`
	Kind      string                         `json:"kind"`
	Keys      []string                       `json:"keys,omitempty"`
	Status    RuntimeLocalSkillRequestStatus `json:"status"`
	Entries   []RuntimeLocalMemorySummary    `json:"entries,omitempty"`
	Files     []RuntimeLocalMemoryFile       `json:"files,omitempty"`
	Errors    []RuntimeLocalMemoryReadError  `json:"errors,omitempty"`
	Error     string                         `json:"error,omitempty"`
	CreatedAt time.Time                      `json:"created_at"`
	UpdatedAt time.Time                      `json:"updated_at"`
	// RunStartedAt is internal bookkeeping; the Redis store persists it
	// through runtimeLocalMemoryEnvelope.
	RunStartedAt *time.Time `json:"-"`
}

type RuntimeLocalMemoryResult struct {
	Entries []RuntimeLocalMemorySummary
	Files   []RuntimeLocalMemoryFile
	Errors  []RuntimeLocalMemoryReadError
}

// LocalMemoryStore tracks runtime-local memory requests. Same multi-node
// contract as LocalSkillListStore: production with Redis uses
// RedisLocalMemoryStore so POST, heartbeat and poll may land on any node.
type LocalMemoryStore interface {
	Create(ctx context.Context, runtimeID, provider, kind string, keys []string) (*RuntimeLocalMemoryRequest, error)
	Get(ctx context.Context, id string) (*RuntimeLocalMemoryRequest, error)
	HasPending(ctx context.Context, runtimeID string) (bool, error)
	PopPendingBatch(ctx context.Context, runtimeID string, limit int) ([]*RuntimeLocalMemoryRequest, error)
	Complete(ctx context.Context, id string, result RuntimeLocalMemoryResult) error
	Fail(ctx context.Context, id string, errMsg string) error
}

func newRuntimeLocalMemoryRequest(runtimeID, provider, kind string, keys []string) *RuntimeLocalMemoryRequest {
	now := time.Now()
	return &RuntimeLocalMemoryRequest{
		ID:        randomID(),
		RuntimeID: runtimeID,
		Provider:  provider,
		Kind:      kind,
		Keys:      keys,
		Status:    RuntimeLocalSkillPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func applyLocalMemoryTimeout(req *RuntimeLocalMemoryRequest, now time.Time) bool {
	switch req.Status {
	case RuntimeLocalSkillPending:
		if now.Sub(req.CreatedAt) > runtimeLocalMemoryPendingTimeout {
			req.Status = RuntimeLocalSkillTimeout
			req.Error = "the daemon did not pick up the request; make sure it is online and runs a multica CLI with agent memory support"
			req.UpdatedAt = now
			return true
		}
	case RuntimeLocalSkillRunning:
		if req.RunStartedAt != nil && now.Sub(*req.RunStartedAt) > runtimeLocalMemoryRunningTimeout {
			req.Status = RuntimeLocalSkillTimeout
			req.Error = "the daemon did not finish within 60 seconds"
			req.UpdatedAt = now
			return true
		}
	}
	return false
}

func applyLocalMemoryResult(req *RuntimeLocalMemoryRequest, result RuntimeLocalMemoryResult) {
	req.Status = RuntimeLocalSkillCompleted
	req.Entries = result.Entries
	req.Files = result.Files
	req.Errors = result.Errors
	req.UpdatedAt = time.Now()
}

func claimLocalMemoryRequest(req *RuntimeLocalMemoryRequest, now time.Time) {
	req.Status = RuntimeLocalSkillRunning
	startedAt := now
	req.RunStartedAt = &startedAt
	req.UpdatedAt = now
}

// InMemoryLocalMemoryStore is the single-node implementation.
type InMemoryLocalMemoryStore struct {
	mu       sync.Mutex
	requests map[string]*RuntimeLocalMemoryRequest
}

func NewInMemoryLocalMemoryStore() *InMemoryLocalMemoryStore {
	return &InMemoryLocalMemoryStore{requests: make(map[string]*RuntimeLocalMemoryRequest)}
}

func (s *InMemoryLocalMemoryStore) Create(_ context.Context, runtimeID, provider, kind string, keys []string) (*RuntimeLocalMemoryRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, req := range s.requests {
		if time.Since(req.CreatedAt) > runtimeLocalMemoryStoreRetention {
			delete(s.requests, id)
		}
	}
	req := newRuntimeLocalMemoryRequest(runtimeID, provider, kind, keys)
	s.requests[req.ID] = req
	copied := *req
	return &copied, nil
}

func (s *InMemoryLocalMemoryStore) Get(_ context.Context, id string) (*RuntimeLocalMemoryRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	req, ok := s.requests[id]
	if !ok {
		return nil, nil
	}
	applyLocalMemoryTimeout(req, time.Now())
	copied := *req
	return &copied, nil
}

func (s *InMemoryLocalMemoryStore) HasPending(_ context.Context, runtimeID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for _, req := range s.requests {
		applyLocalMemoryTimeout(req, now)
		if req.RuntimeID == runtimeID && req.Status == RuntimeLocalSkillPending {
			return true, nil
		}
	}
	return false, nil
}

func (s *InMemoryLocalMemoryStore) PopPendingBatch(_ context.Context, runtimeID string, limit int) ([]*RuntimeLocalMemoryRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	var pending []*RuntimeLocalMemoryRequest
	for _, req := range s.requests {
		applyLocalMemoryTimeout(req, now)
		if req.RuntimeID == runtimeID && req.Status == RuntimeLocalSkillPending {
			pending = append(pending, req)
		}
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].CreatedAt.Before(pending[j].CreatedAt) })
	if limit > len(pending) {
		limit = len(pending)
	}
	out := make([]*RuntimeLocalMemoryRequest, 0, limit)
	for _, req := range pending[:limit] {
		claimLocalMemoryRequest(req, now)
		copied := *req
		out = append(out, &copied)
	}
	return out, nil
}

func (s *InMemoryLocalMemoryStore) Complete(_ context.Context, id string, result RuntimeLocalMemoryResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if req, ok := s.requests[id]; ok {
		applyLocalMemoryResult(req, result)
	}
	return nil
}

func (s *InMemoryLocalMemoryStore) Fail(_ context.Context, id string, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if req, ok := s.requests[id]; ok {
		req.Status = RuntimeLocalSkillFailed
		req.Error = errMsg
		req.UpdatedAt = time.Now()
	}
	return nil
}

type CreateRuntimeLocalMemoryRequest struct {
	Kind string   `json:"kind"`
	Keys []string `json:"keys,omitempty"`
}

// InitiateLocalMemoryRequest enqueues a list or read request for the daemon.
func (h *Handler) InitiateLocalMemoryRequest(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	rt, ok := h.requireRuntimeLocalSkillAccess(w, r, obsmetrics.RuntimeLookupSourceRuntimeAPI, runtimeID)
	if !ok {
		return
	}
	if rt.status != "online" {
		writeError(w, http.StatusServiceUnavailable, "runtime is offline")
		return
	}
	var body CreateRuntimeLocalMemoryRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	kind := strings.TrimSpace(body.Kind)
	if kind == "" {
		kind = RuntimeLocalMemoryKindList
	}
	var keys []string
	switch kind {
	case RuntimeLocalMemoryKindList:
	case RuntimeLocalMemoryKindRead:
		seen := map[string]bool{}
		for _, k := range body.Keys {
			k = strings.TrimSpace(k)
			if k == "" || seen[k] {
				continue
			}
			if len(k) > runtimeLocalMemoryMaxKeyLength || !utf8.ValidString(k) {
				writeError(w, http.StatusBadRequest, "invalid memory key")
				return
			}
			seen[k] = true
			keys = append(keys, k)
		}
		if len(keys) == 0 {
			writeError(w, http.StatusBadRequest, "keys is required for kind=read")
			return
		}
		if len(keys) > runtimeLocalMemoryMaxReadKeys {
			writeError(w, http.StatusBadRequest, "too many memory files requested at once")
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "kind must be list or read")
		return
	}

	req, err := h.LocalMemoryStore.Create(r.Context(), rt.runtimeID, rt.provider, kind, keys)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to enqueue local memory request: "+err.Error())
		return
	}
	h.requestDaemonPendingWork(rt.runtimeID, protocol.PendingWorkKindLocalMemory)
	writeJSON(w, http.StatusOK, req)
}

func (h *Handler) GetLocalMemoryRequest(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	rt, ok := h.requireRuntimeLocalSkillAccess(w, r, obsmetrics.RuntimeLookupSourceRuntimeLocalSkillPoll, runtimeID)
	if !ok {
		return
	}
	req, err := h.LocalMemoryStore.Get(r.Context(), chi.URLParam(r, "requestId"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load request: "+err.Error())
		return
	}
	if req == nil || req.RuntimeID != rt.runtimeID {
		writeError(w, http.StatusNotFound, "request not found")
		return
	}
	writeJSON(w, http.StatusOK, req)
}

// ReportLocalMemoryResult receives the daemon's list/read result.
func (h *Handler) ReportLocalMemoryResult(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	if _, ok := h.requireDaemonRuntimeAccess(w, r, runtimeID); !ok {
		return
	}
	requestID := chi.URLParam(r, "requestId")
	req, err := h.LocalMemoryStore.Get(r.Context(), requestID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load request: "+err.Error())
		return
	}
	if req == nil || req.RuntimeID != runtimeID {
		writeError(w, http.StatusNotFound, "request not found")
		return
	}
	if runtimeLocalSkillRequestTerminal(req.Status) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	var body struct {
		Status  string                        `json:"status"`
		Entries []RuntimeLocalMemorySummary   `json:"entries"`
		Files   []RuntimeLocalMemoryFile      `json:"files"`
		Errors  []RuntimeLocalMemoryReadError `json:"errors"`
		Error   string                        `json:"error"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, runtimeLocalMemoryMaxReportBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if body.Status != "completed" {
		msg := strings.TrimSpace(body.Error)
		if msg == "" {
			msg = "the daemon could not read memory files"
		}
		if err := h.LocalMemoryStore.Fail(r.Context(), requestID, truncateRunes(msg, 1000)); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to persist failure")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	result := RuntimeLocalMemoryResult{}
	switch req.Kind {
	case RuntimeLocalMemoryKindList:
		entries := body.Entries
		if len(entries) > runtimeLocalMemoryMaxEntries {
			entries = entries[:runtimeLocalMemoryMaxEntries]
		}
		result.Entries = make([]RuntimeLocalMemorySummary, 0, len(entries))
		for _, e := range entries {
			if strings.TrimSpace(e.Key) == "" {
				continue
			}
			result.Entries = append(result.Entries, e)
		}
	case RuntimeLocalMemoryKindRead:
		// Accept only the keys that were requested, and only text the memory
		// tab can save; anything else becomes a per-key error.
		requested := make(map[string]bool, len(req.Keys))
		for _, k := range req.Keys {
			requested[k] = true
		}
		total := 0
		for _, f := range body.Files {
			if !requested[f.Key] {
				continue
			}
			switch {
			case !utf8.ValidString(f.Content) || strings.ContainsRune(f.Content, 0):
				result.Errors = append(result.Errors, RuntimeLocalMemoryReadError{Key: f.Key, Error: "file is not UTF-8 text"})
				continue
			case len(f.Content) > agentMemoryMaxFileBytes:
				result.Errors = append(result.Errors, RuntimeLocalMemoryReadError{Key: f.Key, Error: "file is larger than the memory file limit"})
				continue
			case total+len(f.Content) > runtimeLocalMemoryMaxReadBytes:
				result.Errors = append(result.Errors, RuntimeLocalMemoryReadError{Key: f.Key, Error: "selection is too large to import at once"})
				continue
			}
			total += len(f.Content)
			f.SizeBytes = len(f.Content)
			result.Files = append(result.Files, f)
			delete(requested, f.Key)
		}
		for _, e := range body.Errors {
			if requested[e.Key] {
				result.Errors = append(result.Errors, RuntimeLocalMemoryReadError{Key: e.Key, Error: truncateRunes(e.Error, 500)})
				delete(requested, e.Key)
			}
		}
		for k := range requested {
			result.Errors = append(result.Errors, RuntimeLocalMemoryReadError{Key: k, Error: "file was not found on the runtime machine"})
		}
		sort.Slice(result.Errors, func(i, j int) bool { return result.Errors[i].Key < result.Errors[j].Key })
	}

	if err := h.LocalMemoryStore.Complete(r.Context(), requestID, result); err != nil {
		slog.Error("local memory Complete failed", "error", err, "request_id", requestID)
		writeError(w, http.StatusInternalServerError, "failed to persist completion")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// popPendingLocalMemory is the heartbeat hook: a bounded probe, then an
// unbounded claim (same reasoning as the local-skill queues).
func (h *Handler) popPendingLocalMemory(ctx context.Context, runtimeID string) []protocol.DaemonHeartbeatPendingLocalMemory {
	if h.LocalMemoryStore == nil {
		return nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, heartbeatHasPendingTimeout)
	has, err := h.LocalMemoryStore.HasPending(probeCtx, runtimeID)
	cancel()
	if err != nil {
		if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			slog.Warn("local memory HasPending failed", "error", err, "runtime_id", runtimeID)
		}
		return nil
	}
	if !has {
		return nil
	}
	claimed, err := h.LocalMemoryStore.PopPendingBatch(ctx, runtimeID, maxLocalMemoryBatch)
	if err != nil {
		slog.Warn("local memory PopPendingBatch failed", "error", err, "runtime_id", runtimeID, "claimed", len(claimed))
	}
	out := make([]protocol.DaemonHeartbeatPendingLocalMemory, 0, len(claimed))
	for _, req := range claimed {
		out = append(out, protocol.DaemonHeartbeatPendingLocalMemory{ID: req.ID, Kind: req.Kind, Keys: req.Keys})
	}
	return out
}
