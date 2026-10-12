package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Retry only errors that PostgreSQL guarantees aborted the transaction. Never
// retry uncertain commits or arbitrary network errors, which could duplicate a
// write. Each callback must open/commit its own complete transaction.
func retryPersonalRuntimeTx(ctx context.Context, operation func() error) error {
	for attempt := 0; ; attempt++ {
		err := operation()
		var pgErr *pgconn.PgError
		if err == nil || attempt >= 6 || !errors.As(err, &pgErr) || (pgErr.Code != "40001" && pgErr.Code != "40P01") {
			return err
		}
		timer := time.NewTimer(time.Duration(20<<attempt) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func personalRuntimeRetired(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "55000"
}

func writePersonalRuntimeTeardownError(w http.ResponseWriter, err error) {
	if errors.Is(err, service.ErrRuntimeWorkspaceMismatch) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "runtime has an invalid cross-workspace agent binding; nothing was deleted", "code": "runtime_delete_workspace_mismatch"})
		return
	}
	if errors.Is(err, service.ErrRuntimeNotDrained) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "runtime still has unfinished runs; nothing was deleted", "code": "runtime_delete_not_drained"})
		return
	}
	writeError(w, http.StatusConflict, "runtime dependencies changed; nothing was deleted")
}

func daemonOwnsRuntime(r *http.Request, rt db.AgentRuntime) bool {
	// Credential scope takes precedence over all caller-controlled headers.
	if workspace := middleware.DaemonWorkspaceIDFromContext(r.Context()); workspace != "" {
		if workspace != uuidToString(rt.WorkspaceID) {
			return false
		}
		id := middleware.DaemonIDFromContext(r.Context())
		if rt.DaemonID.Valid && rt.DaemonID.String != "" {
			return id != "" && rt.DaemonID.String == id
		}
		return rt.RuntimeMode != "local"
	}
	user := requestUserID(r)
	return user != "" && rt.OwnerID.Valid && uuidToString(rt.OwnerID) == user
}

func requirePersonalRuntimeActor(w http.ResponseWriter, r *http.Request) bool {
	if isMachineCredentialActor(r) {
		writeError(w, http.StatusForbidden, "personal runtimes can only be managed by their human owner")
		return false
	}
	return true
}

// Preserve owner-initiated assistant workflows (including Mika creating an
// agent) without lending the host owner's rights to a shared-agent caller.
// Auth stamps these headers from the task token; request bodies cannot choose
// the originator. Terminal/unattributed runs and cloud-node PATs lend nothing.
func (h *Handler) ownerInitiatedAgentTask(r *http.Request) bool {
	if r.Header.Get("X-Actor-Source") != "task_token" {
		return false
	}
	user := requestUserID(r)
	if user == "" {
		return false
	}
	task, ok := h.taskFromRequestHeader(r)
	if !ok || isTerminalTaskStatus(task.Status) || !task.OriginatorUserID.Valid || uuidToString(task.OriginatorUserID) != user || uuidToString(task.AgentID) != r.Header.Get("X-Agent-ID") || !task.RuntimeID.Valid {
		return false
	}
	agent, err := h.Queries.GetAgent(r.Context(), task.AgentID)
	if err != nil || agent.ArchivedAt.Valid || !agent.OwnerID.Valid || uuidToString(agent.OwnerID) != user || uuidToString(agent.WorkspaceID) != h.resolveWorkspaceID(r) {
		return false
	}
	runtime, err := h.Queries.GetAgentRuntime(r.Context(), task.RuntimeID)
	return err == nil && runtime.OwnerID == agent.OwnerID && runtime.WorkspaceID == agent.WorkspaceID
}

func (h *Handler) requirePersonalRuntimeReadActor(w http.ResponseWriter, r *http.Request) bool {
	if !isMachineCredentialActor(r) || h.ownerInitiatedAgentTask(r) {
		return true
	}
	writeError(w, http.StatusForbidden, "shared-agent callers may invoke an agent but cannot inspect its owner's computers")
	return false
}

// Returns false only for a legacy/cloud row without a personal registration.
func (h *Handler) deletePersonalRuntime(w http.ResponseWriter, r *http.Request, rt db.AgentRuntime, confirmed bool, expected map[string]struct{}) bool {
	var canonical pgtype.UUID
	if err := h.DB.QueryRow(r.Context(), `SELECT personal_runtime_id FROM agent_runtime WHERE id=$1`, rt.ID).Scan(&canonical); err != nil {
		writeError(w, 500, "failed to load personal runtime")
		return true
	}
	if !canonical.Valid {
		return false
	}
	if !rt.OwnerID.Valid || uuidToString(rt.OwnerID) != requestUserID(r) {
		writeError(w, 404, "runtime not found")
		return true
	}
	ctx := r.Context()
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		writeError(w, 500, "failed to begin deletion")
		return true
	}
	defer tx.Rollback(ctx)
	if err = tx.QueryRow(ctx, `SELECT id FROM personal_runtime WHERE id=$1 AND owner_id=$2 FOR UPDATE`, canonical, rt.OwnerID).Scan(&canonical); err != nil {
		writeError(w, 404, "runtime not found")
		return true
	}
	rows, err := tx.Query(ctx, `SELECT id FROM agent_runtime WHERE personal_runtime_id=$1 ORDER BY id FOR UPDATE`, canonical)
	if err != nil {
		writeError(w, 500, "failed to lock runtime")
		return true
	}
	var ids []pgtype.UUID
	for rows.Next() {
		var id pgtype.UUID
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			writeError(w, 500, "failed to load runtimes")
			return true
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		writeError(w, 500, "failed to load runtimes")
		return true
	}
	q := h.Queries.WithTx(tx)
	var agents []db.Agent
	for _, id := range ids {
		if _, err = q.ListUserAgentsByRuntimeForUpdate(ctx, id); err != nil {
			writeError(w, 500, "failed to lock agents")
			return true
		}
		bound, e := q.ListActiveAgentsByRuntimeForUpdate(ctx, id)
		if e != nil {
			writeError(w, 500, "failed to list agents")
			return true
		}
		agents = append(agents, bound...)
	}
	if (!confirmed && len(agents) > 0) || (confirmed && !activeAgentSetMatches(agents, expected)) {
		body := h.runtimeHasActiveAgentsResponse(agents)
		if confirmed {
			body["code"] = "runtime_delete_plan_changed"
		}
		writeJSON(w, 409, body)
		return true
	}
	pending, e := q.CountUndrainedTasksByRuntimeOrAgent(ctx, db.CountUndrainedTasksByRuntimeOrAgentParams{RuntimeIds: ids})
	if e != nil {
		writeError(w, 500, "failed to check unfinished runs")
		return true
	}
	if pending > 0 {
		writePersonalRuntimeTeardownError(w, service.ErrRuntimeNotDrained)
		return true
	}
	var changes []service.RuntimeTeardownResult
	var scopes []string
	for _, id := range ids {
		row, e := q.GetAgentRuntime(ctx, id)
		if e != nil {
			writeError(w, 409, "runtime changed")
			return true
		}
		result, e := service.TeardownRuntime(ctx, q, id, service.RuntimeTeardownOptions{})
		if e != nil {
			writePersonalRuntimeTeardownError(w, e)
			return true
		}
		if e = q.DeleteAgentRuntime(ctx, id); e != nil {
			writeError(w, 500, "failed to delete runtime")
			return true
		}
		changes = append(changes, result)
		scopes = append(scopes, uuidToString(row.WorkspaceID))
	}
	if _, err = tx.Exec(ctx, `DELETE FROM personal_runtime WHERE id=$1`, canonical); err != nil {
		writeError(w, 500, "failed to delete personal runtime")
		return true
	}
	if err = tx.Commit(ctx); err != nil {
		writeError(w, 500, "failed to commit deletion")
		return true
	}
	for i, id := range ids {
		h.NotifyRuntimeGone(uuidToString(id))
		h.publishRuntimeTeardown(ctx, changes[i], scopes[i], requestUserID(r))
	}
	h.publishPersonalRuntimeRefresh(ctx, requestUserID(r))
	writeJSON(w, 200, map[string]string{"status": "ok"})
	return true
}

func (h *Handler) personalMachineRuntimes(ctx context.Context, q *db.Queries, sql db.DBTX, rt db.AgentRuntime, lock bool) ([]db.AgentRuntime, error) {
	if lock {
		rows, err := sql.Query(ctx, `SELECT id FROM personal_runtime WHERE owner_id=$1 AND daemon_id=$2 ORDER BY id FOR UPDATE`, rt.OwnerID, rt.DaemonID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	rows, err := sql.Query(ctx, `SELECT id FROM agent_runtime WHERE owner_id=$1 AND daemon_id=$2 AND runtime_mode='local' ORDER BY id`, rt.OwnerID, rt.DaemonID)
	if err != nil {
		return nil, err
	}
	var ids []pgtype.UUID
	for rows.Next() {
		var id pgtype.UUID
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result := make([]db.AgentRuntime, 0, len(ids))
	for _, id := range ids {
		row, e := q.GetAgentRuntime(ctx, id)
		if e != nil {
			return nil, e
		}
		result = append(result, row)
	}
	return result, nil
}

func (h *Handler) GetPersonalMachineDeletePlan(w http.ResponseWriter, r *http.Request) {
	if !requirePersonalRuntimeActor(w, r) {
		return
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "runtimeId"), "runtime id")
	if !ok {
		return
	}
	rt, err := h.Queries.GetAgentRuntime(r.Context(), id)
	if err != nil || !rt.OwnerID.Valid || uuidToString(rt.OwnerID) != requestUserID(r) {
		writeError(w, 404, "runtime not found")
		return
	}
	all, err := h.personalMachineRuntimes(r.Context(), h.Queries, h.DB, rt, false)
	if err != nil {
		writeError(w, 500, "failed to plan deletion")
		return
	}
	ids := []string{}
	agents := []map[string]string{}
	for _, row := range all {
		ids = append(ids, uuidToString(row.ID))
		rows, e := h.DB.Query(r.Context(), `SELECT id,name FROM agent WHERE runtime_id=$1 AND archived_at IS NULL ORDER BY id`, row.ID)
		if e != nil {
			writeError(w, 500, "failed to read dependencies")
			return
		}
		for rows.Next() {
			var aid pgtype.UUID
			var name string
			if e = rows.Scan(&aid, &name); e != nil {
				rows.Close()
				writeError(w, 500, "failed to read dependencies")
				return
			}
			agents = append(agents, map[string]string{"id": uuidToString(aid), "name": name, "workspace_id": uuidToString(row.WorkspaceID)})
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			writeError(w, 500, "failed to read dependencies")
			return
		}
	}
	writeJSON(w, 200, map[string]any{"ids": ids, "agents": agents})
}

// Delete a definition across all registration projections, with the same
// conservative dependency rules as upstream. Never cancel another workspace's
// live work merely because its owner clicked delete from this workspace.
func (h *Handler) deletePersonalProfile(w http.ResponseWriter, r *http.Request, alias, owner pgtype.UUID) {
	ctx := r.Context()
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		writeError(w, 500, "failed to begin deletion")
		return
	}
	defer tx.Rollback(ctx)
	var canonical pgtype.UUID
	err = tx.QueryRow(ctx, `SELECT c.id FROM personal_runtime_profile c JOIN runtime_profile p ON p.personal_profile_id=c.id WHERE p.id=$1 AND c.owner_id=$2 FOR UPDATE OF c`, alias, owner).Scan(&canonical)
	if err != nil {
		writeError(w, 404, "runtime profile not found")
		return
	}
	// All authoritative rows precede their projections in the lock order.
	// Otherwise a concurrent machine rename can hold this row while waiting
	// for an alias already held by deletion, creating a cycle.
	lockedCanonical, err := tx.Query(ctx, `SELECT id FROM personal_runtime WHERE personal_profile_id=$1 ORDER BY id FOR UPDATE`, canonical)
	if err != nil {
		writeError(w, 500, "failed to lock personal runtimes")
		return
	}
	for lockedCanonical.Next() {
	}
	err = lockedCanonical.Err()
	lockedCanonical.Close()
	if err != nil {
		writeError(w, 500, "failed to lock personal runtimes")
		return
	}
	rows, err := tx.Query(ctx, `SELECT id FROM runtime_profile WHERE personal_profile_id=$1 ORDER BY id FOR UPDATE`, canonical)
	if err != nil {
		writeError(w, 500, "failed to lock profiles")
		return
	}
	var profiles []pgtype.UUID
	for rows.Next() {
		var id pgtype.UUID
		if rows.Scan(&id) != nil {
			rows.Close()
			writeError(w, 500, "failed to read profiles")
			return
		}
		profiles = append(profiles, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		writeError(w, 500, "failed to read profiles")
		return
	}
	rows, err = tx.Query(ctx, `SELECT id FROM agent_runtime WHERE profile_id=ANY($1::uuid[]) ORDER BY id FOR UPDATE`, profiles)
	if err != nil {
		writeError(w, 500, "failed to lock runtimes")
		return
	}
	var runtimes []pgtype.UUID
	for rows.Next() {
		var id pgtype.UUID
		if rows.Scan(&id) != nil {
			rows.Close()
			writeError(w, 500, "failed to read runtimes")
			return
		}
		runtimes = append(runtimes, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		writeError(w, 500, "failed to read runtimes")
		return
	}
	q := h.Queries.WithTx(tx)
	for _, id := range runtimes {
		if _, err = q.ListUserAgentsByRuntimeForUpdate(ctx, id); err != nil {
			writeError(w, 500, "failed to lock agents")
			return
		}
	}
	var blockers, unfinished int
	err = tx.QueryRow(ctx, `SELECT count(*) FROM agent WHERE runtime_id=ANY($1::uuid[]) AND archived_at IS NULL`, runtimes).Scan(&blockers)
	if err != nil {
		writeError(w, 500, "failed to check agents")
		return
	}
	if blockers > 0 {
		// Preserve upstream's bounded, actionable blocker guidance, but total
		// every workspace and label ambiguous agent names with their scope.
		var sample []db.ListActiveAgentsByProfileRow
		scopesByAgent := map[string][2]string{}
		var scopeNames []string
		seenScopes := map[string]bool{}
		var totals db.ListActiveAgentsByProfileRow
		var name string
		for _, id := range profiles {
			p, e := q.GetRuntimeProfile(ctx, id)
			if e != nil {
				writeError(w, 500, "failed to read profile dependencies")
				return
			}
			name = p.DisplayName
			part, e := q.ListActiveAgentsByProfile(ctx, db.ListActiveAgentsByProfileParams{ProfileID: id, WorkspaceID: p.WorkspaceID, MaxRows: maxReportedBlockingAgents})
			if e != nil {
				writeError(w, 500, "failed to read profile dependencies")
				return
			}
			if len(part) == 0 {
				continue
			}
			totals.TotalCount += part[0].TotalCount
			totals.UserCount += part[0].UserCount
			totals.MikaCount += part[0].MikaCount
			totals.AgentBuilderCount += part[0].AgentBuilderCount
			totals.OtherSystemCount += part[0].OtherSystemCount
			ws, e := q.GetWorkspace(ctx, p.WorkspaceID)
			if e != nil {
				writeError(w, 500, "failed to read dependency workspace")
				return
			}
			if !seenScopes[uuidToString(p.WorkspaceID)] {
				seenScopes[uuidToString(p.WorkspaceID)] = true
				scopeNames = append(scopeNames, ws.Name)
			}
			for _, entry := range part {
				if len(sample) >= maxReportedBlockingAgents {
					break
				}
				scopesByAgent[uuidToString(entry.ID)] = [2]string{uuidToString(p.WorkspaceID), ws.Name}
				sample = append(sample, entry)
			}
		}
		if len(sample) > 0 {
			sample[0].TotalCount = totals.TotalCount
			sample[0].UserCount = totals.UserCount
			sample[0].MikaCount = totals.MikaCount
			sample[0].AgentBuilderCount = totals.AgentBuilderCount
			sample[0].OtherSystemCount = totals.OtherSystemCount
		}
		body := profileDeleteBlockedByAgents(name, sample)
		if entries, ok := body["active_agents"].([]map[string]any); ok {
			for _, entry := range entries {
				if id, ok := entry["id"].(string); ok {
					scope := scopesByAgent[id]
					entry["workspace_id"] = scope[0]
					entry["workspace_name"] = scope[1]
				}
			}
		}
		if message, ok := body["error"].(string); ok && len(scopeNames) > 0 {
			body["error"] = message + " Affected workspaces: " + strings.Join(scopeNames, ", ") + "."
		}
		writeJSON(w, 409, body)
		return
	}
	err = tx.QueryRow(ctx, `SELECT count(*) FROM agent_task_queue WHERE runtime_id=ANY($1::uuid[]) AND completed_at IS NULL`, runtimes).Scan(&unfinished)
	if err != nil {
		writeError(w, 500, "failed to check tasks")
		return
	}
	if unfinished > 0 {
		writePersonalRuntimeTeardownError(w, service.ErrRuntimeNotDrained)
		return
	}
	var changes []service.RuntimeTeardownResult
	var scopes []string
	for _, id := range runtimes {
		rt, e := q.GetAgentRuntime(ctx, id)
		if e != nil {
			writeError(w, 500, "runtime changed")
			return
		}
		result, e := service.TeardownRuntime(ctx, q, id, service.RuntimeTeardownOptions{})
		if e != nil {
			writePersonalRuntimeTeardownError(w, e)
			return
		}
		if e = q.DeleteAgentRuntime(ctx, id); e != nil {
			writeError(w, 500, "failed to delete runtime")
			return
		}
		changes = append(changes, result)
		scopes = append(scopes, uuidToString(rt.WorkspaceID))
	}
	if _, err = tx.Exec(ctx, `DELETE FROM runtime_profile WHERE id=ANY($1::uuid[])`, profiles); err != nil {
		writeError(w, 500, "failed to delete aliases")
		return
	}
	if _, err = tx.Exec(ctx, `DELETE FROM personal_runtime WHERE personal_profile_id=$1`, canonical); err != nil {
		writeError(w, 500, "failed to delete personal runtimes")
		return
	}
	if _, err = tx.Exec(ctx, `DELETE FROM personal_runtime_profile WHERE id=$1`, canonical); err != nil {
		writeError(w, 500, "failed to delete personal profile")
		return
	}
	if err = tx.Commit(ctx); err != nil {
		writeError(w, 500, "failed to commit deletion")
		return
	}
	for i, id := range runtimes {
		h.NotifyRuntimeGone(uuidToString(id))
		h.publishRuntimeTeardown(ctx, changes[i], scopes[i], uuidToString(owner))
	}
	h.publishPersonalRuntimeRefresh(ctx, uuidToString(owner))
	w.WriteHeader(http.StatusNoContent)
}

// Workspace rows are registration adapters for old daemons, not independently
// editable configurations. Only the authenticated owner's registry is projected.
func (h *Handler) ensurePersonalRuntimes(ctx context.Context, workspace pgtype.UUID, user string) error {
	return retryPersonalRuntimeTx(ctx, func() error {
		_, err := h.DB.Exec(ctx, `SELECT team_mesh_ensure_runtime_workspace($1,$2)`, parseUUID(user), workspace)
		return err
	})
}

func (h *Handler) publishPersonalRuntimeRefresh(ctx context.Context, owner string) {
	rows, err := h.DB.Query(ctx, `SELECT workspace_id FROM member WHERE user_id=$1`, parseUUID(owner))
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id pgtype.UUID
		if rows.Scan(&id) == nil {
			ids = append(ids, uuidToString(id))
		}
	}
	rows.Close()
	for _, id := range ids {
		h.requestDaemonRuntimeProfileRefresh(id, "")
		// No names, commands, paths or credentials in workspace-wide events.
		h.publish(protocol.EventDaemonRegister, id, "member", owner, map[string]any{"action": "refresh"})
	}
}

func (h *Handler) updatePersonalRuntimeProfile(ctx context.Context, p db.UpdateRuntimeProfileParams, owner pgtype.UUID) (db.RuntimeProfile, error) {
	var row db.RuntimeProfile
	err := retryPersonalRuntimeTx(ctx, func() error { var err error; row, err = h.updatePersonalRuntimeProfileOnce(ctx, p, owner); return err })
	return row, err
}

func (h *Handler) updatePersonalRuntimeProfileOnce(ctx context.Context, p db.UpdateRuntimeProfileParams, owner pgtype.UUID) (db.RuntimeProfile, error) {
	var result db.RuntimeProfile
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	var canonical pgtype.UUID
	err = tx.QueryRow(ctx, `SELECT c.id FROM personal_runtime_profile c JOIN runtime_profile p ON p.personal_profile_id=c.id WHERE p.id=$1 AND c.owner_id=$2 FOR UPDATE OF c`, p.ID, owner).Scan(&canonical)
	if err != nil {
		return result, err
	}
	_, err = tx.Exec(ctx, `UPDATE personal_runtime_profile SET display_name=coalesce($2,display_name),command_name=coalesce($3,command_name),description=coalesce($4,description),fixed_args=coalesce($5::jsonb,fixed_args),enabled=coalesce($6,enabled),revision=revision+1,updated_at=now() WHERE id=$1`, canonical, p.DisplayName, p.CommandName, p.Description, p.FixedArgs, p.Enabled)
	if err != nil {
		return result, err
	}
	_, err = tx.Exec(ctx, `UPDATE runtime_profile p SET display_name=c.display_name,command_name=c.command_name,description=c.description,fixed_args=c.fixed_args,enabled=c.enabled,updated_at=c.updated_at FROM personal_runtime_profile c WHERE c.id=$1 AND p.personal_profile_id=c.id`, canonical)
	if err != nil {
		return result, err
	}
	result, err = h.Queries.WithTx(tx).GetRuntimeProfile(ctx, p.ID)
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}

func (h *Handler) renamePersonalRuntime(ctx context.Context, rt db.AgentRuntime, customName pgtype.Text, machine bool) (db.AgentRuntime, error) {
	var row db.AgentRuntime
	err := retryPersonalRuntimeTx(ctx, func() error {
		var err error
		row, err = h.renamePersonalRuntimeOnce(ctx, rt, customName, machine)
		return err
	})
	return row, err
}

func (h *Handler) renamePersonalRuntimeOnce(ctx context.Context, rt db.AgentRuntime, customName pgtype.Text, machine bool) (db.AgentRuntime, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return rt, err
	}
	defer tx.Rollback(ctx)
	// Lock the authoritative rows first in stable order; no alias lock inversion.
	rows, err := tx.Query(ctx, `SELECT c.id FROM personal_runtime c WHERE c.owner_id=$1 AND (( $3::boolean AND c.daemon_id=$2 ) OR c.id=(SELECT personal_runtime_id FROM agent_runtime WHERE id=$4)) ORDER BY c.id FOR UPDATE`, rt.OwnerID, rt.DaemonID, machine, rt.ID)
	if err != nil {
		return rt, err
	}
	var ids []pgtype.UUID
	for rows.Next() {
		var id pgtype.UUID
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return rt, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return rt, err
	}
	if len(ids) == 0 {
		// Legacy/cloud registrations without a machine identity have no
		// personal projection. Preserve owner-only rename rather than
		// returning a misleading success for an UPDATE affecting zero rows.
		q := h.Queries.WithTx(tx)
		locked, e := q.LockAgentRuntime(ctx, rt.ID)
		if e != nil {
			return rt, e
		}
		if !locked.OwnerID.Valid || locked.OwnerID != rt.OwnerID {
			return rt, errors.New("runtime owner changed")
		}
		updated, e := q.UpdateAgentRuntimeCustomName(ctx, db.UpdateAgentRuntimeCustomNameParams{ID: rt.ID, CustomName: customName})
		if e != nil {
			return rt, e
		}
		return updated, tx.Commit(ctx)
	}
	_, err = tx.Exec(ctx, `UPDATE personal_runtime SET custom_name=$2,updated_at=now() WHERE id=ANY($1::uuid[])`, ids, customName)
	if err != nil {
		return rt, err
	}
	_, err = tx.Exec(ctx, `UPDATE agent_runtime r SET custom_name=c.custom_name,updated_at=now() FROM personal_runtime c WHERE c.id=ANY($1::uuid[]) AND r.personal_runtime_id=c.id`, ids)
	if err != nil {
		return rt, err
	}
	rt, err = h.Queries.WithTx(tx).GetAgentRuntime(ctx, rt.ID)
	if err != nil {
		return rt, err
	}
	return rt, tx.Commit(ctx)
}
