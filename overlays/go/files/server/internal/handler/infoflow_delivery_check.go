package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
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
	allowed, err := h.infoFlowDeliveryAllowed(r.Context(), req.infoFlowScope, id)
	if err != nil {
		infoFlowError(w, 500, "delivery check")
		return
	}
	writeJSON(w, 200, map[string]bool{"allowed": allowed})
}

func (h *Handler) infoFlowDeliveryAllowed(ctx context.Context, req infoFlowScope, id pgtype.UUID) (bool, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var conversation infoFlowConversation
	var bindingID string
	err = tx.QueryRow(ctx, `SELECT b.conversation_kind,b.conversation_id,b.id::text
		FROM infoflow_issue_delivery d JOIN infoflow_issue_binding b ON b.id=d.binding_id
		JOIN comment c ON c.id=d.comment_id JOIN issue i ON i.id=b.issue_id
		WHERE d.id=$1 AND b.workspace_id=$3 AND d.generation=b.generation AND d.acked_at IS NULL AND d.cancelled_at IS NULL AND b.sync_enabled
		AND c.issue_id=b.issue_id AND c.workspace_id=b.workspace_id AND c.deleted_at IS NULL
		AND c.created_at>=GREATEST(b.bound_at,b.broadcast_started_at)
		AND i.infoflow_assignment_version=b.assignment_version
		AND ((b.conversation_kind='private' AND b.robot_id=$2 AND b.agent_id=$4)
		OR (b.conversation_kind='group' AND b.leader_robot_id=$2))`,
		id, req.RobotID, req.WorkspaceID, req.AgentID).Scan(&conversation.Kind, &conversation.ID, &bindingID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err = infoFlowLock(ctx, tx, req, conversation); err != nil {
		return false, err
	}
	binding, err := infoFlowLoadBindingID(ctx, tx, req, conversation, bindingID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	scoped, _ := h.infoFlowScoped(tx)
	allowed := false
	authors := []string{strings.ToLower(req.AgentID)}
	if conversation.Kind == "group" {
		view := scoped.infoFlowGroupView(ctx, binding, req.WorkspaceID)
		allowed = binding.IssueID.Valid && view.State == "active" && view.LeaderRobotID == req.RobotID && view.LeaderAgentID == strings.ToLower(req.AgentID)
		authors = view.AgentIDs
	} else {
		view := scoped.infoFlowPrivateView(ctx, binding, req.WorkspaceID, req.AgentID)
		allowed = binding.IssueID.Valid && view.State == "active"
		if allowed {
			_, _, err = scoped.infoFlowIdentity(ctx, conversation.ID, req.WorkspaceID)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return false, err
			}
			allowed = err == nil
		}
	}
	// Recheck the generation after the shared lock; an unbind may have won
	// between the initial delivery lookup and acquiring it.
	if allowed {
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM infoflow_issue_delivery d JOIN infoflow_issue_binding b ON b.id=d.binding_id
			WHERE d.id=$1 AND d.generation=b.generation AND b.issue_id=$2 AND b.sync_enabled
			AND d.acked_at IS NULL AND d.cancelled_at IS NULL
			AND EXISTS(SELECT 1 FROM comment c WHERE c.id=d.comment_id AND c.created_at>=GREATEST(b.bound_at,b.broadcast_started_at)
			AND (c.author_type<>'agent' OR c.author_id=ANY($3::uuid[]))))`, id, binding.IssueID, authors).Scan(&allowed)
		if err != nil {
			return false, err
		}
	}
	return allowed, nil
}
