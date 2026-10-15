package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

type infoFlowConversationNotice struct {
	ID                         string               `json:"id"`
	Kind                       string               `json:"kind"`
	IssueID                    string               `json:"issue_id"`
	Identifier                 string               `json:"identifier"`
	WorkspaceSlug              string               `json:"workspace_slug"`
	RobotID                    string               `json:"robot_id"`
	AgentID                    string               `json:"agent_id"`
	Conversation               infoFlowConversation `json:"conversation"`
	binding, generation, actor pgtype.UUID
}

func infoFlowEnqueueNotice(ctx context.Context, tx pgx.Tx, s infoFlowScope, c infoFlowConversation, b infoFlowBinding, kind string) error {
	if !b.ID.Valid || !b.IssueID.Valid || !b.BinderID.Valid {
		return nil
	}
	if c.Kind == "group" {
		var topology infoFlowGroupTopology
		if json.Unmarshal(b.GroupTopology, &topology) != nil || topology.LeaderAgentID == "" || b.LeaderRobotID == "" {
			return nil
		}
		s.RobotID, s.AgentID = b.LeaderRobotID, topology.LeaderAgentID
	}
	var identifier, slug string
	err := tx.QueryRow(ctx, `SELECT w.issue_prefix||'-'||i.number::text,w.slug FROM issue i JOIN workspace w ON w.id=i.workspace_id WHERE i.id=$1 AND i.workspace_id=$2`, b.IssueID, s.WorkspaceID).Scan(&identifier, &slug)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO infoflow_conversation_notice(id,binding_id,generation,kind,workspace_id,issue_id,actor_id,robot_id,agent_id,conversation_kind,conversation_id,identifier,workspace_slug) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT(binding_id,generation,kind) DO NOTHING`, dbid.NewV7(), b.ID, b.Generation, kind, s.WorkspaceID, b.IssueID, b.BinderID, s.RobotID, s.AgentID, c.Kind, c.ID, identifier, slug)
	return err
}

// Notices contain no model output or comment bodies. Bound notices require a
// still-current binding; unbound notices retain only the authorized destination
// snapshot. Fresh identity checks prevent deleted/transferred/revoked exposure.
func (h *Handler) infoFlowNoticeAllowed(ctx context.Context, tx pgx.Tx, s infoFlowScope, n infoFlowConversationNotice) (bool, error) {
	var valid bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issue i JOIN member m ON m.workspace_id=i.workspace_id AND m.user_id=$3 JOIN agent a ON a.id=$4 AND a.workspace_id=i.workspace_id AND a.archived_at IS NULL WHERE i.id=$1 AND i.workspace_id=$2)`, n.IssueID, s.WorkspaceID, n.actor, s.AgentID).Scan(&valid)
	if err != nil || !valid {
		return false, err
	}
	sh, _ := h.infoFlowScoped(tx)
	if n.Conversation.Kind == "private" {
		user, _, e := sh.infoFlowIdentity(ctx, n.Conversation.ID, s.WorkspaceID)
		if errors.Is(e, pgx.ErrNoRows) {
			return false, nil
		}
		if e != nil {
			return false, e
		}
		if user.ID != n.actor {
			return false, nil
		}
	}
	if n.Kind == "unbound" {
		var rebound bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM infoflow_issue_binding WHERE workspace_id=$1 AND issue_id=$2 AND conversation_kind=$3 AND conversation_id=$4 AND ($3='group' OR robot_id=$5))`, s.WorkspaceID, n.IssueID, n.Conversation.Kind, n.Conversation.ID, s.RobotID).Scan(&rebound)
		return !rebound, err
	}
	b, err := infoFlowLoadBindingID(ctx, tx, s, n.Conversation, uuidToString(n.binding))
	if errors.Is(err, pgx.ErrNoRows) {
		return n.Kind == "unbound", nil
	}
	if err != nil {
		return false, err
	}
	same := b.IssueID.Valid && uuidToString(b.IssueID) == n.IssueID && b.Generation == n.generation
	if n.Kind == "unbound" {
		return !same, nil
	}
	if !same {
		return false, nil
	}
	if n.Conversation.Kind == "private" {
		return sh.infoFlowPrivateView(ctx, b, s.WorkspaceID, s.AgentID).State == "active", nil
	}
	g := sh.infoFlowGroupView(ctx, b, s.WorkspaceID)
	return g.State == "active" && g.LeaderRobotID == s.RobotID && g.LeaderAgentID == strings.ToLower(s.AgentID), nil
}

func (h *Handler) InfoFlowConversationNotices(w http.ResponseWriter, r *http.Request) {
	if !infoFlowAuthenticate(w, r) {
		return
	}
	var req infoFlowDeliveryRequest
	if !infoFlowDecode(w, r, &req) {
		return
	}
	if !req.infoFlowScope.valid() || req.Limit < 0 || req.Limit > 50 {
		infoFlowError(w, 400, "scope")
		return
	}
	limit := req.Limit
	if limit == 0 {
		limit = 20
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		infoFlowError(w, 500, "begin")
		return
	}
	defer tx.Rollback(r.Context())
	rows, err := tx.Query(r.Context(), `SELECT id::text,kind,issue_id::text,identifier,workspace_slug,robot_id,agent_id::text,conversation_kind,conversation_id,binding_id,generation,actor_id FROM infoflow_conversation_notice WHERE workspace_id=$1 AND robot_id=$2 AND agent_id=$3 AND acked_at IS NULL AND cancelled_at IS NULL AND created_at>clock_timestamp()-interval '1 day' AND next_at<=clock_timestamp() ORDER BY next_at,created_at,id LIMIT $4`, req.WorkspaceID, req.RobotID, req.AgentID, limit)
	if err != nil {
		infoFlowError(w, 500, "notices")
		return
	}
	var candidates []infoFlowConversationNotice
	for rows.Next() {
		var n infoFlowConversationNotice
		if err = rows.Scan(&n.ID, &n.Kind, &n.IssueID, &n.Identifier, &n.WorkspaceSlug, &n.RobotID, &n.AgentID, &n.Conversation.Kind, &n.Conversation.ID, &n.binding, &n.generation, &n.actor); err != nil {
			break
		}
		candidates = append(candidates, n)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		infoFlowError(w, 500, "notices")
		return
	}
	out := []infoFlowConversationNotice{}
	for _, n := range candidates {
		// Rotate every attempted destination, including temporary admission
		// failures and SDK errors. Later conversations cannot be starved.
		if _, err = tx.Exec(r.Context(), `UPDATE infoflow_conversation_notice SET next_at=clock_timestamp()+interval '5 seconds' WHERE id=$1`, n.ID); err != nil {
			infoFlowError(w, 500, "notice retry")
			return
		}
		allowed, e := h.infoFlowNoticeAllowed(r.Context(), tx, req.infoFlowScope, n)
		if e != nil {
			infoFlowError(w, 500, "notice authority")
			return
		}
		if allowed {
			out = append(out, n)
		} else {
			// Expired identity or a superseded binding must not starve later
			// notices. A temporarily missing group lease remains retryable.
			_, e = tx.Exec(r.Context(), `UPDATE infoflow_conversation_notice SET cancelled_at=clock_timestamp() WHERE id=$1 AND (
 NOT EXISTS(SELECT 1 FROM issue i JOIN member m ON m.workspace_id=i.workspace_id AND m.user_id=$4 JOIN agent a ON a.id=$5 AND a.workspace_id=i.workspace_id AND a.archived_at IS NULL WHERE i.id=$2 AND i.workspace_id=$3)
 OR (kind='bound' AND NOT EXISTS(SELECT 1 FROM infoflow_issue_binding b JOIN issue i ON i.id=b.issue_id AND i.workspace_id=b.workspace_id WHERE b.id=binding_id AND b.generation=$6 AND b.issue_id=$2 AND b.workspace_id=$3 AND b.assignment_version=i.infoflow_assignment_version))
 OR (kind='unbound' AND EXISTS(SELECT 1 FROM infoflow_issue_binding b WHERE b.workspace_id=$3 AND b.issue_id=$2 AND b.conversation_kind=infoflow_conversation_notice.conversation_kind AND b.conversation_id=infoflow_conversation_notice.conversation_id AND (b.conversation_kind='group' OR b.robot_id=infoflow_conversation_notice.robot_id))))`, n.ID, n.IssueID, req.WorkspaceID, n.actor, req.AgentID, n.generation)
			if e != nil {
				infoFlowError(w, 500, "notice cancellation")
				return
			}
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		infoFlowError(w, 500, "commit")
		return
	}
	writeJSON(w, 200, map[string]any{"notices": out})
}

// The Bridge rechecks immediately before an unsent notice's SDK operation.
// Already-sent checkpoints retry only ACK, never the external message.
func (h *Handler) InfoFlowConversationNoticeCheck(w http.ResponseWriter, r *http.Request) {
	if !infoFlowAuthenticate(w, r) {
		return
	}
	var scope infoFlowScope
	if !infoFlowDecode(w, r, &scope) {
		return
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	if !scope.valid() {
		infoFlowError(w, 400, "scope")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		infoFlowError(w, 500, "begin")
		return
	}
	defer tx.Rollback(r.Context())
	var n infoFlowConversationNotice
	err = tx.QueryRow(r.Context(), `SELECT id::text,kind,issue_id::text,identifier,workspace_slug,robot_id,agent_id::text,conversation_kind,conversation_id,binding_id,generation,actor_id FROM infoflow_conversation_notice WHERE id=$1 AND workspace_id=$2 AND robot_id=$3 AND agent_id=$4 AND acked_at IS NULL AND cancelled_at IS NULL AND created_at>clock_timestamp()-interval '1 day'`, id, scope.WorkspaceID, scope.RobotID, scope.AgentID).Scan(&n.ID, &n.Kind, &n.IssueID, &n.Identifier, &n.WorkspaceSlug, &n.RobotID, &n.AgentID, &n.Conversation.Kind, &n.Conversation.ID, &n.binding, &n.generation, &n.actor)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, 200, map[string]bool{"allowed": false})
		return
	}
	if err != nil {
		infoFlowError(w, 500, "notice")
		return
	}
	allowed, err := h.infoFlowNoticeAllowed(r.Context(), tx, scope, n)
	if err != nil {
		infoFlowError(w, 500, "authority")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		infoFlowError(w, 500, "commit")
		return
	}
	writeJSON(w, 200, map[string]bool{"allowed": allowed})
}

func (h *Handler) InfoFlowConversationNoticeAck(w http.ResponseWriter, r *http.Request) {
	if !infoFlowAuthenticate(w, r) {
		return
	}
	var req infoFlowDeliveryRequest
	if !infoFlowDecode(w, r, &req) {
		return
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	if !req.infoFlowScope.valid() || len(req.MessageIDs) > 100 {
		infoFlowError(w, 400, "ack")
		return
	}
	if req.MessageID != "" {
		req.MessageIDs = append(req.MessageIDs, req.MessageID)
	}
	for _, v := range req.MessageIDs {
		if v == "" || len(v) > 256 || strings.ContainsRune(v, 0) {
			infoFlowError(w, 400, "message id")
			return
		}
	}
	if req.MessageIDs == nil {
		req.MessageIDs = []string{}
	}
	result, err := h.DB.Exec(r.Context(), `UPDATE infoflow_conversation_notice SET acked_at=COALESCE(acked_at,clock_timestamp()),external_message_ids=ARRAY(SELECT DISTINCT unnest(external_message_ids||$5::text[])) WHERE id=$1 AND workspace_id=$2 AND robot_id=$3 AND agent_id=$4 AND cancelled_at IS NULL`, id, req.WorkspaceID, req.RobotID, req.AgentID, req.MessageIDs)
	if err != nil {
		infoFlowError(w, 500, "ack")
		return
	}
	if result.RowsAffected() != 1 {
		infoFlowError(w, 404, "notice")
		return
	}
	writeJSON(w, 200, infoFlowRelayResponse{Handled: true, Silent: true})
}
