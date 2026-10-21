package handler

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// Sync controls outbound task content only. It does not revoke the binding,
// inbound instructions, operation receipts, or the other conversation's output.
func (h *Handler) UpdateIssueInfoFlowSync(w http.ResponseWriter, r *http.Request) {
	if isMachineCredentialActor(r) {
		writeError(w, 403, "Only human actors can manage conversations")
		return
	}
	var req struct {
		Enabled    *bool  `json:"sync_enabled"`
		Generation string `json:"expected_generation"`
	}
	if !infoFlowDecode(w, r, &req) {
		return
	}
	if req.Enabled == nil {
		writeError(w, 400, "sync_enabled is required")
		return
	}
	generation, ok := parseUUIDOrBadRequest(w, req.Generation, "expected_generation")
	if !ok {
		return
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "bindingId"), "binding_id")
	if !ok {
		return
	}
	i, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	ws := uuidToString(i.WorkspaceID)
	m, ok := h.requireWorkspaceMember(w, r, ws, "issue not found")
	if !ok {
		return
	}
	if !infoFlowWebEnabled() {
		writeError(w, 409, "InfoFlow conversations are unavailable")
		return
	}
	var robot, agent, kind, conversation string
	err := h.DB.QueryRow(r.Context(), `SELECT robot_id,agent_id::text,conversation_kind,conversation_id
		FROM infoflow_issue_binding WHERE id=$1 AND issue_id=$2 AND workspace_id=$3`, id, i.ID, ws).Scan(&robot, &agent, &kind, &conversation)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "binding not found")
		return
	}
	if err != nil {
		writeError(w, 500, "Failed to load conversation")
		return
	}
	scope := infoFlowScope{RobotID: robot, AgentID: agent, WorkspaceID: ws}
	c := infoFlowConversation{Kind: kind, ID: conversation}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "Failed to update sync")
		return
	}
	defer tx.Rollback(r.Context())
	if err = infoFlowLock(r.Context(), tx, scope, c); err != nil {
		writeError(w, 500, "Failed to update sync")
		return
	}
	if kind == "private" {
		if err = infoFlowPrivateWebLock(r.Context(), tx, i.ID, m.UserID); err != nil {
			writeError(w, 500, "Failed to update sync")
			return
		}
	}
	b, err := infoFlowLoadBindingID(r.Context(), tx, scope, c, uuidToString(id))
	if err != nil || b.IssueID != i.ID || b.Generation != generation {
		writeError(w, 409, "Binding changed")
		return
	}
	sh, _ := h.infoFlowScoped(tx)
	sender, err := sh.infoFlowWebSender(r.Context(), ws, m.UserID)
	if err != nil {
		writeError(w, 404, "issue not found")
		return
	}
	if kind == "private" {
		if b.BinderID != m.UserID || sender == "" || conversation != sender {
			writeError(w, 403, "Cannot manage another user's private conversation")
			return
		}
	} else {
		_, _, manage, e := sh.infoFlowWebAuthority(r.Context(), ws, i.ID, m.UserID, b)
		if e != nil || !manage {
			writeError(w, 403, "Insufficient binding permissions")
			return
		}
	}
	if b.SyncEnabled != *req.Enabled {
		// Reuse the broadcast cutoff to make resumption future-only. Keep cancelled
		// delivery rows as tombstones so stale Bridge snapshots cannot revive them.
		_, err = tx.Exec(r.Context(), `UPDATE infoflow_issue_binding SET sync_enabled=$2,
			broadcast_started_at=CASE WHEN $2 THEN clock_timestamp() ELSE broadcast_started_at END
			WHERE id=$1`, id, *req.Enabled)
		if err == nil && !*req.Enabled {
			_, err = tx.Exec(r.Context(), `UPDATE infoflow_issue_delivery SET cancelled_at=clock_timestamp()
				WHERE binding_id=$1 AND acked_at IS NULL AND cancelled_at IS NULL`, id)
		}
		if err != nil {
			writeError(w, 500, "Failed to update sync")
			return
		}
	}
	out, err := h.infoFlowConversationView(r.Context(), tx, ws, i.ID, m.UserID)
	if err != nil {
		infoFlowWriteActionError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "Failed to update sync")
		return
	}
	writeJSON(w, 200, out)
}
