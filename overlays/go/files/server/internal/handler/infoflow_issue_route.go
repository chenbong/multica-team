package handler

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
)

const infoFlowBindingColumns = `id,issue_id,binder_id,generation,bound_at,conversation_kind,group_topology,group_state,COALESCE(leader_robot_id,''),leader_verified_at`

func infoFlowScanBinding(row pgx.Row) (infoFlowBinding, error) {
	var b infoFlowBinding
	err := row.Scan(&b.ID, &b.IssueID, &b.BinderID, &b.Generation, &b.BoundAt, &b.Kind, &b.GroupTopology, &b.GroupState, &b.LeaderRobotID, &b.LeaderVerifiedAt)
	return b, err
}

func infoFlowLoadBindingID(ctx context.Context, tx pgx.Tx, s infoFlowScope, c infoFlowConversation, id string) (infoFlowBinding, error) {
	return infoFlowScanBinding(tx.QueryRow(ctx, `SELECT `+infoFlowBindingColumns+` FROM infoflow_issue_binding WHERE id::text=$6 AND workspace_id=$2 AND conversation_kind=$4 AND conversation_id=$5 AND ($4='group' OR (robot_id=$1 AND agent_id::text=lower($3)))`, s.RobotID, s.WorkspaceID, s.AgentID, c.Kind, c.ID, strings.ToLower(id)))
}

var infoFlowTaskRef = regexp.MustCompile(`(?i)\b[A-Z0-9]{1,10}-[0-9]+\b`)

// The caller holds the conversation advisory lock. This receipt lookup spans
// all bindings, including tombstones, so a redelivered external message cannot
// acquire a second task when another robot or selector handles its retry.
func infoFlowGroupReceipt(ctx context.Context, tx pgx.Tx, req infoFlowRelayRequest) (infoFlowBinding, error) {
	var id string
	err := tx.QueryRow(ctx, `SELECT b.id::text FROM infoflow_issue_source s JOIN infoflow_issue_binding b ON b.id=s.binding_id WHERE b.conversation_kind='group' AND b.conversation_id=$1 AND s.message_id=$2 ORDER BY s.created_at,s.id LIMIT 1`, req.Conversation.ID, req.MessageID).Scan(&id)
	if err != nil {
		return infoFlowBinding{}, err
	}
	return infoFlowLoadBindingID(ctx, tx, req.infoFlowScope, req.Conversation, id)
}

// Selectors never use private conversations or another group's provenance.
// Quoted messages have priority over text, even when the text names another task.
func infoFlowGroupQuoteBinding(ctx context.Context, tx pgx.Tx, req infoFlowRelayRequest) (string, bool, error) {
	if req.ReplyToMessageID == "" {
		return "", false, nil
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT b.id::text, (b.issue_id IS NOT NULL AND b.generation=x.generation AND b.issue_id=x.issue_id) AS current FROM infoflow_issue_binding b JOIN (
	 SELECT binding_id,generation,issue_id FROM infoflow_issue_source WHERE message_id=$3
	 UNION ALL SELECT d.binding_id,d.generation,c.issue_id FROM infoflow_issue_delivery d JOIN comment c ON c.id=d.comment_id WHERE d.external_message_id=$3 OR $3=ANY(d.external_message_ids)
	) x ON x.binding_id=b.id WHERE b.conversation_kind='group' AND b.conversation_id=$1 AND b.workspace_id=$2`, req.Conversation.ID, req.WorkspaceID, req.ReplyToMessageID)
	if err != nil {
		return "", false, err
	}
	defer rows.Close()
	var ids []string
	stale := false
	for rows.Next() {
		var id string
		var current bool
		if err := rows.Scan(&id, &current); err != nil {
			return "", false, err
		}
		ids = append(ids, id)
		stale = stale || !current
	}
	if err := rows.Err(); err != nil {
		return "", false, err
	}
	if len(ids) == 0 {
		var elsewhere bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM infoflow_issue_source WHERE message_id=$1) OR EXISTS(SELECT 1 FROM infoflow_issue_delivery WHERE external_message_id=$1 OR $1=ANY(external_message_ids))`, req.ReplyToMessageID).Scan(&elsewhere)
		return "", elsewhere, err
	}
	if len(ids) != 1 || stale {
		return "", true, nil
	}
	return ids[0], true, nil
}

func infoFlowExplicitRef(req infoFlowRelayRequest) (string, bool) {
	if strings.TrimSpace(req.IssueRef) != "" {
		return strings.TrimSpace(req.IssueRef), false
	}
	refs := infoFlowTaskRef.FindAllString(strings.ToUpper(req.Content), -1)
	slices.Sort(refs)
	refs = slices.Compact(refs)
	if len(refs) > 1 {
		return "", true
	}
	if len(refs) == 1 {
		return refs[0], false
	}
	return "", false
}

// Returns a handled notice for missing/ambiguous selectors. Only a genuinely
// unbound group may fall back to Chat. The caller must gate every notice by
// workspace membership before exposing task metadata or choices.
func (h *Handler) infoFlowRouteGroup(ctx context.Context, tx pgx.Tx, req infoFlowRelayRequest) (infoFlowBinding, string, error) {
	if req.MessageID != "" {
		b, err := infoFlowGroupReceipt(ctx, tx, req)
		if err == nil {
			return b, "", nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return b, "", err
		}
		var received, echo bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM infoflow_issue_source s JOIN infoflow_issue_binding b ON b.id=s.binding_id WHERE b.conversation_kind='group' AND b.conversation_id=$1 AND s.message_id=$2),EXISTS(SELECT 1 FROM infoflow_issue_delivery d JOIN infoflow_issue_binding b ON b.id=d.binding_id WHERE b.conversation_kind='group' AND b.conversation_id=$1 AND (d.external_message_id=$2 OR $2=ANY(d.external_message_ids)))`, req.Conversation.ID, req.MessageID).Scan(&received, &echo)
		if err != nil {
			return b, "", err
		}
		if received || echo {
			return b, "", errInfoFlowGroupEcho
		}
	}
	if req.Operation == "create" || req.Operation == "binding" && req.Command == "create" {
		return infoFlowBinding{}, "", nil
	}
	var anyGroup bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM infoflow_issue_binding WHERE conversation_kind='group' AND conversation_id=$1 AND issue_id IS NOT NULL)`, req.Conversation.ID).Scan(&anyGroup)
	if err != nil {
		return infoFlowBinding{}, "", err
	}
	ordinary := req.Operation == "message" || req.Operation == "binding" && req.Command == ""
	if !anyGroup && ordinary && req.ExpectedBindingID == "" && req.ExpectedGeneration == "" && req.IssueRef == "" {
		return infoFlowBinding{}, "", nil
	}
	ref, many := infoFlowExplicitRef(req)
	if req.IssueRef == "" && ordinary {
		var prefix string
		if err = tx.QueryRow(ctx, `SELECT issue_prefix FROM workspace WHERE id=$1`, req.WorkspaceID).Scan(&prefix); err != nil {
			return infoFlowBinding{}, "", err
		}
		refs := []string{}
		for _, loc := range infoFlowTaskRef.FindAllStringIndex(req.Content, -1) {
			key := strings.ToUpper(req.Content[loc[0]:loc[1]])
			if !strings.HasPrefix(key, strings.ToUpper(prefix)+"-") {
				continue
			}
			if loc[1] < len(req.Content) && (req.Content[loc[1]] == '-' || req.Content[loc[1]] == '_' || req.Content[loc[1]] >= 'a' && req.Content[loc[1]] <= 'z' || req.Content[loc[1]] >= 'A' && req.Content[loc[1]] <= 'Z') {
				continue
			}
			refs = append(refs, key)
		}
		slices.Sort(refs)
		refs = slices.Compact(refs)
		ref = ""
		many = len(refs) > 1
		if len(refs) == 1 {
			ref = refs[0]
		}
	}
	var selected string
	commandTarget := req.Operation == "unbind" || req.Operation == "binding" && req.Command != ""
	if req.Operation != "bind" && !(commandTarget && ref != "") {
		quoted, known, err := infoFlowGroupQuoteBinding(ctx, tx, req)
		if err != nil {
			return infoFlowBinding{}, "", err
		}
		if known {
			if quoted == "" {
				return infoFlowBinding{}, "引用消息的任务绑定已失效，请重新指定任务编号", nil
			}
			selected = quoted
			ref = ""
			many = false
		}
	}
	if many {
		return infoFlowBinding{}, "消息包含多个任务编号，请指定一个任务或回复对应任务消息", nil
	}
	if selected == "" && ref != "" {
		var issue string
		err := tx.QueryRow(ctx, `SELECT i.id::text FROM issue i JOIN workspace w ON w.id=i.workspace_id WHERE i.workspace_id=$1 AND (i.id::text=lower($2) OR upper(w.issue_prefix || '-' || i.number::text)=upper($2))`, req.WorkspaceID, ref).Scan(&issue)
		if errors.Is(err, pgx.ErrNoRows) {
			return infoFlowBinding{}, "未找到该工作区中的任务，请检查任务编号", nil
		}
		if err != nil {
			return infoFlowBinding{}, "", err
		}
		err = tx.QueryRow(ctx, `SELECT id::text FROM infoflow_issue_binding WHERE workspace_id=$1 AND conversation_kind='group' AND conversation_id=$2 AND issue_id=$3`, req.WorkspaceID, req.Conversation.ID, issue).Scan(&selected)
		if errors.Is(err, pgx.ErrNoRows) {
			if req.Operation == "bind" {
				return infoFlowBinding{}, "", nil
			}
			return infoFlowBinding{}, "该任务未绑定当前群，请先使用 /bind 任务编号", nil
		}
		if err != nil {
			return infoFlowBinding{}, "", err
		}
	}
	if req.ExpectedBindingID != "" {
		if selected != "" && !strings.EqualFold(selected, req.ExpectedBindingID) {
			return infoFlowBinding{}, "", errInfoFlowRouteChanged
		}
		returnBinding, err := infoFlowLoadBindingID(ctx, tx, req.infoFlowScope, req.Conversation, req.ExpectedBindingID)
		if errors.Is(err, pgx.ErrNoRows) {
			return infoFlowBinding{}, "", errInfoFlowRouteChanged
		}
		return returnBinding, "", err
	}
	if selected != "" {
		b, err := infoFlowLoadBindingID(ctx, tx, req.infoFlowScope, req.Conversation, selected)
		return b, "", err
	}
	if req.Operation == "bind" {
		return infoFlowBinding{}, "请指定要绑定的任务编号", nil
	}
	rows, err := tx.Query(ctx, `SELECT id::text FROM infoflow_issue_binding WHERE workspace_id=$1 AND conversation_kind='group' AND conversation_id=$2 AND issue_id IS NOT NULL ORDER BY id`, req.WorkspaceID, req.Conversation.ID)
	if err != nil {
		return infoFlowBinding{}, "", err
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
		return infoFlowBinding{}, "", err
	}
	var candidates []infoFlowBinding
	for _, id := range ids {
		b, e := infoFlowLoadBindingID(ctx, tx, req.infoFlowScope, req.Conversation, id)
		if e != nil {
			return b, "", e
		}
		if req.Operation == "message" {
			var live string
			e = tx.QueryRow(ctx, `SELECT id::text FROM issue WHERE id=$1 AND workspace_id=$2 FOR NO KEY UPDATE`, b.IssueID, req.WorkspaceID).Scan(&live)
			if errors.Is(e, pgx.ErrNoRows) {
				continue
			}
			if e != nil {
				return b, "", e
			}
		}
		t, reason, e := h.infoFlowTopology(ctx, b.IssueID, req.WorkspaceID)
		if e != nil {
			return b, "", e
		}
		if reason == "" && slices.Contains(t.AgentIDs, strings.ToLower(req.AgentID)) {
			candidates = append(candidates, b)
		}
	}
	if len(candidates) == 1 {
		return candidates[0], "", nil
	}
	if len(candidates) > 1 {
		return infoFlowBinding{}, "当前智能体关联多个任务，请回复对应任务消息或指定任务编号", nil
	}
	if anyGroup {
		return infoFlowBinding{}, "当前群没有可路由到该智能体的任务，请指定已绑定的任务编号或联系任务负责人", nil
	}
	return infoFlowBinding{}, "", nil
}

var errInfoFlowRouteChanged = errors.New("binding route changed")
var errInfoFlowGroupEcho = errors.New("group message already handled")
