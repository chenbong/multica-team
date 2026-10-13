package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Presence is an admission lease, not a permanent assertion about IM membership.
const infoFlowGroupLease = 45 * time.Second

type infoFlowGroupTopology struct {
	AssigneeType  string   `json:"assignee_type"`
	AssigneeID    string   `json:"assignee_id"`
	LeaderAgentID string   `json:"leader_agent_id"`
	AgentIDs      []string `json:"agent_ids"`
}

type infoFlowGroupView struct {
	State         string   `json:"state"`
	LeaderAgentID string   `json:"leader_agent_id"`
	LeaderRobotID string   `json:"leader_robot_id,omitempty"`
	AgentIDs      []string `json:"agent_ids"`
	Reason        string   `json:"reason,omitempty"`
}

// Call inside the binding transaction for mutations. Issue/squad/agent row
// locks fence reassignment, archived leaders, and existing membership removal.
func (h *Handler) infoFlowTopology(ctx context.Context, issueID pgtype.UUID, wsID string) (infoFlowGroupTopology, string, error) {
	t := infoFlowGroupTopology{AgentIDs: []string{}}
	err := h.DB.QueryRow(ctx, `SELECT COALESCE(assignee_type,''),COALESCE(assignee_id::text,'') FROM issue WHERE id=$1 AND workspace_id=$2 FOR SHARE`, issueID, wsID).Scan(&t.AssigneeType, &t.AssigneeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, "任务已失效或移出工作区", nil
	}
	if err != nil {
		return t, "", err
	}
	switch t.AssigneeType {
	case "agent":
		t.LeaderAgentID = t.AssigneeID
	case "squad":
		err = h.DB.QueryRow(ctx, `SELECT COALESCE(leader_id::text,'') FROM squad WHERE id=$1 AND workspace_id=$2 AND archived_at IS NULL FOR SHARE`, t.AssigneeID, wsID).Scan(&t.LeaderAgentID)
		if errors.Is(err, pgx.ErrNoRows) {
			return t, "任务小队不可用，请重新分配任务", nil
		}
		if err != nil {
			return t, "", err
		}
	default:
		return t, "群聊绑定需要先将任务分配给智能体或小队", nil
	}
	if t.LeaderAgentID == "" {
		return t, "请先为任务小队设置队长", nil
	}
	var leader string
	err = h.DB.QueryRow(ctx, `SELECT id::text FROM agent WHERE id::text=$1 AND workspace_id=$2 AND archived_at IS NULL FOR SHARE`, t.LeaderAgentID, wsID).Scan(&leader)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, "任务负责人或队长智能体不可用", nil
	}
	if err != nil {
		return t, "", err
	}
	t.AgentIDs = append(t.AgentIDs, leader)
	if t.AssigneeType == "squad" {
		rows, err := h.DB.Query(ctx, `SELECT a.id::text FROM squad_member sm JOIN agent a ON a.id=sm.member_id WHERE sm.squad_id=$1 AND sm.member_type='agent' AND a.workspace_id=$2 AND a.archived_at IS NULL ORDER BY a.id FOR SHARE OF sm,a`, t.AssigneeID, wsID)
		if err != nil {
			return t, "", err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				return t, "", err
			}
			t.AgentIDs = append(t.AgentIDs, id)
		}
		if err = rows.Err(); err != nil {
			return t, "", err
		}
	}
	slices.Sort(t.AgentIDs)
	t.AgentIDs = slices.Compact(t.AgentIDs)
	return t, "", nil
}

func infoFlowSameTopology(saved []byte, t infoFlowGroupTopology) bool {
	var old infoFlowGroupTopology
	return json.Unmarshal(saved, &old) == nil && old.AssigneeType == t.AssigneeType && old.AssigneeID == t.AssigneeID && old.LeaderAgentID == t.LeaderAgentID && slices.Equal(old.AgentIDs, t.AgentIDs)
}

func (h *Handler) infoFlowGroupView(ctx context.Context, b infoFlowBinding, wsID string) *infoFlowGroupView {
	t, reason, err := h.infoFlowTopology(ctx, b.IssueID, wsID)
	v := &infoFlowGroupView{State: "pending", LeaderAgentID: t.LeaderAgentID, AgentIDs: t.AgentIDs, Reason: reason}
	if err != nil {
		v.Reason = "暂时无法确认任务负责人，请稍后重试"
		return v
	}
	if reason != "" {
		return v
	}
	if !infoFlowSameTopology(b.GroupTopology, t) {
		v.Reason = "任务负责人或小队成员已变化，等待重新确认队长入群"
		return v
	}
	v.LeaderRobotID = b.LeaderRobotID
	if b.GroupState != "active" || b.LeaderRobotID == "" || !b.LeaderVerifiedAt.Valid || time.Since(b.LeaderVerifiedAt.Time) > infoFlowGroupLease || b.LeaderVerifiedAt.Time.After(time.Now().Add(time.Second)) {
		v.Reason = "等待确认队长或负责智能体的机器人已入群"
		return v
	}
	v.State = "active"
	return v
}

type infoFlowBoundGroup struct {
	Conversation infoFlowConversation `json:"conversation"`
	Binding      *infoFlowBindingView `json:"binding"`
}

// Trusted service discovery intentionally spans robots, but never workspaces.
func (h *Handler) InfoFlowIssueGroups(w http.ResponseWriter, r *http.Request) {
	if !infoFlowAuthenticate(w, r) {
		return
	}
	var s infoFlowScope
	if !infoFlowDecode(w, r, &s) {
		return
	}
	if !s.valid() {
		infoFlowError(w, 400, "scope")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		infoFlowError(w, 500, "begin")
		return
	}
	defer tx.Rollback(r.Context())
	rows, err := tx.Query(r.Context(), `SELECT conversation_id FROM infoflow_issue_binding WHERE workspace_id=$1 AND conversation_kind='group' AND issue_id IS NOT NULL ORDER BY conversation_id`, s.WorkspaceID)
	if err != nil {
		infoFlowError(w, 500, "groups")
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		infoFlowError(w, 500, "groups")
		return
	}
	sh, _ := h.infoFlowScoped(tx)
	out := []infoFlowBoundGroup{}
	for _, id := range ids {
		c := infoFlowConversation{Kind: "group", ID: id}
		b, e := infoFlowLoadBinding(r.Context(), tx, s, c)
		if errors.Is(e, pgx.ErrNoRows) {
			continue
		}
		if e != nil {
			infoFlowError(w, 500, "binding")
			return
		}
		if v := sh.infoFlowBindingView(r.Context(), b, s.WorkspaceID, true); v != nil {
			out = append(out, infoFlowBoundGroup{c, v})
		}
	}
	writeJSON(w, 200, map[string]any{"groups": out})
}

type infoFlowGroupStatusRequest struct {
	infoFlowScope
	Conversation  infoFlowConversation `json:"conversation"`
	Generation    string               `json:"generation"`
	LeaderAgentID string               `json:"leader_agent_id"`
	LeaderRobotID string               `json:"leader_robot_id"`
	LeaderPresent bool                 `json:"leader_present"`
}

func (h *Handler) InfoFlowIssueGroupStatus(w http.ResponseWriter, r *http.Request) {
	if !infoFlowAuthenticate(w, r) {
		return
	}
	var req infoFlowGroupStatusRequest
	if !infoFlowDecode(w, r, &req) {
		return
	}
	if !req.infoFlowScope.valid() || req.Conversation.Kind != "group" || !infoFlowValidGroupID(req.Conversation.ID) || len(req.LeaderRobotID) > 256 || strings.ContainsRune(req.LeaderRobotID, 0) {
		infoFlowError(w, 400, "scope")
		return
	}
	if _, ok := parseUUIDOrBadRequest(w, req.Generation, "generation"); !ok {
		return
	}
	// The trusted Bridge must submit the configured leader mapping, never the
	// unrelated robot whose inbound event happened to initiate reconciliation.
	if (req.LeaderPresent || req.LeaderRobotID != "") && (req.LeaderRobotID != req.RobotID || !strings.EqualFold(req.LeaderAgentID, req.AgentID)) {
		infoFlowError(w, 409, "coordinator")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		infoFlowError(w, 500, "begin")
		return
	}
	defer tx.Rollback(r.Context())
	if err = infoFlowLock(r.Context(), tx, req.infoFlowScope, req.Conversation); err != nil {
		infoFlowError(w, 500, "lock")
		return
	}
	b, err := infoFlowLoadBinding(r.Context(), tx, req.infoFlowScope, req.Conversation)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (!b.IssueID.Valid || uuidToString(b.Generation) != strings.ToLower(req.Generation)) {
		infoFlowError(w, 409, "stale binding")
		return
	}
	if err != nil {
		infoFlowError(w, 500, "binding")
		return
	}
	sh, _ := h.infoFlowScoped(tx)
	t, reason, err := sh.infoFlowTopology(r.Context(), b.IssueID, req.WorkspaceID)
	if err != nil {
		infoFlowError(w, 500, "topology")
		return
	}
	if reason != "" || t.LeaderAgentID != strings.ToLower(req.LeaderAgentID) {
		infoFlowError(w, 409, "stale topology")
		return
	}
	state := "pending"
	var verified any
	if req.LeaderPresent {
		state = "active"
		verified = time.Now()
	}
	topology, _ := json.Marshal(t)
	_, err = tx.Exec(r.Context(), `UPDATE infoflow_issue_binding SET group_topology=$2,group_state=$3,leader_robot_id=NULLIF($4,''),leader_verified_at=$5 WHERE id=$1`, b.ID, topology, state, req.LeaderRobotID, verified)
	if err != nil {
		infoFlowError(w, 500, "status")
		return
	}
	b, err = infoFlowLoadBinding(r.Context(), tx, req.infoFlowScope, req.Conversation)
	if err != nil {
		infoFlowError(w, 500, "binding")
		return
	}
	v := sh.infoFlowBindingView(r.Context(), b, req.WorkspaceID, true)
	notice := "等待队长或负责智能体的机器人入群"
	if state == "active" {
		notice = "群聊任务绑定已启用"
	}
	if err = tx.Commit(r.Context()); err != nil {
		infoFlowError(w, 500, "commit")
		return
	}
	writeJSON(w, 200, infoFlowRelayResponse{Handled: true, ConversationMode: "issue", Binding: v, Notice: notice})
}

func infoFlowGroupURL(id string) string {
	payload, _ := json.Marshal(map[string]any{"APIName": "BdHiJs.service.hi.open", "version": 65, "data": map[string]string{"id": id, "type": "2"}})
	return "infoflow://APICenter?data=" + url.QueryEscape(base64.StdEncoding.EncodeToString(payload))
}

// Normal member-authenticated API; it never exposes private conversation IDs,
// relay tokens, owner credentials, or pending/unverified group links.
func (h *Handler) GetIssueInfoFlowGroups(w http.ResponseWriter, r *http.Request) {
	i, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if _, ok = h.requireWorkspaceMember(w, r, uuidToString(i.WorkspaceID), "issue not found"); !ok {
		return
	}
	if len(os.Getenv("INFOFLOW_ISSUE_RELAY_TOKEN")) < 32 || !infoFlowDomain.MatchString(os.Getenv("INFOFLOW_ISSUE_IDENTITY_DOMAIN")) {
		writeJSON(w, 200, map[string]any{"groups": []any{}})
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "Failed to load groups")
		return
	}
	defer tx.Rollback(r.Context())
	ws := uuidToString(i.WorkspaceID)
	rows, err := tx.Query(r.Context(), `SELECT conversation_id FROM infoflow_issue_binding WHERE workspace_id=$1 AND issue_id=$2 AND conversation_kind='group' ORDER BY conversation_id`, ws, i.ID)
	if err != nil {
		writeError(w, 500, "Failed to load groups")
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		writeError(w, 500, "Failed to load groups")
		return
	}
	sh, _ := h.infoFlowScoped(tx)
	out := []map[string]string{}
	for _, id := range ids {
		if !infoFlowValidGroupID(id) {
			continue
		}
		b, e := infoFlowLoadBinding(r.Context(), tx, infoFlowScope{WorkspaceID: ws}, infoFlowConversation{Kind: "group", ID: id})
		if errors.Is(e, pgx.ErrNoRows) {
			continue
		}
		if e != nil {
			writeError(w, 500, "Failed to load groups")
			return
		}
		if b.IssueID != i.ID {
			continue
		}
		if v := sh.infoFlowGroupView(r.Context(), b, ws); v.State == "active" {
			out = append(out, map[string]string{"group_id": id, "url": infoFlowGroupURL(id)})
		}
	}
	writeJSON(w, 200, map[string]any{"groups": out})
}
