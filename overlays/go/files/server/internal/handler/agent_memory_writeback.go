package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Agent memory write-back and source sync (multica-team overlay, team
// migration 468).
//
// Write-back: after a run, a memory-aware daemon reports how the agent changed
// its memory — edits to the .multica/memory/ copies and, for Claude-family
// runtimes, the runtime's own auto-memory for that task. Each change becomes
// a proposal. In "review" mode (the default) the owner accepts or rejects it
// on the Memory tab; in "auto" mode it is applied at once unless the file
// changed since the run started, in which case it waits for review. Issue
// content is untrusted, so nothing reaches every future run unless the
// owner reviews it or explicitly chose "auto".
//
// Source sync: a runtime-imported file with auto_sync on is re-read from its
// source file on that machine about every agentMemorySyncInterval, piggy-
// backing on the runtime's heartbeat and the existing local-memory read
// request. Only the user who turned sync on, while still owning the runtime,
// keeps it running.

const (
	AgentMemoryWritebackOff    = "off"
	AgentMemoryWritebackReview = "review"
	AgentMemoryWritebackAuto   = "auto"

	AgentMemoryOriginAgent   = "agent"
	AgentMemoryOriginRuntime = "runtime"

	agentMemorySyncInterval = 5 * time.Minute
)

type AgentMemorySettings struct {
	Writeback            string `json:"writeback"`
	CaptureRuntimeMemory bool   `json:"capture_runtime_memory"`
}

// AgentMemoryWritebackData is the claim-payload write-back policy.
type AgentMemoryWritebackData struct {
	Mode           string `json:"mode"`
	CaptureRuntime bool   `json:"capture_runtime"`
}

func defaultAgentMemorySettings() AgentMemorySettings {
	return AgentMemorySettings{Writeback: AgentMemoryWritebackReview, CaptureRuntimeMemory: true}
}

func isUndefinedTable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42P01"
}

// loadAgentMemorySettings returns the agent's write-back policy. Before
// migration 468 has run, write-back is reported as off.
func (h *Handler) loadAgentMemorySettings(ctx context.Context, agentID pgtype.UUID) (AgentMemorySettings, error) {
	row, err := h.Queries.GetAgentMemorySetting(ctx, agentID)
	switch {
	case err == nil:
		return AgentMemorySettings{Writeback: row.Writeback, CaptureRuntimeMemory: row.CaptureRuntimeMemory}, nil
	case errors.Is(err, pgx.ErrNoRows):
		return defaultAgentMemorySettings(), nil
	case isUndefinedTable(err):
		return AgentMemorySettings{Writeback: AgentMemoryWritebackOff}, nil
	default:
		return AgentMemorySettings{}, err
	}
}

type AgentMemoryProposalResponse struct {
	ID          string  `json:"id"`
	TaskID      *string `json:"task_id"`
	Path        string  `json:"path"`
	Action      string  `json:"action"`
	Content     string  `json:"content"`
	BaseContent *string `json:"base_content"`
	// CurrentContent is the stored file now (nil when absent), so the
	// reviewer sees exactly what accepting would replace.
	CurrentContent *string `json:"current_content"`
	Origin         string  `json:"origin"`
	Status         string  `json:"status"`
	Note           string  `json:"note"`
	CreatedAt      string  `json:"created_at"`
	ResolvedAt     *string `json:"resolved_at"`
	ResolvedBy     *string `json:"resolved_by"`
}

func agentMemoryProposalToResponse(p db.AgentMemoryProposal, current map[string]string) AgentMemoryProposalResponse {
	resp := AgentMemoryProposalResponse{
		ID:         uuidToString(p.ID),
		TaskID:     uuidToPtr(p.TaskID),
		Path:       p.Path,
		Action:     p.Action,
		Content:    p.Content,
		Origin:     p.Origin,
		Status:     p.Status,
		Note:       p.Note,
		CreatedAt:  timestampToString(p.CreatedAt),
		ResolvedAt: timestampToPtr(p.ResolvedAt),
		ResolvedBy: uuidToPtr(p.ResolvedBy),
	}
	if p.BaseContent.Valid {
		base := p.BaseContent.String
		resp.BaseContent = &base
	}
	if c, ok := current[p.Path]; ok {
		resp.CurrentContent = &c
	}
	return resp
}

// agentMemoryUserError is a user-facing validation failure.
type agentMemoryUserError struct{ msg string }

func (e *agentMemoryUserError) Error() string { return e.msg }

// resolveAgentMemorySyncOwners decides who owns each auto-synced file in a
// save. A file that already synced from the same source keeps its owner
// (nil here; the upsert coalesces). Turning sync on, or pointing it at a
// different source, requires the caller to own that runtime.
func (h *Handler) resolveAgentMemorySyncOwners(ctx context.Context, agentID pgtype.UUID, member db.Member, files []validatedAgentMemoryFile) (map[string]pgtype.UUID, error) {
	owners := map[string]pgtype.UUID{}
	var existing map[string]db.AgentMemoryFile
	for _, f := range files {
		if !f.autoSync {
			continue
		}
		if existing == nil {
			rows, err := h.Queries.ListAgentMemoryFiles(ctx, agentID)
			if err != nil {
				return nil, err
			}
			existing = make(map[string]db.AgentMemoryFile, len(rows))
			for _, row := range rows {
				existing[row.Path] = row
			}
		}
		if prev, ok := existing[f.path]; ok && prev.AutoSync && prev.SyncOwnerID.Valid &&
			prev.SourceRuntimeID == f.sourceRuntimeID && prev.SourcePath == f.sourcePath {
			continue
		}
		rt, err := h.Queries.GetAgentRuntime(ctx, f.sourceRuntimeID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, &agentMemoryUserError{fmt.Sprintf("memory file %q: its source runtime no longer exists, so it cannot sync", f.path)}
			}
			return nil, err
		}
		if !rt.OwnerID.Valid || rt.OwnerID != member.UserID {
			return nil, &agentMemoryUserError{fmt.Sprintf("memory file %q: only the owner of its source runtime can turn on sync", f.path)}
		}
		owners[f.path] = member.UserID
	}
	return owners, nil
}

var agentMemorySyncLast sync.Map // runtime id -> time.Time of the last sync enqueue

func resetAgentMemorySyncThrottle(runtimeID string) { agentMemorySyncLast.Delete(runtimeID) }

// applyAgentMemoryChange writes one accepted change into the stored set,
// re-validating the resulting set against the same limits as a save.
func applyAgentMemoryChange(ctx context.Context, qtx *db.Queries, agentID pgtype.UUID, path, action, content string, actor pgtype.UUID) error {
	rows, err := qtx.ListAgentMemoryFiles(ctx, agentID)
	if err != nil {
		return err
	}
	inputs := make([]AgentMemoryFileInput, 0, len(rows)+1)
	var target *db.AgentMemoryFile
	for i := range rows {
		row := rows[i]
		in := AgentMemoryFileInput{
			Path:       row.Path,
			Content:    row.Content,
			Source:     row.Source,
			SourcePath: row.SourcePath,
			Enabled:    &row.Enabled,
			AutoSync:   row.AutoSync,
		}
		if row.SourceRuntimeID.Valid {
			in.SourceRuntimeID = uuidToString(row.SourceRuntimeID)
		}
		if row.Path == path {
			target = &rows[i]
			if action == "delete" {
				continue
			}
			in.Content = content
			in.AutoSync = false
		}
		inputs = append(inputs, in)
	}
	if action == "upsert" && target == nil {
		inputs = append(inputs, AgentMemoryFileInput{Path: path, Content: content, Source: AgentMemorySourceAgent})
	}
	if _, err := validateAgentMemoryFiles(inputs); err != nil {
		return &agentMemoryUserError{err.Error()}
	}
	if action == "delete" {
		if target == nil {
			return nil
		}
		return qtx.DeleteAgentMemoryFile(ctx, db.DeleteAgentMemoryFileParams{AgentID: agentID, Path: path})
	}
	params := db.UpsertAgentMemoryFileParams{
		AgentID:   agentID,
		Path:      path,
		Content:   content,
		Source:    AgentMemorySourceAgent,
		Enabled:   true,
		UpdatedBy: actor,
	}
	if target != nil {
		// Keep where the file came from; an accepted edit replaces the
		// content, so a synced file stops syncing (the edit would otherwise
		// be overwritten by the next sync).
		params.Source = target.Source
		params.SourceRuntimeID = target.SourceRuntimeID
		params.SourcePath = target.SourcePath
		params.Enabled = target.Enabled
	}
	_, err = qtx.UpsertAgentMemoryFile(ctx, params)
	return err
}

type UpdateAgentMemorySettingsRequest struct {
	Writeback            string `json:"writeback"`
	CaptureRuntimeMemory *bool  `json:"capture_runtime_memory"`
}

// UpdateAgentMemorySettings sets the agent's write-back policy.
func (h *Handler) UpdateAgentMemorySettings(w http.ResponseWriter, r *http.Request) {
	agent, member, ok := h.authorizeAgentMemory(w, r)
	if !ok {
		return
	}
	var req UpdateAgentMemorySettingsRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	current, err := h.loadAgentMemorySettings(r.Context(), agent.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load memory settings")
		return
	}
	mode := strings.TrimSpace(req.Writeback)
	if mode == "" {
		mode = current.Writeback
	}
	switch mode {
	case AgentMemoryWritebackOff, AgentMemoryWritebackReview, AgentMemoryWritebackAuto:
	default:
		writeError(w, http.StatusBadRequest, "writeback must be off, review or auto")
		return
	}
	capture := current.CaptureRuntimeMemory
	if req.CaptureRuntimeMemory != nil {
		capture = *req.CaptureRuntimeMemory
	}
	if _, err := h.Queries.UpsertAgentMemorySetting(r.Context(), db.UpsertAgentMemorySettingParams{
		AgentID:              agent.ID,
		Writeback:            mode,
		CaptureRuntimeMemory: capture,
		UpdatedBy:            member.UserID,
	}); err != nil {
		slog.Error("update agent memory settings failed", "agent_id", uuidToString(agent.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save memory settings")
		return
	}
	resp, err := h.agentMemoryResponse(r.Context(), agent)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "memory settings saved but could not be reloaded")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ListAgentMemoryProposals lists recent proposals (?status=pending|accepted|
// rejected|superseded; empty for all).
func (h *Handler) ListAgentMemoryProposals(w http.ResponseWriter, r *http.Request) {
	agent, _, ok := h.authorizeAgentMemory(w, r)
	if !ok {
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	switch status {
	case "", "pending", "accepted", "rejected", "superseded":
	default:
		writeError(w, http.StatusBadRequest, "invalid status filter")
		return
	}
	rows, err := h.Queries.ListAgentMemoryProposals(r.Context(), db.ListAgentMemoryProposalsParams{AgentID: agent.ID, Status: status})
	if err != nil {
		if isUndefinedTable(err) {
			writeJSON(w, http.StatusOK, map[string]any{"proposals": []AgentMemoryProposalResponse{}})
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load memory proposals")
		return
	}
	files, err := h.Queries.ListAgentMemoryFiles(r.Context(), agent.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agent memory")
		return
	}
	current := make(map[string]string, len(files))
	for _, f := range files {
		current[f.Path] = f.Content
	}
	out := make([]AgentMemoryProposalResponse, 0, len(rows))
	for _, p := range rows {
		out = append(out, agentMemoryProposalToResponse(p, current))
	}
	writeJSON(w, http.StatusOK, map[string]any{"proposals": out})
}

func (h *Handler) AcceptAgentMemoryProposal(w http.ResponseWriter, r *http.Request) {
	h.resolveAgentMemoryProposal(w, r, "accepted")
}

func (h *Handler) RejectAgentMemoryProposal(w http.ResponseWriter, r *http.Request) {
	h.resolveAgentMemoryProposal(w, r, "rejected")
}

func (h *Handler) resolveAgentMemoryProposal(w http.ResponseWriter, r *http.Request, status string) {
	agent, member, ok := h.authorizeAgentMemory(w, r)
	if !ok {
		return
	}
	var proposalID pgtype.UUID
	if err := proposalID.Scan(chi.URLParam(r, "proposalId")); err != nil {
		writeError(w, http.StatusBadRequest, "invalid proposal id")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	if err := qtx.LockAgentMemory(r.Context(), agent.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve proposal")
		return
	}
	p, err := qtx.GetAgentMemoryProposalForUpdate(r.Context(), db.GetAgentMemoryProposalForUpdateParams{ID: proposalID, AgentID: agent.ID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "proposal not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load proposal")
		return
	}
	if p.Status != "pending" {
		writeError(w, http.StatusConflict, "this proposal was already "+p.Status)
		return
	}
	if status == "accepted" {
		if err := applyAgentMemoryChange(r.Context(), qtx, agent.ID, p.Path, p.Action, p.Content, member.UserID); err != nil {
			var bad *agentMemoryUserError
			if errors.As(err, &bad) {
				writeError(w, http.StatusConflict, bad.Error())
				return
			}
			slog.Error("apply agent memory proposal failed", "proposal_id", uuidToString(p.ID), "error", err)
			writeError(w, http.StatusInternalServerError, "failed to apply proposal")
			return
		}
	}
	if _, err := qtx.ResolveAgentMemoryProposal(r.Context(), db.ResolveAgentMemoryProposalParams{
		ID: p.ID, Status: status, Note: p.Note, ResolvedBy: member.UserID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve proposal")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve proposal")
		return
	}
	slog.Info("agent memory proposal resolved", "agent_id", uuidToString(agent.ID), "proposal_id", uuidToString(p.ID), "status", status, "path", p.Path)
	resp, err := h.agentMemoryResponse(r.Context(), agent)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "proposal resolved but memory could not be reloaded")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// DaemonAgentMemoryChange is one memory change a daemon observed in a run.
type DaemonAgentMemoryChange struct {
	Path    string `json:"path"`
	Action  string `json:"action"`
	Content string `json:"content"`
	// BaseContent is what the run started from; nil when the file was not
	// delivered. A mismatch with the stored file marks a conflict.
	BaseContent *string `json:"base_content"`
	Origin      string  `json:"origin"`
}

type DaemonAgentMemoryChangesRequest struct {
	Changes []DaemonAgentMemoryChange `json:"changes"`
}

type DaemonAgentMemoryChangesResponse struct {
	Mode     string `json:"mode"`
	Proposed int    `json:"proposed"`
	Applied  int    `json:"applied"`
	Skipped  int    `json:"skipped"`
}

// agentMemoryReportWindow bounds how long after a run ended its memory
// changes are still accepted. The daemon reports before it marks the task
// complete, so this only covers a slow report racing the completion.
const agentMemoryReportWindow = 10 * time.Minute

// authorizeAgentMemoryReport admits only the daemon that ran the task: the
// caller must be the task's runtime — its daemon token, or the PAT of the
// runtime's owner, which is what a local daemon signs in with — never an
// agent's task token and never another workspace member. The task must be
// running or have just ended.
func (h *Handler) authorizeAgentMemoryReport(w http.ResponseWriter, r *http.Request) (db.AgentTaskQueue, bool) {
	task, wsID, ok := h.requireDaemonTaskAccessWithWorkspace(w, r, chi.URLParam(r, "taskId"))
	if !ok {
		return db.AgentTaskQueue{}, false
	}
	ctx := r.Context()
	if !task.RuntimeID.Valid {
		writeError(w, http.StatusForbidden, "this task has no runtime")
		return db.AgentTaskQueue{}, false
	}
	rt, err := h.Queries.GetAgentRuntime(ctx, task.RuntimeID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusForbidden, "this task's runtime no longer exists")
			return db.AgentTaskQueue{}, false
		}
		writeError(w, http.StatusInternalServerError, "failed to load runtime")
		return db.AgentTaskQueue{}, false
	}
	if daemonID := middleware.DaemonIDFromContext(ctx); daemonID != "" {
		if daemonID != rt.DaemonID.String && daemonID != rt.LegacyDaemonID.String {
			writeError(w, http.StatusForbidden, "only the daemon that ran this task can report its memory changes")
			return db.AgentTaskQueue{}, false
		}
	} else {
		userID := requestUserID(r)
		if actorType, _ := h.resolveActor(r, userID, wsID); actorType == "agent" {
			writeError(w, http.StatusForbidden, "agents may not report memory changes")
			return db.AgentTaskQueue{}, false
		}
		if userID == "" || !rt.OwnerID.Valid || uuidToString(rt.OwnerID) != userID {
			writeError(w, http.StatusForbidden, "only the daemon that ran this task can report its memory changes")
			return db.AgentTaskQueue{}, false
		}
	}
	switch {
	case task.Status == "running":
	case task.CompletedAt.Valid && time.Since(task.CompletedAt.Time) <= agentMemoryReportWindow:
	default:
		writeError(w, http.StatusConflict, "memory changes are only accepted while the task is finishing")
		return db.AgentTaskQueue{}, false
	}
	return task, true
}

// ReportAgentMemoryChanges records the memory changes of a finished run as
// proposals and, in auto mode, applies the ones without conflicts. Each task
// reports once.
func (h *Handler) ReportAgentMemoryChanges(w http.ResponseWriter, r *http.Request) {
	task, ok := h.authorizeAgentMemoryReport(w, r)
	if !ok {
		return
	}
	var req DaemonAgentMemoryChangesRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, agentMemoryMaxBodyBytes)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Changes) > 2*agentMemoryMaxFiles {
		writeError(w, http.StatusBadRequest, "too many memory changes")
		return
	}
	ctx := r.Context()
	settings, err := h.loadAgentMemorySettings(ctx, task.AgentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load memory settings")
		return
	}
	resp := DaemonAgentMemoryChangesResponse{Mode: settings.Writeback}
	if settings.Writeback == AgentMemoryWritebackOff {
		resp.Skipped = len(req.Changes)
		writeJSON(w, http.StatusOK, resp)
		return
	}

	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(ctx)
	qtx := h.Queries.WithTx(tx)
	if err := qtx.LockAgentMemory(ctx, task.AgentID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record memory changes")
		return
	}
	recorded, err := qtx.RecordAgentMemoryReport(ctx, db.RecordAgentMemoryReportParams{TaskID: task.ID, AgentID: task.AgentID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record memory changes")
		return
	}
	if recorded == 0 {
		writeError(w, http.StatusConflict, "memory changes for this task were already reported")
		return
	}
	current, err := agentMemoryFilesByPath(ctx, qtx, task.AgentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agent memory")
		return
	}
	seen := map[string]bool{}
	for _, c := range req.Changes {
		path, perr := normalizeAgentMemoryPath(c.Path)
		valid := perr == nil && !seen[strings.ToLower(path)] &&
			(c.Action == "upsert" || c.Action == "delete") &&
			(c.Origin == AgentMemoryOriginAgent || (c.Origin == AgentMemoryOriginRuntime && settings.CaptureRuntimeMemory)) &&
			utf8.ValidString(c.Content) && !strings.ContainsRune(c.Content, 0) && len(c.Content) <= agentMemoryMaxFileBytes
		if !valid {
			resp.Skipped++
			continue
		}
		seen[strings.ToLower(path)] = true
		content := c.Content
		if c.Action == "delete" {
			content = ""
		}
		cur, exists := current[path]
		if (c.Action == "upsert" && exists && cur.Content == content) || (c.Action == "delete" && !exists) {
			resp.Skipped++
			continue
		}
		conflict := exists
		var base pgtype.Text
		if c.BaseContent != nil {
			base = pgtype.Text{String: *c.BaseContent, Valid: true}
			conflict = !exists || cur.Content != *c.BaseContent
		}
		if err := qtx.SupersedePendingAgentMemoryProposals(ctx, db.SupersedePendingAgentMemoryProposalsParams{AgentID: task.AgentID, Path: path}); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to record memory change")
			return
		}
		p, err := qtx.CreateAgentMemoryProposal(ctx, db.CreateAgentMemoryProposalParams{
			AgentID: task.AgentID, TaskID: task.ID, Path: path, Action: c.Action,
			Content: content, BaseContent: base, Origin: c.Origin,
		})
		if err != nil {
			slog.Error("create agent memory proposal failed", "task_id", uuidToString(task.ID), "error", err)
			writeError(w, http.StatusInternalServerError, "failed to record memory change")
			return
		}
		resp.Proposed++
		note := ""
		switch {
		case conflict:
			note = "the stored file changed after this run started"
		case exists && cur.AutoSync:
			note = "the stored file syncs from its source; accepting stops the sync"
		case settings.Writeback == AgentMemoryWritebackAuto:
			if err := applyAgentMemoryChange(ctx, qtx, task.AgentID, path, c.Action, content, pgtype.UUID{}); err != nil {
				var bad *agentMemoryUserError
				if !errors.As(err, &bad) {
					slog.Error("auto-apply agent memory change failed", "task_id", uuidToString(task.ID), "path", path, "error", err)
					writeError(w, http.StatusInternalServerError, "failed to apply memory change")
					return
				}
				note = bad.Error()
				break
			}
			if _, err := qtx.ResolveAgentMemoryProposal(ctx, db.ResolveAgentMemoryProposalParams{ID: p.ID, Status: "accepted"}); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to apply memory change")
				return
			}
			resp.Applied++
			if current, err = agentMemoryFilesByPath(ctx, qtx, task.AgentID); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to reload agent memory")
				return
			}
		}
		if note != "" {
			if err := qtx.SetAgentMemoryProposalNote(ctx, db.SetAgentMemoryProposalNoteParams{ID: p.ID, Note: note}); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to record memory change")
				return
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record memory changes")
		return
	}
	if resp.Proposed > 0 {
		slog.Info("agent memory changes reported", "task_id", uuidToString(task.ID), "agent_id", uuidToString(task.AgentID),
			"mode", settings.Writeback, "proposed", resp.Proposed, "applied", resp.Applied, "skipped", resp.Skipped)
	}
	writeJSON(w, http.StatusOK, resp)
}

func agentMemoryFilesByPath(ctx context.Context, q *db.Queries, agentID pgtype.UUID) (map[string]db.AgentMemoryFile, error) {
	rows, err := q.ListAgentMemoryFiles(ctx, agentID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]db.AgentMemoryFile, len(rows))
	for _, row := range rows {
		out[row.Path] = row
	}
	return out, nil
}

// agentMemoryWritebackForClaim is the policy sent to a memory-aware daemon.
// Lookup failures dispatch without write-back rather than failing the claim.
func (h *Handler) agentMemoryWritebackForClaim(ctx context.Context, agentID pgtype.UUID) *AgentMemoryWritebackData {
	settings, err := h.loadAgentMemorySettings(ctx, agentID)
	if err != nil {
		slog.Warn("task claim: agent memory settings load failed; dispatching without write-back", "agent_id", uuidToString(agentID), "error", err)
		return nil
	}
	if settings.Writeback == AgentMemoryWritebackOff {
		return nil
	}
	return &AgentMemoryWritebackData{Mode: settings.Writeback, CaptureRuntime: settings.CaptureRuntimeMemory}
}

// maybeEnqueueAgentMemorySync is the heartbeat hook for source sync: at most
// every agentMemorySyncInterval per runtime, it asks the daemon to re-read
// the source files that auto-synced memory mirrors. The result is applied by
// applyAgentMemorySync when the read reports back.
func (h *Handler) maybeEnqueueAgentMemorySync(ctx context.Context, runtimeID string) {
	if h.LocalMemoryStore == nil {
		return
	}
	now := time.Now()
	if last, ok := agentMemorySyncLast.Load(runtimeID); ok && now.Sub(last.(time.Time)) < agentMemorySyncInterval {
		return
	}
	agentMemorySyncLast.Store(runtimeID, now)
	var rid pgtype.UUID
	if err := rid.Scan(runtimeID); err != nil {
		return
	}
	qctx, cancel := context.WithTimeout(ctx, heartbeatHasPendingTimeout)
	keys, err := h.Queries.ListAgentMemorySyncKeys(qctx, rid)
	cancel()
	if err != nil {
		if !isUndefinedTable(err) && !errors.Is(err, context.DeadlineExceeded) {
			slog.Warn("agent memory sync: list keys failed", "runtime_id", runtimeID, "error", err)
		}
		return
	}
	if len(keys) == 0 {
		return
	}
	if len(keys) > runtimeLocalMemoryMaxReadKeys {
		keys = keys[:runtimeLocalMemoryMaxReadKeys]
	}
	if _, err := h.LocalMemoryStore.Create(ctx, runtimeID, "", RuntimeLocalMemoryKindRead, keys); err != nil {
		slog.Warn("agent memory sync: enqueue read failed", "runtime_id", runtimeID, "error", err)
	}
}

// applyAgentMemorySync refreshes auto-synced files from a completed read on
// their source runtime, whoever requested the read.
func (h *Handler) applyAgentMemorySync(ctx context.Context, runtimeID string, files []RuntimeLocalMemoryFile) {
	var rid pgtype.UUID
	if err := rid.Scan(runtimeID); err != nil {
		return
	}
	for _, f := range files {
		rows, err := h.Queries.ApplyAgentMemorySync(ctx, db.ApplyAgentMemorySyncParams{Content: f.Content, RuntimeID: rid, SourcePath: f.Key})
		if err != nil {
			if !isUndefinedTable(err) {
				slog.Warn("agent memory sync: apply failed", "runtime_id", runtimeID, "key", f.Key, "error", err)
			}
			return
		}
		if len(rows) > 0 {
			slog.Debug("agent memory sync applied", "runtime_id", runtimeID, "key", f.Key, "files", len(rows))
		}
	}
}
