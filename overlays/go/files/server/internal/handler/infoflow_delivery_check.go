package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// A poll result is not authority to send after an unbind or reassignment.
func (h *Handler) InfoFlowIssueDeliveryCheck(w http.ResponseWriter, r *http.Request) {
	if !infoFlowAuthenticate(w, r) {
		return
	}
	var req infoFlowDeliveryRequest
	if !infoFlowDecode(w, r, &req) {
		return
	}
	id, ok := parseUUIDOrBadRequest(&infoFlowCapture{}, chi.URLParam(r, "id"), "id")
	if !ok || !req.infoFlowScope.valid() {
		infoFlowError(w, 400, "invalid delivery check")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		infoFlowError(w, 500, "begin")
		return
	}
	defer tx.Rollback(r.Context())
	var conversation infoFlowConversation
	var bindingID string
	err = tx.QueryRow(r.Context(), `SELECT b.conversation_kind,b.conversation_id,b.id::text
		FROM infoflow_issue_delivery d JOIN infoflow_issue_binding b ON b.id=d.binding_id
		JOIN comment c ON c.id=d.comment_id JOIN issue i ON i.id=b.issue_id
		WHERE d.id=$1 AND b.workspace_id=$3 AND d.generation=b.generation AND d.acked_at IS NULL
		AND c.issue_id=b.issue_id AND c.workspace_id=b.workspace_id AND c.deleted_at IS NULL
		AND c.created_at>=GREATEST(b.bound_at,b.broadcast_started_at)
		AND i.infoflow_assignment_version=b.assignment_version
		AND ((b.conversation_kind='private' AND b.robot_id=$2 AND b.agent_id=$4)
		OR (b.conversation_kind='group' AND b.leader_robot_id=$2))`,
		id, req.RobotID, req.WorkspaceID, req.AgentID).Scan(&conversation.Kind, &conversation.ID, &bindingID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, 200, map[string]bool{"allowed": false})
		return
	}
	if err != nil {
		infoFlowError(w, 500, "delivery")
		return
	}
	if err = infoFlowLock(r.Context(), tx, req.infoFlowScope, conversation); err != nil {
		infoFlowError(w, 500, "lock")
		return
	}
	binding, err := infoFlowLoadBindingID(r.Context(), tx, req.infoFlowScope, conversation, bindingID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, 200, map[string]bool{"allowed": false})
		return
	}
	if err != nil {
		infoFlowError(w, 500, "binding")
		return
	}
	scoped, _ := h.infoFlowScoped(tx)
	allowed := false
	if conversation.Kind == "group" {
		view := scoped.infoFlowGroupView(r.Context(), binding, req.WorkspaceID)
		allowed = binding.IssueID.Valid && view.State == "active" && view.LeaderRobotID == req.RobotID && view.LeaderAgentID == strings.ToLower(req.AgentID)
	} else {
		view := scoped.infoFlowPrivateView(r.Context(), binding, req.WorkspaceID, req.AgentID)
		allowed = binding.IssueID.Valid && view.State == "active"
		if allowed {
			_, _, err = scoped.infoFlowIdentity(r.Context(), conversation.ID, req.WorkspaceID)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				infoFlowError(w, 500, "identity")
				return
			}
			allowed = err == nil
		}
	}
	// Recheck the generation after the shared lock; an unbind may have won
	// between the initial delivery lookup and acquiring it.
	if allowed {
		err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM infoflow_issue_delivery d JOIN infoflow_issue_binding b ON b.id=d.binding_id
			WHERE d.id=$1 AND d.generation=b.generation AND b.issue_id=$2 AND d.acked_at IS NULL)`, id, binding.IssueID).Scan(&allowed)
		if err != nil {
			infoFlowError(w, 500, "generation")
			return
		}
	}
	writeJSON(w, 200, map[string]bool{"allowed": allowed})
}
