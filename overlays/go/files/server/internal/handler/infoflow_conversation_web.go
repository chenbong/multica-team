package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

type infoFlowOwnPrivate struct {
	BindingID   string `json:"binding_id"`
	RobotID     string `json:"robot_id"`
	RobotName   string `json:"robot_name"`
	RobotChatID string `json:"robot_chat_id"`
	State       string `json:"state"`
	Reason      string `json:"reason"`
}
type infoFlowPrivateWebRequestView struct {
	RequestID  string `json:"request_id"`
	State      string `json:"state"`
	Reason     string `json:"reason"`
	Generation string `json:"generation,omitempty"`
	CanCancel  bool   `json:"can_cancel,omitempty"`
}
type infoFlowPrivateWebView struct {
	Eligible  bool                           `json:"eligible"`
	CanManage bool                           `json:"can_manage"`
	Reason    string                         `json:"reason"`
	Bindings  []infoFlowOwnPrivate           `json:"bindings"`
	Request   *infoFlowPrivateWebRequestView `json:"request"`
	Robot     *infoFlowPrivateRobotPreview   `json:"robot,omitempty"`
}
type infoFlowConversationsView struct {
	Enabled bool `json:"enabled"`
	Group   struct {
		CanManage bool                  `json:"can_manage"`
		Binding   *infoFlowWebState     `json:"binding"`
		Request   *infoFlowWebState     `json:"request"`
		Groups    []infoFlowGroupOption `json:"groups"`
	} `json:"group"`
	Private infoFlowPrivateWebView `json:"private"`
}
type infoFlowOwnBinding struct {
	Binding              infoFlowBinding
	Robot, Agent, Sender string
}

func (h *Handler) infoFlowWebSender(ctx context.Context, ws string, user pgtype.UUID) (string, error) {
	var email string
	err := h.DB.QueryRow(ctx, `SELECT u.email FROM member m JOIN "user" u ON u.id=m.user_id WHERE m.workspace_id=$1 AND m.user_id=$2 FOR SHARE OF m,u`, ws, user).Scan(&email)
	if err != nil {
		return "", err
	}
	email = strings.ToLower(email)
	sender, domain, found := strings.Cut(email, "@")
	if !found || domain != strings.ToLower(os.Getenv("INFOFLOW_ISSUE_IDENTITY_DOMAIN")) || !infoFlowUsername.MatchString(sender) || auth.IsTemporarilyDisabledUser(uuidToString(user), email) {
		return "", nil
	}
	return sender, nil
}

func infoFlowOwnPrivateBindings(ctx context.Context, tx pgx.Tx, ws string, issue, user pgtype.UUID, sender string) ([]infoFlowOwnBinding, error) {
	rows, err := tx.Query(ctx, `SELECT `+infoFlowBindingColumns+`,robot_id,agent_id::text,conversation_id FROM infoflow_issue_binding WHERE workspace_id=$1 AND issue_id=$2 AND binder_id=$3 AND conversation_kind='private' AND conversation_id=$4 ORDER BY id`, ws, issue, user, sender)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []infoFlowOwnBinding{}
	for rows.Next() {
		var v infoFlowOwnBinding
		v.Binding, err = infoFlowScanBinding(rows, &v.Robot, &v.Agent, &v.Sender)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func infoFlowPrivateSnapshot(rows []infoFlowOwnBinding) []byte {
	out := []map[string]string{}
	for _, b := range rows {
		out = append(out, map[string]string{"binding_id": uuidToString(b.Binding.ID), "generation": uuidToString(b.Binding.Generation), "robot_id": b.Robot, "agent_id": b.Agent})
	}
	raw, _ := json.Marshal(out)
	return raw
}

func (h *Handler) infoFlowConversationView(ctx context.Context, tx pgx.Tx, ws string, issue, user pgtype.UUID) (infoFlowConversationsView, error) {
	out := infoFlowConversationsView{Enabled: infoFlowWebEnabled(), Private: infoFlowPrivateWebView{Bindings: []infoFlowOwnPrivate{}}}
	out.Group.Groups = []infoFlowGroupOption{}
	if !out.Enabled {
		return out, nil
	}
	// Fence the issue before membership/agent rows, matching assignment writes.
	var live pgtype.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM issue WHERE id=$1 AND workspace_id=$2 FOR SHARE`, issue, ws).Scan(&live); err != nil {
		return out, err
	}
	group, err := h.infoFlowWebView(ctx, tx, ws, issue, user)
	if err != nil {
		return out, err
	}
	out.Group.CanManage, out.Group.Binding, out.Group.Request, out.Group.Groups = group.CanManage, group.Binding, group.Request, group.Groups
	sh, _ := h.infoFlowScoped(tx)
	var id, state, reason string
	var expires time.Time
	var generation pgtype.UUID
	err = tx.QueryRow(ctx, `SELECT id::text,state,reason,expires_at,generation FROM infoflow_web_private_request WHERE issue_id=$1 AND workspace_id=$2 AND requester_id=$3`, issue, ws, user).Scan(&id, &state, &reason, &expires, &generation)
	if err == nil && state != "active" {
		canCancel := false
		if infoFlowWebRequestPending(state, expires) {
			canCancel, err = h.infoFlowCanCancelWebRequest(ctx, tx, infoFlowWebActor{ws, issue, user}, "private", user)
			if err != nil {
				return out, err
			}
		} else if state == "queued" || state == "pending" {
			state, reason = "failed", "request_expired"
		}
		out.Private.Request = &infoFlowPrivateWebRequestView{RequestID: id, State: state, Reason: infoFlowPrivateWebReason(reason), Generation: uuidToString(generation), CanCancel: canCancel}
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	sender, err := sh.infoFlowWebSender(ctx, ws, user)
	if err != nil {
		return out, err
	}
	if sender == "" {
		out.Private.Reason = "无法确认你的如流账号"
		return out, nil
	}
	rows, err := infoFlowOwnPrivateBindings(ctx, tx, ws, issue, user, sender)
	if err != nil {
		return out, err
	}
	management := infoFlowBinding{}
	if len(rows) > 0 {
		management = rows[0].Binding
	}
	t, _, manage, err := sh.infoFlowWebAuthority(ctx, ws, issue, user, management)
	if err != nil {
		return out, err
	}
	out.Private.Eligible = t.AssigneeType == "agent" && t.LeaderAgentID != "" && slices.Contains(t.AgentIDs, t.LeaderAgentID)
	out.Private.CanManage = out.Private.Eligible && manage || len(rows) > 0
	if out.Private.Eligible && manage {
		robot, err := infoFlowPrivateRobot(ctx, tx, ws, t.LeaderAgentID)
		if err != nil {
			return out, err
		}
		out.Private.Robot = &robot
	}
	if !out.Private.Eligible {
		out.Private.Reason = "私聊仅支持由单个智能体负责的任务；小队、人工负责人或未分配任务请使用群聊"
	}
	for _, row := range rows {
		v := sh.infoFlowPrivateView(ctx, row.Binding, ws, row.Agent)
		out.Private.Bindings = append(out.Private.Bindings, infoFlowOwnPrivate{BindingID: uuidToString(row.Binding.ID), RobotID: row.Robot, RobotName: row.Binding.RobotName, RobotChatID: row.Binding.RobotChatID, State: v.State, Reason: v.Reason})
	}
	return out, nil
}

func (h *Handler) GetIssueInfoFlowConversations(w http.ResponseWriter, r *http.Request) {
	if isMachineCredentialActor(r) {
		writeError(w, 403, "Only human actors can manage conversations")
		return
	}
	i, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	m, ok := h.requireWorkspaceMember(w, r, uuidToString(i.WorkspaceID), "issue not found")
	if !ok {
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "Failed to load conversations")
		return
	}
	defer tx.Rollback(r.Context())
	out, err := h.infoFlowConversationView(r.Context(), tx, uuidToString(i.WorkspaceID), i.ID, m.UserID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 404, "issue not found")
		} else {
			writeError(w, 500, "Failed to load conversations")
		}
		return
	}
	writeJSON(w, 200, out)
}

func (h *Handler) BindIssueInfoFlowConversation(w http.ResponseWriter, r *http.Request) {
	if isMachineCredentialActor(r) {
		writeError(w, 403, "Only human actors can manage conversations")
		return
	}
	var req struct {
		Kind    string          `json:"kind"`
		GroupID json.RawMessage `json:"group_id,omitempty"`
	}
	if !infoFlowDecode(w, r, &req) {
		return
	}
	var groupID string
	if req.Kind != "group" && req.Kind != "private" || req.Kind == "private" && len(req.GroupID) != 0 || req.Kind == "group" && (json.Unmarshal(req.GroupID, &groupID) != nil || !infoFlowValidGroupID(groupID)) {
		writeError(w, 400, "Invalid conversation target")
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
	result, err := h.infoFlowSubmitWebBinding(r.Context(), infoFlowWebActor{ws, i.ID, m.UserID}, req.Kind, groupID, true)
	if err != nil {
		infoFlowWriteActionError(w, err)
		return
	}
	writeJSON(w, result.Status, result.Conversations)
}

func (h *Handler) CancelIssueInfoFlowRequest(w http.ResponseWriter, r *http.Request) {
	if isMachineCredentialActor(r) {
		writeError(w, 403, "Only human actors can manage conversations")
		return
	}
	var req struct {
		Kind               string `json:"kind"`
		ExpectedGeneration string `json:"expected_generation"`
	}
	if !infoFlowDecode(w, r, &req) {
		return
	}
	if _, err := infoFlowWebRequestTable(req.Kind); err != nil {
		infoFlowWriteActionError(w, err)
		return
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "requestId"), "request_id")
	if !ok {
		return
	}
	generation, ok := parseUUIDOrBadRequest(w, req.ExpectedGeneration, "expected_generation")
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
		writeError(w, 503, "InfoFlow conversations are not enabled")
		return
	}
	out, err := h.infoFlowCancelWebRequest(r.Context(), infoFlowWebActor{ws, i.ID, m.UserID}, req.Kind, id, generation)
	if err != nil {
		infoFlowWriteActionError(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (h *Handler) DeleteIssueInfoFlowConversation(w http.ResponseWriter, r *http.Request) {
	if isMachineCredentialActor(r) {
		writeError(w, 403, "Only human actors can manage conversations")
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
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "bindingId"), "binding_id")
	if !ok {
		return
	}
	var robot, agent, kind, conversation string
	err := h.DB.QueryRow(r.Context(), `SELECT robot_id,agent_id::text,conversation_kind,conversation_id FROM infoflow_issue_binding WHERE id=$1 AND issue_id=$2 AND workspace_id=$3`, id, i.ID, ws).Scan(&robot, &agent, &kind, &conversation)
	if err != nil {
		writeError(w, 404, "binding not found")
		return
	}
	scope := infoFlowScope{RobotID: robot, AgentID: agent, WorkspaceID: ws}
	c := infoFlowConversation{Kind: kind, ID: conversation}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "Failed to remove conversation")
		return
	}
	defer tx.Rollback(r.Context())
	if err = infoFlowLock(r.Context(), tx, scope, c); err != nil {
		writeError(w, 500, "Failed to remove conversation")
		return
	}
	if kind == "private" {
		if err = infoFlowPrivateWebLock(r.Context(), tx, i.ID, m.UserID); err != nil {
			writeError(w, 500, "Failed to remove conversation")
			return
		}
	}
	b, err := infoFlowLoadBindingID(r.Context(), tx, scope, c, uuidToString(id))
	if err != nil || b.IssueID != i.ID {
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
	noticeBinding := b
	noticeBinding.BinderID = m.UserID
	if err = infoFlowEnqueueNotice(r.Context(), tx, scope, c, noticeBinding, "unbound"); err != nil {
		writeError(w, 500, "Failed to queue unbind notice")
		return
	}
	_, err = tx.Exec(r.Context(), `UPDATE infoflow_issue_binding SET issue_id=NULL,binder_id=NULL,generation=$2,group_state='pending',leader_verified_at=NULL WHERE id=$1`, id, dbid.NewV7())
	if err != nil {
		writeError(w, 500, "Failed to remove conversation")
		return
	}
	err = infoFlowInvalidateWebRequest(r.Context(), tx, kind, infoFlowWebActor{ws, i.ID, m.UserID}, "binding_changed")
	if err != nil {
		writeError(w, 500, "Failed to remove conversation request")
		return
	}
	out, err := h.infoFlowConversationView(r.Context(), tx, ws, i.ID, m.UserID)
	if err != nil {
		infoFlowWriteActionError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "Failed to remove conversation")
		return
	}
	writeJSON(w, 200, out)
}

func infoFlowPrivateWebReason(reason string) string {
	return map[string]string{"": "等待确认负责智能体的私聊机器人", "active": "私聊绑定成功", "request_expired": "确认已超时，请重新提交", "permission_changed": "任务负责人或权限已变化，请重新绑定", "binding_changed": "私聊绑定已变化，请重新检查", "private_robot_missing_or_ambiguous": "负责人机器人未配置或有多个匹配，请确认唯一机器人", "private_robot_unavailable": "负责人的机器人暂不可用", "private_chat_disabled": "负责人机器人未开启私聊", "private_metadata_failed": "无法获取机器人对话信息，请稍后重试", infoFlowRequestCancelled: "申请已取消，可重新提交"}[reason]
}

func infoFlowAssignmentText(version int64) string { return strconv.FormatInt(version, 10) }
