package handler

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Only public task navigation metadata crosses this read-only boundary.
type infoFlowTaskChoice struct {
	IssueID       string   `json:"issue_id"`
	Identifier    string   `json:"identifier"`
	WorkspaceSlug string   `json:"workspace_slug"`
	Status        string   `json:"status"`
	BindingKinds  string   `json:"binding_kinds,omitempty"`
	Title         string   `json:"title,omitempty"`
	AgentNames    []string `json:"agent_names"`
}

// Resolve the current assignment, never the robot that happened to bind it.
// The outer agent scan deduplicates a leader also recorded as a squad member.
const infoFlowTaskAgentNamesSQL = `ARRAY(SELECT a.name FROM agent a
 WHERE a.workspace_id=i.workspace_id AND a.archived_at IS NULL AND
 ((i.assignee_type='agent' AND a.id=i.assignee_id) OR
 (i.assignee_type='squad' AND EXISTS(SELECT 1 FROM squad s
 WHERE s.id=i.assignee_id AND s.workspace_id=i.workspace_id AND s.archived_at IS NULL
 AND (s.leader_id=a.id OR EXISTS(SELECT 1 FROM squad_member sm
 WHERE sm.squad_id=s.id AND sm.member_type='agent' AND sm.member_id=a.id)))))
 ORDER BY CASE WHEN a.id=i.assignee_id OR a.id=(SELECT s.leader_id FROM squad s
 WHERE s.id=i.assignee_id AND s.workspace_id=i.workspace_id) THEN 0 ELSE 1 END,a.name,a.id)`

func (h *Handler) infoFlowTaskChoices(ctx context.Context, tx pgx.Tx, ws string, bindings []infoFlowBinding) ([]infoFlowTaskChoice, error) {
	choices := make([]infoFlowTaskChoice, 0, len(bindings))
	for _, b := range bindings {
		var choice infoFlowTaskChoice
		err := tx.QueryRow(ctx, `SELECT i.id::text,w.issue_prefix || '-' || i.number::text,w.slug,i.status,i.title,`+infoFlowTaskAgentNamesSQL+` FROM issue i JOIN workspace w ON w.id=i.workspace_id WHERE i.id=$1 AND i.workspace_id=$2`, b.IssueID, ws).Scan(&choice.IssueID, &choice.Identifier, &choice.WorkspaceSlug, &choice.Status, &choice.Title, &choice.AgentNames)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		choices = append(choices, choice)
	}
	slices.SortFunc(choices, func(a, b infoFlowTaskChoice) int { return strings.Compare(a.Identifier, b.Identifier) })
	return choices, nil
}

// Conversation commands never select an issue, reconcile membership, or invoke
// an agent. A status overview still requires verified workspace membership.
func (h *Handler) infoFlowConversationCommand(w http.ResponseWriter, r *http.Request, tx pgx.Tx, req infoFlowRelayRequest) {
	if req.StatusPage < 0 || req.StatusPage > 100000 {
		infoFlowError(w, 400, "status page")
		return
	}
	agent, err := h.Queries.GetAgent(r.Context(), parseUUID(req.AgentID))
	if err != nil || uuidToString(agent.WorkspaceID) != strings.ToLower(req.WorkspaceID) || agent.ArchivedAt.Valid {
		infoFlowError(w, 409, "agent unavailable")
		return
	}
	out := infoFlowRelayResponse{Handled: true, ConversationMode: "chat"}
	user, _, err := h.infoFlowIdentity(r.Context(), req.Sender, req.WorkspaceID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			infoFlowError(w, 500, "identity")
			return
		}
		out.Notice = "没有工作区权限，无法评论，请向[" + h.infoFlowOwner(r.Context(), req.WorkspaceID, nil) + "]申请工作区权限"
	} else if req.Command == "help" {
		out.NoticeKind, out.Notice = "help", "基本操作命令"
	} else {
		out.StatusPage = max(1, req.StatusPage)
		out.NoticeKind, out.Notice = "status", "当前群未绑定任务。"
		if req.Conversation.Kind == "private" {
			out.Notice = "当前智能体暂无参与的任务。"
		}
		out.TaskChoices, err = h.infoFlowStatusChoices(r.Context(), tx, req, uuidToString(user.ID))
		if err != nil {
			infoFlowError(w, 500, "task choices")
			return
		}
		if len(out.TaskChoices) > 0 {
			out.ConversationMode, out.Notice = "issue", "当前群绑定的任务："
			if req.Conversation.Kind == "private" {
				out.Notice = "当前智能体参与的任务（含群聊、私聊及未绑定会话的任务）："
			}
		}
	}
	writeJSON(w, 200, out)
}

func (h *Handler) infoFlowStatusChoices(ctx context.Context, tx pgx.Tx, req infoFlowRelayRequest, userID string) ([]infoFlowTaskChoice, error) {
	// Group status is conversation-wide, not limited to the addressed agent.
	// Private status is agent-wide, including squad participation and issues
	// with no IM binding. Neither path exposes another user's private chat ID.
	rows, err := tx.Query(ctx, `SELECT i.id::text,w.issue_prefix || '-' || i.number::text,w.slug,i.status,i.title,`+infoFlowTaskAgentNamesSQL+`,
	 CASE WHEN $2='group' THEN 'group' ELSE COALESCE((SELECT string_agg(DISTINCT b.conversation_kind, ',' ORDER BY b.conversation_kind)
	 FROM infoflow_issue_binding b WHERE b.issue_id=i.id AND b.workspace_id=i.workspace_id),'') END
	 FROM issue i JOIN workspace w ON w.id=i.workspace_id
	 WHERE EXISTS(SELECT 1 FROM member m WHERE m.workspace_id=i.workspace_id AND m.user_id=$1)
	 AND (($2='group' AND EXISTS(SELECT 1 FROM infoflow_issue_binding b WHERE b.issue_id=i.id AND b.workspace_id=i.workspace_id AND b.conversation_kind='group' AND b.conversation_id=$3))
	 OR ($2='private' AND i.workspace_id=$4 AND
	 ((i.assignee_type='agent' AND i.assignee_id::text=lower($5)) OR
	 (i.assignee_type='squad' AND EXISTS(SELECT 1 FROM squad s WHERE s.id=i.assignee_id AND s.workspace_id=i.workspace_id AND s.archived_at IS NULL AND
	 (s.leader_id::text=lower($5) OR EXISTS(SELECT 1 FROM squad_member sm WHERE sm.squad_id=s.id AND sm.member_type='agent' AND sm.member_id::text=lower($5))))))))
	 ORDER BY w.slug,i.number,i.id`, userID, req.Conversation.Kind, req.Conversation.ID, req.WorkspaceID, req.AgentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	choices := []infoFlowTaskChoice{}
	for rows.Next() {
		var choice infoFlowTaskChoice
		if err := rows.Scan(&choice.IssueID, &choice.Identifier, &choice.WorkspaceSlug, &choice.Status, &choice.Title, &choice.AgentNames, &choice.BindingKinds); err != nil {
			return nil, err
		}
		choices = append(choices, choice)
	}
	return choices, rows.Err()
}
