package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/pkg/agent"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ---------------------------------------------------------------------------
// Custom Runtime Profiles (MUL-3284)
//
// A runtime_profile is a workspace-level, team-shared definition of a custom
// runtime — e.g. an in-house Codex wrapper. Daemons pull the enabled profiles
// for their workspace, resolve command_name on PATH, and register an
// agent_runtime instance carrying the profile_id. The profile only changes how
// a runtime is launched/displayed; the underlying protocol_family must be a
// backend Multica officially supports (validated against agent.SupportedTypes).
//
// Iron rule: a profile carries NO generic per-agent args. Per-agent launch args
// stay on agent.custom_args. The only args field is fixed_args — args every
// agent on this runtime must inherit to enter a compatible mode.
// ---------------------------------------------------------------------------

type RuntimeProfileResponse struct {
	ID             string   `json:"id"`
	WorkspaceID    string   `json:"workspace_id"`
	DisplayName    string   `json:"display_name"`
	ProtocolFamily string   `json:"protocol_family"`
	CommandName    string   `json:"command_name"`
	Description    *string  `json:"description"`
	FixedArgs      []string `json:"fixed_args"`
	Visibility     string   `json:"visibility"`
	CreatedBy      *string  `json:"created_by"`
	Enabled        bool     `json:"enabled"`
	CreatedAt      string   `json:"created_at"`
	UpdatedAt      string   `json:"updated_at"`
}

func runtimeProfileToResponse(p db.RuntimeProfile) RuntimeProfileResponse {
	args := []string{}
	if len(p.FixedArgs) > 0 {
		_ = json.Unmarshal(p.FixedArgs, &args)
		if args == nil {
			args = []string{}
		}
	}
	return RuntimeProfileResponse{
		ID:             uuidToString(p.ID),
		WorkspaceID:    uuidToString(p.WorkspaceID),
		DisplayName:    p.DisplayName,
		ProtocolFamily: p.ProtocolFamily,
		CommandName:    p.CommandName,
		Description:    textToPtr(p.Description),
		FixedArgs:      args,
		Visibility:     p.Visibility,
		CreatedBy:      uuidToPtr(p.CreatedBy),
		Enabled:        p.Enabled,
		CreatedAt:      timestampToString(p.CreatedAt),
		UpdatedAt:      timestampToString(p.UpdatedAt),
	}
}

// Automatic internal runtime profiles (ducx/ducc) are materialized for the
// authenticated user when a daemon asks for its workspace profiles. They are
// private-by-owner in daemon responses even though the v1 schema predates
// visibility enforcement for arbitrary profiles.
const internalRuntimeProfileMarker = "multica:auto-internal-runtime:"

type internalRuntimeProfileDefinition struct {
	commandName    string
	protocolFamily string
}

var internalRuntimeProfileDefinitions = []internalRuntimeProfileDefinition{
	{commandName: "ducx", protocolFamily: "codex"},
	{commandName: "ducc", protocolFamily: "claude"},
}

func isInternalRuntimeProfile(profile db.RuntimeProfile) bool {
	if !profile.Description.Valid {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(profile.Description.String), internalRuntimeProfileMarker)
}

func internalRuntimeProfileBelongsTo(profile db.RuntimeProfile, userID string) bool {
	return isInternalRuntimeProfile(profile) && uuidToString(profile.CreatedBy) == strings.TrimSpace(userID)
}

func runtimeProfileVisibleToUser(profile db.RuntimeProfile, userID string) bool {
	if !isInternalRuntimeProfile(profile) {
		return true
	}
	return internalRuntimeProfileBelongsTo(profile, userID)
}

func internalRuntimeUsername(user db.User) string {
	email := strings.TrimSpace(user.Email)
	at := strings.IndexByte(email, '@')
	if at <= 0 {
		return ""
	}
	username := strings.TrimSpace(email[:at])
	if username == "" || strings.ContainsAny(username, " \t\r\n\x00") {
		return ""
	}
	return username
}

// ensureInternalRuntimeProfiles creates or refreshes the two managed profiles
// for the authenticated daemon owner. The username is derived from the
// server-side email row, never from a daemon-supplied string.
func (h *Handler) ensureInternalRuntimeProfiles(ctx context.Context, workspaceID pgtype.UUID, userID string) error {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil
	}
	user, err := h.Queries.GetUser(ctx, parseUUID(userID))
	if err != nil {
		return fmt.Errorf("load daemon user: %w", err)
	}
	username := internalRuntimeUsername(user)
	if username == "" {
		return nil
	}
	ownerID := parseUUID(userID)
	const insertSQL = "INSERT INTO runtime_profile (" +
		"workspace_id, display_name, protocol_family, command_name, " +
		"description, fixed_args, visibility, created_by, enabled" +
		") VALUES ($1, $2, $3, $4, $5, $6, 'workspace', $7, true) " +
		"ON CONFLICT (workspace_id, display_name) DO UPDATE SET " +
		"protocol_family = EXCLUDED.protocol_family, " +
		"command_name = EXCLUDED.command_name, " +
		"description = EXCLUDED.description, fixed_args = EXCLUDED.fixed_args, " +
		"visibility = EXCLUDED.visibility, enabled = true, updated_at = now() " +
		"WHERE runtime_profile.created_by = EXCLUDED.created_by " +
		"AND runtime_profile.description LIKE 'multica:auto-internal-runtime:%'"
	for _, spec := range internalRuntimeProfileDefinitions {
		displayName := fmt.Sprintf("%s (%s)", spec.commandName, username)
		fixedArgs, marshalErr := json.Marshal([]string{"--username", username})
		if marshalErr != nil {
			return fmt.Errorf("marshal %s fixed_args: %w", spec.commandName, marshalErr)
		}
		description := internalRuntimeProfileMarker + spec.commandName
		if _, execErr := h.DB.Exec(ctx, insertSQL, workspaceID, displayName, spec.protocolFamily, spec.commandName, description, fixedArgs, ownerID); execErr != nil {
			return fmt.Errorf("ensure %s profile: %w", spec.commandName, execErr)
		}
	}
	return nil
}

func filterRuntimeProfilesForUser(profiles []db.RuntimeProfile, userID string) []db.RuntimeProfile {
	filtered := profiles[:0]
	for _, profile := range profiles {
		if runtimeProfileVisibleToUser(profile, userID) {
			filtered = append(filtered, profile)
		}
	}
	return filtered
}

// NOTE: runtime_profile.visibility is intentionally NOT user-settable in v1.
// The column exists and the API still returns it, but creation always forces
// 'workspace': the daemon-pull, DaemonRegister and ListRuntimeProfiles read
// paths do not yet enforce 'private', so accepting 'private' from a client
// would silently leak a "private" profile's name/command to other members and
// let other machines' daemons register it (lateral data leak). Re-expose a
// visibility control only once those read paths enforce creator visibility.
// Follow-up: MUL-3308.
const runtimeProfileDefaultVisibility = "workspace"

// marshalFixedArgs validates and JSON-encodes the fixed_args list. Each entry
// must be a non-empty string; the column defaults to an empty array.
func marshalFixedArgs(args []string) ([]byte, error) {
	if len(args) == 0 {
		return []byte("[]"), nil
	}
	clean := make([]string, 0, len(args))
	for _, a := range args {
		// fixed_args are launch flags inherited by every agent on the runtime;
		// blank entries are always a client mistake.
		if strings.TrimSpace(a) == "" {
			return nil, errors.New("fixed_args entries must be non-empty")
		}
		if strings.ContainsRune(a, '\x00') {
			return nil, errors.New("fixed_args entries cannot contain NUL bytes")
		}
		clean = append(clean, a)
	}
	return json.Marshal(clean)
}

func validateRuntimeProfileCommandName(commandName string) error {
	if commandName == "" {
		return errors.New("command_name is required")
	}
	if strings.ContainsAny(commandName, " \t\r\n") {
		return errors.New("command_name must be a single executable token; put arguments in fixed_args")
	}
	if strings.ContainsRune(commandName, '\x00') {
		return errors.New("command_name cannot contain NUL bytes")
	}
	return nil
}

type createRuntimeProfileRequest struct {
	DisplayName    string   `json:"display_name"`
	ProtocolFamily string   `json:"protocol_family"`
	CommandName    string   `json:"command_name"`
	Description    *string  `json:"description"`
	FixedArgs      []string `json:"fixed_args"`
	Enabled        *bool    `json:"enabled"`
}

// CreateRuntimeProfile creates a workspace runtime profile. Admin-gated by the
// router. protocol_family is validated against the agent backend whitelist.
func (h *Handler) CreateRuntimeProfile(w http.ResponseWriter, r *http.Request) {
	wsID := strings.TrimSpace(chi.URLParam(r, "id"))
	member, ok := h.requireWorkspaceMember(w, r, wsID, "workspace not found")
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, wsID, "workspace id")
	if !ok {
		return
	}

	var req createRuntimeProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.DisplayName = strings.TrimSpace(req.DisplayName)
	req.ProtocolFamily = strings.TrimSpace(req.ProtocolFamily)
	req.CommandName = strings.TrimSpace(req.CommandName)

	if req.DisplayName == "" {
		writeError(w, http.StatusBadRequest, "display_name is required")
		return
	}
	if !agent.IsSupportedType(req.ProtocolFamily) {
		writeError(w, http.StatusBadRequest, "unsupported protocol_family: must be one of "+strings.Join(agent.SupportedTypes, ", "))
		return
	}
	if req.CommandName == "" {
		writeError(w, http.StatusBadRequest, "command_name is required")
		return
	}
	if err := validateRuntimeProfileCommandName(req.CommandName); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	fixedArgs, err := marshalFixedArgs(req.FixedArgs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	profile, err := h.Queries.CreateRuntimeProfile(r.Context(), db.CreateRuntimeProfileParams{
		WorkspaceID:    wsUUID,
		DisplayName:    req.DisplayName,
		ProtocolFamily: req.ProtocolFamily,
		CommandName:    req.CommandName,
		Description:    ptrToText(req.Description),
		FixedArgs:      fixedArgs,
		Visibility:     runtimeProfileDefaultVisibility,
		CreatedBy:      member.UserID,
		Enabled:        enabled,
	})
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a runtime profile with this display_name already exists")
			return
		}
		slog.Error("CreateRuntimeProfile failed", "error", err, "workspace_id", wsID)
		writeError(w, http.StatusInternalServerError, "failed to create runtime profile")
		return
	}

	profileID := uuidToString(profile.ID)
	h.requestDaemonRuntimeProfileRefresh(wsID, profileID)
	h.publish(protocol.EventDaemonRegister, wsID, "member", uuidToString(member.UserID), map[string]any{
		"runtime_profile_id": profileID,
	})

	writeJSON(w, http.StatusCreated, runtimeProfileToResponse(profile))
}

// ListRuntimeProfiles returns every runtime profile in the workspace.
// Member-gated by the router.
func (h *Handler) ListRuntimeProfiles(w http.ResponseWriter, r *http.Request) {
	wsID := strings.TrimSpace(chi.URLParam(r, "id"))
	if _, ok := h.requireWorkspaceMember(w, r, wsID, "workspace not found"); !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, wsID, "workspace id")
	if !ok {
		return
	}

	profiles, err := h.Queries.ListRuntimeProfiles(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list runtime profiles")
		return
	}
	profiles = filterRuntimeProfilesForUser(profiles, requestUserID(r))
	resp := make([]RuntimeProfileResponse, len(profiles))
	for i, p := range profiles {
		resp[i] = runtimeProfileToResponse(p)
	}
	writeJSON(w, http.StatusOK, map[string]any{"runtime_profiles": resp})
}

// GetRuntimeProfile returns one runtime profile. Member-gated by the router.
func (h *Handler) GetRuntimeProfile(w http.ResponseWriter, r *http.Request) {
	wsID := strings.TrimSpace(chi.URLParam(r, "id"))
	if _, ok := h.requireWorkspaceMember(w, r, wsID, "workspace not found"); !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, wsID, "workspace id")
	if !ok {
		return
	}
	profileUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "profileId"), "profile id")
	if !ok {
		return
	}

	profile, err := h.Queries.GetRuntimeProfileForWorkspace(r.Context(), db.GetRuntimeProfileForWorkspaceParams{
		ID:          profileUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil || !runtimeProfileVisibleToUser(profile, requestUserID(r)) {
		writeError(w, http.StatusNotFound, "runtime profile not found")
		return
	}
	writeJSON(w, http.StatusOK, runtimeProfileToResponse(profile))
}

type updateRuntimeProfileRequest struct {
	DisplayName *string   `json:"display_name"`
	CommandName *string   `json:"command_name"`
	Description *string   `json:"description"`
	FixedArgs   *[]string `json:"fixed_args"`
	Enabled     *bool     `json:"enabled"`
}

// UpdateRuntimeProfile applies a partial update. protocol_family is immutable
// (changing it would silently repoint bound agents onto a different backend).
// Admin-gated by the router.
func (h *Handler) UpdateRuntimeProfile(w http.ResponseWriter, r *http.Request) {
	wsID := strings.TrimSpace(chi.URLParam(r, "id"))
	member, ok := h.requireWorkspaceMember(w, r, wsID, "workspace not found")
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, wsID, "workspace id")
	if !ok {
		return
	}
	profileUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "profileId"), "profile id")
	if !ok {
		return
	}
	existingProfile, profileErr := h.Queries.GetRuntimeProfileForWorkspace(r.Context(), db.GetRuntimeProfileForWorkspaceParams{
		ID:          profileUUID,
		WorkspaceID: wsUUID,
	})
	if profileErr != nil || !runtimeProfileVisibleToUser(existingProfile, requestUserID(r)) {
		writeError(w, http.StatusNotFound, "runtime profile not found")
		return
	}

	var req updateRuntimeProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	params := db.UpdateRuntimeProfileParams{ID: profileUUID, WorkspaceID: wsUUID}
	if req.DisplayName != nil {
		name := strings.TrimSpace(*req.DisplayName)
		if name == "" {
			writeError(w, http.StatusBadRequest, "display_name cannot be empty")
			return
		}
		params.DisplayName = strToText(name)
	}
	if req.CommandName != nil {
		cmd := strings.TrimSpace(*req.CommandName)
		if err := validateRuntimeProfileCommandName(cmd); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		params.CommandName = strToText(cmd)
	}
	if req.Description != nil {
		params.Description = ptrToText(req.Description)
	}
	if req.FixedArgs != nil {
		fixedArgs, err := marshalFixedArgs(*req.FixedArgs)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		params.FixedArgs = fixedArgs
	}
	if req.Enabled != nil {
		params.Enabled = pgtype.Bool{Bool: *req.Enabled, Valid: true}
	}

	profile, err := h.Queries.UpdateRuntimeProfile(r.Context(), params)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "runtime profile not found")
			return
		}
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a runtime profile with this display_name already exists")
			return
		}
		slog.Error("UpdateRuntimeProfile failed", "error", err, "profile_id", uuidToString(profileUUID))
		writeError(w, http.StatusInternalServerError, "failed to update runtime profile")
		return
	}

	profileID := uuidToString(profile.ID)
	h.requestDaemonRuntimeProfileRefresh(wsID, profileID)
	h.publish(protocol.EventDaemonRegister, wsID, "member", uuidToString(member.UserID), map[string]any{
		"runtime_profile_id": profileID,
	})

	writeJSON(w, http.StatusOK, runtimeProfileToResponse(profile))
}

// DeleteRuntimeProfile removes a profile and, in the same transaction, the
// agent_runtime instance rows registered against it. Migration 120 dropped the
// DB ON DELETE CASCADE, so this app-layer cleanup is what prevents orphaned
// runtime rows. Refuses (409) while active agents are still bound to the
// profile's runtimes. Admin-gated by the router.
func (h *Handler) DeleteRuntimeProfile(w http.ResponseWriter, r *http.Request) {
	wsID := strings.TrimSpace(chi.URLParam(r, "id"))
	member, ok := h.requireWorkspaceMember(w, r, wsID, "workspace not found")
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, wsID, "workspace id")
	if !ok {
		return
	}
	profileUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "profileId"), "profile id")
	if !ok {
		return
	}

	// The profile-delete cascade must run the SAME teardown the runtime-delete
	// path uses for each one: agent.runtime_id is ON DELETE RESTRICT, so an
	// agent still pointing at one of these rows would turn a bare delete into a
	// 500. Active agents are refused (409); everything else is unbound rather
	// than destroyed, exactly as in service.TeardownRuntime.
	// Guard: refuse while any active (non-archived) agent is bound to one of
	// the profile's runtimes. Keep this a 409 — the profile is the thing that
	// defines those runtimes, so the user should retire the agents or move them
	// deliberately instead of having them silently unbound in bulk.
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	// Lock the profile before planning the cascade. Daemon registration takes
	// a conflicting KEY SHARE lock in its own transaction, so it cannot insert
	// a runtime after the plan and have that row escape deletion. If the profile
	// row is already gone, still clean up any orphaned profile_id rows.
	lockedProfile, profileErr := qtx.LockRuntimeProfileForDelete(r.Context(), db.LockRuntimeProfileForDeleteParams{
		ID:          profileUUID,
		WorkspaceID: wsUUID,
	})
	profileMissing := errors.Is(profileErr, pgx.ErrNoRows)
	if profileErr == nil && !runtimeProfileVisibleToUser(lockedProfile, uuidToString(member.UserID)) {
		writeError(w, http.StatusNotFound, "runtime profile not found")
		return
	}
	if profileErr != nil && !profileMissing {
		writeError(w, http.StatusInternalServerError, "failed to load runtime profile")
		return
	}

	// Lock runtime rows in deterministic ID order. Their agent/task foreign-key
	// inserts take KEY SHARE locks, preventing dependencies from appearing after
	// the active-agent check.
	runtimeIDs, err := qtx.ListAgentRuntimeIDsByProfile(r.Context(), db.ListAgentRuntimeIDsByProfileParams{
		ProfileID:   profileUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to enumerate profile runtimes")
		return
	}
	if profileMissing && len(runtimeIDs) == 0 {
		writeError(w, http.StatusNotFound, "runtime profile not found")
		return
	}
	for _, runtimeID := range runtimeIDs {
		if _, err := qtx.ListUserAgentsByRuntimeForUpdate(r.Context(), runtimeID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to lock profile dependencies")
			return
		}
	}

	agentCount, err := qtx.CountAgentsByProfile(r.Context(), db.CountAgentsByProfileParams{
		ProfileID:   profileUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check profile usage")
		return
	}
	if agentCount > 0 {
		writeError(w, http.StatusConflict, "cannot delete runtime profile: active agents are still bound to its runtimes")
		return
	}

	// App-layer cascade, per runtime, mirroring DeleteAgentRuntime: unbind the
	// remaining (archived) agents and their task history, cancel anything still
	// in flight, and hard-delete only the system agents, so removing the runtime
	// rows below cannot destroy an agent, a conversation or a task record.
	var teardowns []service.RuntimeTeardownResult
	for _, rid := range runtimeIDs {
		teardown, err := service.TeardownRuntime(r.Context(), qtx, rid, service.RuntimeTeardownOptions{CancelNonTerminalTasks: true})
		if err != nil {
			if errors.Is(err, service.ErrRuntimeNotDrained) {
				slog.Error("runtime profile delete aborted: tasks not drained",
					"runtime_id", uuidToString(rid), "profile_id", uuidToString(profileUUID), "error", err)
				writeJSON(w, http.StatusConflict, map[string]any{
					"error": "a runtime of this profile still has tasks in flight; retry in a moment.",
					"code":  "runtime_delete_not_drained",
				})
				return
			}
			if errors.Is(err, service.ErrRuntimeWorkspaceMismatch) {
				slog.Error("runtime profile delete aborted: agent workspace mismatch",
					"runtime_id", uuidToString(rid), "profile_id", uuidToString(profileUUID), "error", err)
				writeJSON(w, http.StatusConflict, map[string]any{
					"error": "a runtime of this profile has an invalid cross-workspace agent binding.",
					"code":  "runtime_delete_workspace_mismatch",
				})
				return
			}
			slog.Error("runtime profile delete teardown failed",
				"runtime_id", uuidToString(rid), "profile_id", uuidToString(profileUUID), "error", err)
			writeError(w, http.StatusInternalServerError, "failed to unbind agents")
			return
		}
		teardowns = append(teardowns, teardown)
	}

	// Now the runtime rows have no agent references; remove them, then the
	// profile itself.
	deletedRuntimes, err := qtx.DeleteAgentRuntimesByProfile(r.Context(), db.DeleteAgentRuntimesByProfileParams{
		ProfileID:   profileUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		slog.Error("DeleteAgentRuntimesByProfile failed", "error", err, "profile_id", uuidToString(profileUUID))
		writeError(w, http.StatusInternalServerError, "failed to clean up runtime instances")
		return
	}
	if !profileMissing {
		if err := qtx.DeleteRuntimeProfile(r.Context(), db.DeleteRuntimeProfileParams{
			ID:          profileUUID,
			WorkspaceID: wsUUID,
		}); err != nil {
			slog.Error("DeleteRuntimeProfile failed", "error", err, "profile_id", uuidToString(profileUUID))
			writeError(w, http.StatusInternalServerError, "failed to delete runtime profile")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit transaction")
		return
	}
	for _, runtime := range deletedRuntimes {
		h.NotifyRuntimeGone(uuidToString(runtime.ID))
	}

	// Tell connected clients to refetch the runtime list (instances vanished),
	// and fan out the per-runtime teardown so unbound agents and cancelled
	// tasks reach subscribers the same way the runtime-delete path emits them.
	profileID := uuidToString(profileUUID)
	userID := uuidToString(member.UserID)
	for _, teardown := range teardowns {
		h.publishRuntimeTeardown(r.Context(), teardown, wsID, userID)
	}
	h.requestDaemonRuntimeProfileRefresh(wsID, profileID)
	h.publish(protocol.EventDaemonRegister, wsID, "member", userID, map[string]any{
		"deleted_runtime_profile_id": profileID,
	})

	w.WriteHeader(http.StatusNoContent)
}

// DaemonListRuntimeProfiles serves the enabled runtime profiles for a workspace
// to a daemon. The daemon resolves each profile's command_name on PATH and
// registers an agent_runtime instance per profile it can run. Daemon-token
// gated by the router.
func (h *Handler) DaemonListRuntimeProfiles(w http.ResponseWriter, r *http.Request) {
	workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceId"))
	if !h.requireDaemonWorkspaceAccess(w, r, workspaceID) {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	userID := requestUserID(r)
	if userID != "" {
		if err := h.ensureInternalRuntimeProfiles(r.Context(), wsUUID, userID); err != nil {
			slog.Warn("ensure automatic internal runtime profiles failed", "workspace_id", workspaceID, "error", err)
		}
	}

	profiles, err := h.Queries.ListEnabledRuntimeProfilesForWorkspace(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list runtime profiles")
		return
	}
	profiles = filterRuntimeProfilesForUser(profiles, userID)
	resp := make([]RuntimeProfileResponse, len(profiles))
	for i, p := range profiles {
		resp[i] = runtimeProfileToResponse(p)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"workspace_id":     workspaceID,
		"runtime_profiles": resp,
	})
}

func (h *Handler) requestDaemonRuntimeProfileRefresh(workspaceID, profileID string) {
	if h.DaemonProfileRefresh == nil {
		return
	}
	h.DaemonProfileRefresh.NotifyRuntimeProfilesChanged(workspaceID, profileID)
}
