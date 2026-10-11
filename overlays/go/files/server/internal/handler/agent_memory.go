package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Agent memory (multica-team overlay).
//
// An agent owns a small set of Markdown/text "memory" files. They are edited
// on the agent's Memory tab — typed by hand, uploaded from the browser, or
// imported from the runtime machine — and saved as one set through
// PUT /api/agents/{id}/memory. Every claimed task carries the set in
// TaskAgentData.Memory; the daemon writes the files under
// .multica/memory/ in the task workdir and renders them into the runtime
// brief (CLAUDE.md / AGENTS.md) so the agent starts every run with them.
//
// Memory can hold private notes copied from the owner's machine, so reading
// and writing it follows the env-management rule: the agent owner or a
// workspace owner/admin, never an agent actor.

const (
	agentMemoryMaxFiles      = 64
	agentMemoryMaxFileBytes  = 256 << 10
	agentMemoryMaxTotalBytes = 1 << 20
	agentMemoryMaxPathLength = 200
	agentMemoryMaxPathDepth  = 8
	// JSON escaping can roughly double Markdown with many quotes/newlines;
	// the decoded content is re-checked against the real limits below.
	agentMemoryMaxBodyBytes = 4 * agentMemoryMaxTotalBytes

	AgentMemorySourceManual  = "manual"
	AgentMemorySourceUpload  = "upload"
	AgentMemorySourceRuntime = "runtime"
)

type AgentMemoryLimits struct {
	MaxFiles      int `json:"max_files"`
	MaxFileBytes  int `json:"max_file_bytes"`
	MaxTotalBytes int `json:"max_total_bytes"`
}

func agentMemoryLimits() AgentMemoryLimits {
	return AgentMemoryLimits{
		MaxFiles:      agentMemoryMaxFiles,
		MaxFileBytes:  agentMemoryMaxFileBytes,
		MaxTotalBytes: agentMemoryMaxTotalBytes,
	}
}

type AgentMemoryFileResponse struct {
	ID              string  `json:"id"`
	Path            string  `json:"path"`
	Content         string  `json:"content"`
	Source          string  `json:"source"`
	SourceRuntimeID *string `json:"source_runtime_id"`
	SourcePath      string  `json:"source_path"`
	Enabled         bool    `json:"enabled"`
	SizeBytes       int     `json:"size_bytes"`
	UpdatedBy       *string `json:"updated_by"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
}

type AgentMemoryResponse struct {
	AgentID string                    `json:"agent_id"`
	Files   []AgentMemoryFileResponse `json:"files"`
	Limits  AgentMemoryLimits         `json:"limits"`
}

type AgentMemoryFileInput struct {
	Path            string `json:"path"`
	Content         string `json:"content"`
	Source          string `json:"source,omitempty"`
	SourceRuntimeID string `json:"source_runtime_id,omitempty"`
	SourcePath      string `json:"source_path,omitempty"`
	// Enabled defaults to true when omitted, so older clients keep every file on.
	Enabled *bool `json:"enabled,omitempty"`
}

type UpdateAgentMemoryRequest struct {
	Files []AgentMemoryFileInput `json:"files"`
}

// AgentMemoryFileData is the claim-payload shape the daemon receives.
type AgentMemoryFileData struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Source  string `json:"source,omitempty"`
}

func agentMemoryFileToResponse(f db.AgentMemoryFile) AgentMemoryFileResponse {
	return AgentMemoryFileResponse{
		ID:              uuidToString(f.ID),
		Path:            f.Path,
		Content:         f.Content,
		Source:          f.Source,
		SourceRuntimeID: uuidToPtr(f.SourceRuntimeID),
		SourcePath:      f.SourcePath,
		Enabled:         f.Enabled,
		SizeBytes:       len(f.Content),
		UpdatedBy:       uuidToPtr(f.UpdatedBy),
		CreatedAt:       timestampToString(f.CreatedAt),
		UpdatedAt:       timestampToString(f.UpdatedAt),
	}
}

// normalizeAgentMemoryPath returns the canonical slash-separated relative
// path for a memory file, or an error describing why it is unusable. Paths
// end up on daemon machines of every OS, so characters Windows rejects are
// refused here too.
func normalizeAgentMemoryPath(raw string) (string, error) {
	p := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if p == "" {
		return "", errors.New("path is required")
	}
	if strings.HasPrefix(p, "/") {
		return "", errors.New("path must be relative")
	}
	for _, r := range p {
		if r == 0 || unicode.IsControl(r) || strings.ContainsRune(`<>:"|?*`, r) {
			return "", fmt.Errorf("path contains an unsupported character %q", r)
		}
	}
	cleaned := path.Clean(p)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errors.New("path must stay inside the memory directory")
	}
	if len(cleaned) > agentMemoryMaxPathLength {
		return "", fmt.Errorf("path is longer than %d characters", agentMemoryMaxPathLength)
	}
	segments := strings.Split(cleaned, "/")
	if len(segments) > agentMemoryMaxPathDepth {
		return "", fmt.Errorf("path is nested deeper than %d levels", agentMemoryMaxPathDepth)
	}
	for _, seg := range segments {
		if seg == "" || seg == "." || seg == ".." {
			return "", errors.New("path has an empty or relative segment")
		}
		if strings.HasPrefix(seg, ".") {
			return "", errors.New("path segments may not start with a dot")
		}
		if strings.HasSuffix(seg, " ") || strings.HasSuffix(seg, ".") {
			return "", errors.New("path segments may not end with a space or dot")
		}
	}
	return cleaned, nil
}

type validatedAgentMemoryFile struct {
	path            string
	content         string
	source          string
	sourceRuntimeID pgtype.UUID
	sourcePath      string
	enabled         bool
}

// validateAgentMemoryFiles normalizes and checks a full replacement set.
func validateAgentMemoryFiles(inputs []AgentMemoryFileInput) ([]validatedAgentMemoryFile, error) {
	if len(inputs) > agentMemoryMaxFiles {
		return nil, fmt.Errorf("an agent can keep at most %d memory files", agentMemoryMaxFiles)
	}
	out := make([]validatedAgentMemoryFile, 0, len(inputs))
	seen := make(map[string]string, len(inputs))
	total := 0
	for _, in := range inputs {
		p, err := normalizeAgentMemoryPath(in.Path)
		if err != nil {
			return nil, fmt.Errorf("invalid memory file path %q: %v", in.Path, err)
		}
		folded := strings.ToLower(p)
		if prev, dup := seen[folded]; dup {
			return nil, fmt.Errorf("duplicate memory file path %q (also %q)", p, prev)
		}
		seen[folded] = p
		if !utf8.ValidString(in.Content) || strings.ContainsRune(in.Content, 0) {
			return nil, fmt.Errorf("memory file %q must be UTF-8 text", p)
		}
		if len(in.Content) > agentMemoryMaxFileBytes {
			return nil, fmt.Errorf("memory file %q is larger than %d KiB", p, agentMemoryMaxFileBytes>>10)
		}
		total += len(in.Content)
		if total > agentMemoryMaxTotalBytes {
			return nil, fmt.Errorf("memory files exceed %d KiB in total", agentMemoryMaxTotalBytes>>10)
		}

		source := strings.TrimSpace(in.Source)
		if source == "" {
			source = AgentMemorySourceManual
		}
		var runtimeID pgtype.UUID
		sourcePath := ""
		switch source {
		case AgentMemorySourceManual:
		case AgentMemorySourceUpload:
			sourcePath = truncateRunes(strings.TrimSpace(in.SourcePath), 512)
		case AgentMemorySourceRuntime:
			sourcePath = truncateRunes(strings.TrimSpace(in.SourcePath), 512)
			if rid := strings.TrimSpace(in.SourceRuntimeID); rid != "" {
				if err := runtimeID.Scan(rid); err != nil {
					return nil, fmt.Errorf("memory file %q has an invalid source_runtime_id", p)
				}
			}
		default:
			return nil, fmt.Errorf("memory file %q has an unknown source %q", p, source)
		}
		out = append(out, validatedAgentMemoryFile{
			path:            p,
			content:         in.Content,
			source:          source,
			sourceRuntimeID: runtimeID,
			sourcePath:      sourcePath,
			enabled:         in.Enabled == nil || *in.Enabled,
		})
	}
	// A path cannot be both a file and the parent directory of another file.
	for folded, p := range seen {
		parts := strings.Split(folded, "/")
		for i := 1; i < len(parts); i++ {
			if other, clash := seen[strings.Join(parts[:i], "/")]; clash {
				return nil, fmt.Errorf("memory file %q conflicts with the folder of %q", other, p)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, nil
}

func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}

// authorizeAgentMemory applies the env-management rule to memory: the agent
// owner or a workspace owner/admin; agent actors are always refused.
func (h *Handler) authorizeAgentMemory(w http.ResponseWriter, r *http.Request) (db.Agent, db.Member, bool) {
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return db.Agent{}, db.Member{}, false
	}
	workspaceID := uuidToString(agent.WorkspaceID)
	if actorType, _ := h.resolveActor(r, requestUserID(r), workspaceID); actorType == "agent" {
		writeError(w, http.StatusForbidden, "agents may not access memory management endpoints")
		return db.Agent{}, db.Member{}, false
	}
	member, ok := h.requireWorkspaceRole(w, r, workspaceID, "agent not found", "owner", "admin", "member")
	if !ok {
		return db.Agent{}, db.Member{}, false
	}
	if !canManageAgentEnv(agent, member) {
		writeError(w, http.StatusForbidden, "only the agent owner or a workspace owner/admin can manage this agent's memory")
		return db.Agent{}, db.Member{}, false
	}
	return agent, member, true
}

func (h *Handler) agentMemoryResponse(ctx context.Context, agent db.Agent) (AgentMemoryResponse, error) {
	rows, err := h.Queries.ListAgentMemoryFiles(ctx, agent.ID)
	if err != nil {
		return AgentMemoryResponse{}, err
	}
	files := make([]AgentMemoryFileResponse, 0, len(rows))
	for _, f := range rows {
		files = append(files, agentMemoryFileToResponse(f))
	}
	return AgentMemoryResponse{
		AgentID: uuidToString(agent.ID),
		Files:   files,
		Limits:  agentMemoryLimits(),
	}, nil
}

// GetAgentMemory returns every memory file of an agent, with content.
func (h *Handler) GetAgentMemory(w http.ResponseWriter, r *http.Request) {
	agent, _, ok := h.authorizeAgentMemory(w, r)
	if !ok {
		return
	}
	resp, err := h.agentMemoryResponse(r.Context(), agent)
	if err != nil {
		slog.Error("list agent memory failed", "agent_id", uuidToString(agent.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load agent memory")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// UpdateAgentMemory replaces the agent's memory set. Files absent from the
// request are deleted; unchanged files keep their updated_at/updated_by.
func (h *Handler) UpdateAgentMemory(w http.ResponseWriter, r *http.Request) {
	agent, member, ok := h.authorizeAgentMemory(w, r)
	if !ok {
		return
	}
	var req UpdateAgentMemoryRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, agentMemoryMaxBodyBytes))
	if err := dec.Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "memory payload is too large")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Files == nil {
		writeError(w, http.StatusBadRequest, "files is required")
		return
	}
	files, err := validateAgentMemoryFiles(req.Files)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	keep := make([]string, 0, len(files))
	for _, f := range files {
		keep = append(keep, f.path)
	}
	if err := qtx.DeleteAgentMemoryFilesExcept(r.Context(), db.DeleteAgentMemoryFilesExceptParams{
		AgentID:   agent.ID,
		KeepPaths: keep,
	}); err != nil {
		slog.Error("delete agent memory failed", "agent_id", uuidToString(agent.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save agent memory")
		return
	}
	for _, f := range files {
		if _, err := qtx.UpsertAgentMemoryFile(r.Context(), db.UpsertAgentMemoryFileParams{
			AgentID:         agent.ID,
			Path:            f.path,
			Content:         f.content,
			Source:          f.source,
			SourceRuntimeID: f.sourceRuntimeID,
			SourcePath:      f.sourcePath,
			Enabled:         f.enabled,
			UpdatedBy:       member.UserID,
		}); err != nil {
			slog.Error("upsert agent memory failed", "agent_id", uuidToString(agent.ID), "path", f.path, "error", err)
			writeError(w, http.StatusInternalServerError, "failed to save agent memory")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save agent memory")
		return
	}

	resp, err := h.agentMemoryResponse(r.Context(), agent)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "agent memory saved but could not be reloaded")
		return
	}
	slog.Info("agent memory updated", "agent_id", uuidToString(agent.ID), "files", len(files), "user_id", uuidToString(member.UserID))
	writeJSON(w, http.StatusOK, resp)
}

// loadAgentMemoryForClaim returns the enabled memory files for a claimed task. A
// deployment whose team migrations have not run yet (no agent_memory_file
// table) dispatches without memory instead of failing every claim.
func (h *Handler) loadAgentMemoryForClaim(ctx context.Context, agentID pgtype.UUID) ([]AgentMemoryFileData, error) {
	rows, err := h.Queries.ListAgentMemoryFiles(ctx, agentID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42P01" {
			slog.Warn("agent_memory_file table missing; run team migration 467", "agent_id", uuidToString(agentID))
			return nil, nil
		}
		return nil, err
	}
	var out []AgentMemoryFileData
	for _, f := range rows {
		if !f.Enabled {
			continue
		}
		out = append(out, AgentMemoryFileData{Path: f.Path, Content: f.Content, Source: f.Source})
	}
	return out, nil
}

// agentMemoryInstructionsBudget bounds how much memory is folded into
// Instructions for daemons that cannot materialize memory files. It matches
// the daemon's own inline budget; a file is inlined whole or not at all.
const agentMemoryInstructionsBudget = 48 << 10

// renderAgentMemoryInstructions renders memory as an "## Agent Memory"
// section for daemons that predate agent-memory-v1. Those daemons have no
// .multica/memory/ copy, so files beyond the budget can only be named.
func renderAgentMemoryInstructions(files []AgentMemoryFileData) string {
	if len(files) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Agent Memory\n\n")
	b.WriteString("Your owner configured the memory below for you: durable notes, preferences and context carried across runs. Use it as background knowledge; the current task's instructions win when they conflict. When something should be remembered for future runs, say so in your final reply so the owner can update the agent's Memory tab.\n")
	used := 0
	var omitted []string
	for _, f := range files {
		content := neutralizeBriefMarkers(f.Content)
		if used+len(content) > agentMemoryInstructionsBudget {
			omitted = append(omitted, f.Path)
			continue
		}
		used += len(content)
		fence := markdownFence(content)
		fmt.Fprintf(&b, "\n### `%s`\n\n%s\n%s\n%s\n", f.Path, fence, strings.TrimRight(content, "\n"), fence)
	}
	if len(omitted) > 0 {
		b.WriteString("\nThese memory files were too large to include on this machine's Multica version and are not available in this run: ")
		for i, p := range omitted {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "`%s`", p)
		}
		b.WriteString(".\n")
	}
	return b.String()
}

// neutralizeBriefMarkers keeps memory text from closing or reopening the
// daemon's managed brief block.
func neutralizeBriefMarkers(s string) string {
	s = strings.ReplaceAll(s, "<!-- BEGIN MULTICA-RUNTIME", "<!-- (memory) BEGIN MULTICA-RUNTIME")
	return strings.ReplaceAll(s, "<!-- END MULTICA-RUNTIME", "<!-- (memory) END MULTICA-RUNTIME")
}

// markdownFence returns a backtick fence longer than any backtick run in s.
func markdownFence(s string) string {
	longest, run := 0, 0
	for _, r := range s {
		if r == '`' {
			run++
			if run > longest {
				longest = run
			}
			continue
		}
		run = 0
	}
	if longest < 3 {
		return "```"
	}
	return strings.Repeat("`", longest+1)
}
