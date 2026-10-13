package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
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
	RequestID string `json:"request_id"`
	State     string `json:"state"`
	Reason    string `json:"reason"`
}
type infoFlowPrivateWebView struct {
	Eligible  bool                           `json:"eligible"`
	CanManage bool                           `json:"can_manage"`
	Reason    string                         `json:"reason"`
	Bindings  []infoFlowOwnPrivate           `json:"bindings"`
	Request   *infoFlowPrivateWebRequestView `json:"request"`
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
	if !out.Private.Eligible {
		out.Private.Reason = "私聊仅支持由单个智能体负责的任务；小队、人工负责人或未分配任务请使用群聊"
	}
	for _, row := range rows {
		v := sh.infoFlowPrivateView(ctx, row.Binding, ws, row.Agent)
		out.Private.Bindings = append(out.Private.Bindings, infoFlowOwnPrivate{BindingID: uuidToString(row.Binding.ID), RobotID: row.Robot, RobotName: row.Binding.RobotName, RobotChatID: row.Binding.RobotChatID, State: v.State, Reason: v.Reason})
	}
	var id, state, reason string
	var expires time.Time
	err = tx.QueryRow(ctx, `SELECT id::text,state,reason,expires_at FROM infoflow_web_private_request WHERE issue_id=$1 AND workspace_id=$2 AND requester_id=$3`, issue, ws, user).Scan(&id, &state, &reason, &expires)
	if err == nil && state != "active" {
		if (state == "queued" || state == "pending") && !expires.After(time.Now()) {
			state, reason = "failed", "request_expired"
		}
		out.Private.Request = &infoFlowPrivateWebRequestView{RequestID: id, State: state, Reason: infoFlowPrivateWebReason(reason)}
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, err
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
	if req.Kind == "group" {
		body, _ := json.Marshal(map[string]string{"group_id": groupID})
		capture := &infoFlowCapture{}
		request := r.Clone(r.Context())
		request.Body = http.NoBody
		request.Body = io.NopCloser(bytes.NewReader(body))
		h.BindIssueInfoFlowGroup(capture, request)
		if capture.status != 200 && capture.status != 202 {
			w.WriteHeader(capture.status)
			_, _ = w.Write(capture.Bytes())
			return
		}
		view := &infoFlowCapture{}
		h.GetIssueInfoFlowConversations(view, r)
		w.Header().Set("Content-Type", "application/json")
		if view.status != 200 {
			w.WriteHeader(view.status)
		} else {
			w.WriteHeader(capture.status)
		}
		_, _ = w.Write(view.Bytes())
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
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "Failed to request private binding")
		return
	}
	defer tx.Rollback(r.Context())
	if err = infoFlowPrivateWebLock(r.Context(), tx, i.ID, m.UserID); err != nil {
		writeError(w, 500, "Failed to request private binding")
		return
	}
	var live pgtype.UUID
	if err = tx.QueryRow(r.Context(), `SELECT id FROM issue WHERE id=$1 AND workspace_id=$2 FOR SHARE`, i.ID, ws).Scan(&live); err != nil {
		writeError(w, 404, "issue not found")
		return
	}
	sh, _ := h.infoFlowScoped(tx)
	sender, err := sh.infoFlowWebSender(r.Context(), ws, m.UserID)
	if err != nil || sender == "" {
		writeError(w, 403, "Cannot verify private conversation identity")
		return
	}
	rows, err := infoFlowOwnPrivateBindings(r.Context(), tx, ws, i.ID, m.UserID, sender)
	if err != nil {
		writeError(w, 500, "Failed to read private binding")
		return
	}
	manager := infoFlowBinding{}
	if len(rows) > 0 {
		manager = rows[0].Binding
	}
	t, _, manage, err := sh.infoFlowWebAuthority(r.Context(), ws, i.ID, m.UserID, manager)
	if err != nil {
		writeError(w, 500, "Failed to verify assignment")
		return
	}
	if t.AssigneeType != "agent" || t.LeaderAgentID == "" || !slices.Contains(t.AgentIDs, t.LeaderAgentID) {
		writeError(w, 409, "Private conversations require a single assigned agent")
		return
	}
	if !manage {
		writeError(w, 403, "Insufficient binding management permissions")
		return
	}
	status := 202
	alreadyActive := false
	for _, row := range rows {
		if row.Agent == t.LeaderAgentID && row.Binding.RobotChatID != "" && sh.infoFlowPrivateView(r.Context(), row.Binding, ws, row.Agent).State == "active" {
			alreadyActive = true
		}
	}
	if alreadyActive {
		status = 200
	} else {
		var state string
		var expires time.Time
		var originalVersion int64
		err = tx.QueryRow(r.Context(), `SELECT state,expires_at,assignment_version FROM infoflow_web_private_request WHERE issue_id=$1 AND requester_id=$2 FOR UPDATE`, i.ID, m.UserID).Scan(&state, &expires, &originalVersion)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 500, "Failed to read request")
			return
		}
		if (state != "queued" && state != "pending") || !expires.After(time.Now()) || originalVersion != t.AssignmentVersion {
			_, err = tx.Exec(r.Context(), `INSERT INTO infoflow_web_private_request(id,issue_id,workspace_id,requester_id,sender,agent_id,assignment_version,generation,binding_snapshot,state,expires_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'queued',clock_timestamp()+interval '20 minutes')
			ON CONFLICT(issue_id,requester_id) DO UPDATE SET workspace_id=EXCLUDED.workspace_id,sender=EXCLUDED.sender,agent_id=EXCLUDED.agent_id,assignment_version=EXCLUDED.assignment_version,generation=EXCLUDED.generation,binding_snapshot=EXCLUDED.binding_snapshot,state='queued',reason='',lease_token=NULL,lease_robot_id=NULL,lease_agent_id=NULL,lease_until=NULL,next_at=clock_timestamp(),expires_at=EXCLUDED.expires_at,updated_at=clock_timestamp()`, dbid.NewV7(), i.ID, ws, m.UserID, sender, t.LeaderAgentID, t.AssignmentVersion, dbid.NewV7(), infoFlowPrivateSnapshot(rows))
			if err != nil {
				writeError(w, 500, "Failed to request private binding")
				return
			}
		}
	}
	out, err := h.infoFlowConversationView(r.Context(), tx, ws, i.ID, m.UserID)
	if err != nil {
		writeError(w, 500, "Failed to read conversations")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "Failed to commit binding request")
		return
	}
	writeJSON(w, status, out)
}

func infoFlowPrivateWebLock(ctx context.Context, tx pgx.Tx, issue, user pgtype.UUID) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "infoflow-web-private:"+uuidToString(issue)+":"+uuidToString(user))
	return err
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
	_, err = tx.Exec(r.Context(), `UPDATE infoflow_issue_binding SET issue_id=NULL,binder_id=NULL,generation=$2,group_state='pending',leader_verified_at=NULL WHERE id=$1`, id, dbid.NewV7())
	if err != nil {
		writeError(w, 500, "Failed to remove conversation")
		return
	}
	if kind == "private" {
		_, err = tx.Exec(r.Context(), `UPDATE infoflow_web_private_request SET state='failed',reason='binding_changed',generation=$3,lease_token=NULL,lease_until=NULL WHERE issue_id=$1 AND requester_id=$2 AND state IN('queued','pending')`, i.ID, m.UserID, dbid.NewV7())
	} else {
		_, err = tx.Exec(r.Context(), `UPDATE infoflow_web_group_request SET state='failed',reason='binding_changed',generation=$2,lease_token=NULL,lease_until=NULL WHERE issue_id=$1 AND state IN('queued','pending')`, i.ID, dbid.NewV7())
	}
	if err != nil {
		writeError(w, 500, "Failed to remove conversation request")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "Failed to remove conversation")
		return
	}
	h.GetIssueInfoFlowConversations(w, r)
}

func infoFlowPrivateWebReason(reason string) string {
	return map[string]string{"": "等待确认负责智能体的私聊机器人", "active": "私聊绑定成功", "request_expired": "确认已超时，请重新提交", "permission_changed": "任务负责人或权限已变化，请重新绑定", "binding_changed": "私聊绑定已变化，请重新检查", "private_robot_missing_or_ambiguous": "负责人机器人未配置或有多个匹配，请确认唯一机器人", "private_robot_unavailable": "负责人的机器人暂不可用", "private_chat_disabled": "负责人机器人未开启私聊", "private_metadata_failed": "无法获取机器人对话信息，请稍后重试"}[reason]
}

func infoFlowAssignmentText(version int64) string { return strconv.FormatInt(version, 10) }
