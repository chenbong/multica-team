package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
)

type infoFlowWebState struct {
	BindingID  string `json:"binding_id,omitempty"`
	GroupID    string `json:"group_id"`
	Name       string `json:"name,omitempty"`
	State      string `json:"state"`
	Reason     string `json:"reason"`
	RequestID  string `json:"request_id,omitempty"`
	Generation string `json:"generation,omitempty"`
	CanCancel  bool   `json:"can_cancel,omitempty"`
}
type infoFlowGroupOption struct {
	GroupID string `json:"group_id"`
	Name    string `json:"name"`
}
type infoFlowWebView struct {
	Enabled   bool                  `json:"enabled"`
	CanManage bool                  `json:"can_manage"`
	Binding   *infoFlowWebState     `json:"binding"`
	Request   *infoFlowWebState     `json:"request"`
	Groups    []infoFlowGroupOption `json:"groups"`
}

func infoFlowWebEnabled() bool {
	return len(os.Getenv("INFOFLOW_ISSUE_RELAY_TOKEN")) >= 32 && infoFlowDomain.MatchString(os.Getenv("INFOFLOW_ISSUE_IDENTITY_DOMAIN"))
}

// Lock order matches IM binds: physical group, issue claim, then issue/entity rows.
func infoFlowWebLock(ctx context.Context, tx pgx.Tx, ws, group string, issue pgtype.UUID) error {
	if err := infoFlowLock(ctx, tx, infoFlowScope{WorkspaceID: ws}, infoFlowConversation{Kind: "group", ID: group}); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "infoflow-group-issue:"+uuidToString(issue))
	return err
}

func infoFlowWebBinding(ctx context.Context, tx pgx.Tx, ws string, issue pgtype.UUID) (infoFlowBinding, string, error) {
	rows, err := tx.Query(ctx, `SELECT `+infoFlowBindingColumns+`,conversation_id FROM infoflow_issue_binding WHERE workspace_id=$1 AND issue_id=$2 AND conversation_kind='group'`, ws, issue)
	if err != nil {
		return infoFlowBinding{}, "", err
	}
	defer rows.Close()
	var b infoFlowBinding
	var group string
	count := 0
	for rows.Next() {
		count++
		if b, err = infoFlowScanBinding(rows, &group); err != nil {
			return b, "", err
		}
	}
	if count > 1 {
		return b, "", errors.New("ambiguous issue group binding")
	}
	return b, group, rows.Err()
}

// The requester must still be a human workspace member and an authorized binding
// manager. Cached membership and the configured robot owner's PAT are not gates.
func (h *Handler) infoFlowWebAuthority(ctx context.Context, ws string, issue, user pgtype.UUID, b infoFlowBinding) (infoFlowGroupTopology, string, bool, error) {
	t, reason, err := h.infoFlowTopology(ctx, issue, ws)
	if err != nil {
		return t, "", false, err
	}
	var email, role string
	err = h.DB.QueryRow(ctx, `SELECT u.email,m.role FROM member m JOIN "user" u ON u.id=m.user_id WHERE m.workspace_id=$1 AND m.user_id=$2 FOR SHARE OF m,u`, ws, user).Scan(&email, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, "", false, nil
	}
	if err != nil {
		return t, "", false, err
	}
	email = strings.ToLower(email)
	domain := strings.ToLower(os.Getenv("INFOFLOW_ISSUE_IDENTITY_DOMAIN"))
	local, suffix, found := strings.Cut(email, "@")
	if !found || suffix != domain || !infoFlowUsername.MatchString(local) || auth.IsTemporarilyDisabledUser(uuidToString(user), email) {
		return t, "", false, nil
	}
	// An invalid assignment cannot activate a binding, but its authorized
	// manager must still be able to inspect and remove the retained destination.
	if reason != "" {
		return t, local, b.ID.Valid && (role == "owner" || role == "admin" || b.BinderID == user), nil
	}
	var owner pgtype.UUID
	err = h.DB.QueryRow(ctx, `SELECT owner_id FROM agent WHERE id=$1 AND workspace_id=$2 AND archived_at IS NULL FOR SHARE`, t.LeaderAgentID, ws).Scan(&owner)
	if err != nil {
		return t, "", false, err
	}
	return t, local, role == "owner" || role == "admin" || owner == user || b.BinderID.Valid && b.BinderID == user, nil
}

func (h *Handler) infoFlowWebView(ctx context.Context, tx pgx.Tx, ws string, issue, user pgtype.UUID) (infoFlowWebView, error) {
	out := infoFlowWebView{Enabled: infoFlowWebEnabled(), Groups: []infoFlowGroupOption{}}
	if !out.Enabled {
		return out, nil
	}
	// The outer membership check is a snapshot. Revalidate and fence the member
	// row before exposing even an active destination from this transaction.
	var role string
	if err := tx.QueryRow(ctx, `SELECT role FROM member WHERE workspace_id=$1 AND user_id=$2 FOR SHARE`, ws, user).Scan(&role); err != nil {
		return out, err
	}
	b, group, err := infoFlowWebBinding(ctx, tx, ws, issue)
	if err != nil {
		return out, err
	}
	sh, _ := h.infoFlowScoped(tx)
	_, sender, manage, err := sh.infoFlowWebAuthority(ctx, ws, issue, user, b)
	if err != nil {
		return out, err
	}
	out.CanManage = manage
	if b.ID.Valid {
		v := sh.infoFlowGroupView(ctx, b, ws)
		name, e := infoFlowObservedGroupName(ctx, tx, ws, group)
		if e != nil {
			return out, e
		}
		out.Binding = &infoFlowWebState{BindingID: uuidToString(b.ID), GroupID: group, Name: name, State: v.State, Reason: v.Reason}
	}
	var id, gid, state, reason string
	var expires time.Time
	var originalID, originalGeneration, requester, generation pgtype.UUID
	err = tx.QueryRow(ctx, `SELECT id::text,group_id,state,reason,expires_at,original_binding_id,original_generation,requester_id,generation FROM infoflow_web_group_request WHERE issue_id=$1 AND workspace_id=$2`, issue, ws).Scan(&id, &gid, &state, &reason, &expires, &originalID, &originalGeneration, &requester, &generation)
	current := !b.ID.Valid || group == gid && originalID == b.ID && originalGeneration == b.Generation
	canCancel := false
	if err == nil && infoFlowWebRequestPending(state, expires) {
		canCancel, err = h.infoFlowCanCancelWebRequest(ctx, tx, infoFlowWebActor{ws, issue, user}, "group", requester)
		if err != nil {
			return out, err
		}
	}
	if err == nil && state != "active" && (manage && current && (out.Binding == nil || out.Binding.State != "active") || canCancel) {
		if (state == "queued" || state == "pending") && time.Now().After(expires) {
			state, reason = "failed", "request_expired"
		}
		out.Request = &infoFlowWebState{RequestID: id, GroupID: gid, State: state, Reason: infoFlowWebReason(reason), Generation: uuidToString(generation), CanCancel: canCancel}
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	// An original requester may cancel after reassignment, but does not regain
	// candidate discovery or binding permission from that cancellation right.
	if !manage {
		if out.Binding != nil && out.Binding.State != "active" {
			out.Binding = nil
		}
		return out, nil
	}
	encoded, _ := json.Marshal([]string{sender})
	rows, err := tx.Query(ctx, `SELECT DISTINCT ON (o.group_id) o.group_id,COALESCE((SELECT n.name FROM infoflow_group_observation n WHERE n.workspace_id=o.workspace_id AND n.group_id=o.group_id AND n.name<>'' ORDER BY n.observed_at DESC,n.robot_id LIMIT 1),'') FROM infoflow_group_observation o WHERE o.workspace_id=$1 AND o.members @> $2::jsonb AND o.observed_at>clock_timestamp()-interval '24 hours' ORDER BY o.group_id,o.observed_at DESC LIMIT 100`, ws, encoded)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var opt infoFlowGroupOption
		if err = rows.Scan(&opt.GroupID, &opt.Name); err != nil {
			return out, err
		}
		if infoFlowValidGroupID(opt.GroupID) {
			out.Groups = append(out.Groups, opt)
		}
	}
	return out, rows.Err()
}

func (h *Handler) GetIssueInfoFlowGroupBinding(w http.ResponseWriter, r *http.Request) {
	if isMachineCredentialActor(r) {
		writeError(w, 403, "Only human actors can manage group bindings")
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
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "Failed to load group binding")
		return
	}
	defer tx.Rollback(r.Context())
	out, err := h.infoFlowWebView(r.Context(), tx, ws, i.ID, m.UserID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 404, "issue not found")
			return
		}
		writeError(w, 500, "Failed to load group binding")
		return
	}
	writeJSON(w, 200, out)
}

func (h *Handler) BindIssueInfoFlowGroup(w http.ResponseWriter, r *http.Request) {
	if isMachineCredentialActor(r) {
		writeError(w, 403, "Only human actors can manage group bindings")
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
		writeError(w, 503, "InfoFlow group binding is not enabled")
		return
	}
	var req struct {
		GroupID string `json:"group_id"`
	}
	if !infoFlowDecode(w, r, &req) {
		return
	}
	result, err := h.infoFlowSubmitWebBinding(r.Context(), infoFlowWebActor{ws, i.ID, m.UserID}, "group", req.GroupID, false)
	if err != nil {
		infoFlowWriteActionError(w, err)
		return
	}
	writeJSON(w, result.Status, result.Group)
}

type infoFlowGroupObservation struct {
	infoFlowScope
	GroupID string   `json:"group_id"`
	Name    string   `json:"name"`
	Members []string `json:"members"`
}

func (h *Handler) infoFlowWebScope(ctx context.Context, s infoFlowScope) bool {
	var present bool
	err := h.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent WHERE id=$1 AND workspace_id=$2 AND archived_at IS NULL)`, s.AgentID, s.WorkspaceID).Scan(&present)
	return err == nil && present
}

func (h *Handler) InfoFlowGroupObservation(w http.ResponseWriter, r *http.Request) {
	if !infoFlowAuthenticate(w, r) {
		return
	}
	var req infoFlowGroupObservation
	if !infoFlowDecode(w, r, &req) {
		return
	}
	if !req.infoFlowScope.valid() || !infoFlowValidGroupID(req.GroupID) || len(req.Name) > 256 || strings.ContainsRune(req.Name, 0) || len(req.Members) > 10000 || !h.infoFlowWebScope(r.Context(), req.infoFlowScope) {
		infoFlowError(w, 400, "observation")
		return
	}
	for i, m := range req.Members {
		req.Members[i] = strings.ToLower(m)
		if !infoFlowUsername.MatchString(req.Members[i]) {
			infoFlowError(w, 400, "member")
			return
		}
	}
	slices.Sort(req.Members)
	req.Members = slices.Compact(req.Members)
	if req.Members == nil {
		req.Members = []string{}
	}
	members, _ := json.Marshal(req.Members)
	_, err := h.DB.Exec(r.Context(), `INSERT INTO infoflow_group_observation(workspace_id,group_id,robot_id,agent_id,name,members) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(workspace_id,group_id,robot_id) DO UPDATE SET agent_id=EXCLUDED.agent_id,name=COALESCE(NULLIF(EXCLUDED.name,''),infoflow_group_observation.name),members=EXCLUDED.members,observed_at=clock_timestamp()`, req.WorkspaceID, req.GroupID, req.RobotID, req.AgentID, req.Name, members)
	if err != nil {
		infoFlowError(w, 500, "observation")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func infoFlowObservedGroupName(ctx context.Context, tx pgx.Tx, ws, group string) (string, error) {
	var name string
	err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT name FROM infoflow_group_observation WHERE workspace_id=$1 AND group_id=$2 AND name<>'' ORDER BY observed_at DESC,robot_id LIMIT 1),'')`, ws, group).Scan(&name)
	return name, err
}

func infoFlowWebReason(reason string) string {
	return map[string]string{
		"": "等待确认群成员和机器人入群状态", "approval_pending": "入群申请等待群管理员审批",
		"invitation_failed":  "邀请机器人失败，请检查添加群成员权限、目标机器人群可见范围与群聊开关，或手动拉入机器人",
		"invitation_pending": "入群请求结果待确认", "membership_unconfirmed": "已提交入群请求，等待确认机器人入群",
		"leader_not_in_group": "负责人机器人尚未入群", "membership_check_failed": "无法确认群成员，请检查 API 权限或网络",
		"leader_robot_missing_or_ambiguous": "负责人机器人未配置或有多个匹配，请确认唯一机器人",
		"leader_robot_unavailable":          "负责人机器人不可用，请先恢复连接", "actor_not_in_group": "你不在该如流群中，无法绑定",
		"inviter_not_in_group": "群内没有可用的平台机器人，请先手动拉入一个机器人",
		"request_expired":      "确认已超时，请重新提交绑定", "permission_changed": "任务或绑定权限已变化，请重新提交绑定",
		"binding_changed": "任务群聊绑定已变化，请重新检查", "active": "群聊绑定成功", infoFlowRequestCancelled: "申请已取消，可重新提交",
	}[reason]
}
