package handler

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

type infoFlowWebSubmission struct {
	Status        int
	Group         *infoFlowWebView
	Conversations *infoFlowConversationsView
}

// Legacy and unified HTTP routes share one transaction and the same business
// functions. No handler calls, response recordings or second admission read.
func (h *Handler) infoFlowSubmitWebBinding(ctx context.Context, actor infoFlowWebActor, kind, group string, unified bool) (infoFlowWebSubmission, error) {
	out := infoFlowWebSubmission{}
	if !infoFlowWebEnabled() {
		return out, infoFlowActionFailure(503, "InfoFlow conversations are not enabled")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	switch kind {
	case "group":
		out.Status, err = h.infoFlowRequestGroupBinding(ctx, tx, actor, group)
	case "private":
		out.Status, err = h.infoFlowRequestPrivateBinding(ctx, tx, actor)
	default:
		err = infoFlowActionFailure(400, "Invalid conversation kind")
	}
	if err != nil {
		return out, err
	}
	if unified {
		var view infoFlowConversationsView
		view, err = h.infoFlowConversationView(ctx, tx, actor.Workspace, actor.Issue, actor.User)
		out.Conversations = &view
	} else {
		var view infoFlowWebView
		view, err = h.infoFlowWebView(ctx, tx, actor.Workspace, actor.Issue, actor.User)
		out.Group = &view
	}
	if err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, err
	}
	return out, nil
}

func (h *Handler) infoFlowRequestGroupBinding(ctx context.Context, tx pgx.Tx, actor infoFlowWebActor, groupID string) (int, error) {
	if !infoFlowValidGroupID(groupID) {
		return 0, infoFlowActionFailure(400, "Invalid group ID")
	}
	if err := infoFlowWebLock(ctx, tx, actor.Workspace, groupID, actor.Issue); err != nil {
		return 0, err
	}
	b, group, err := infoFlowWebBinding(ctx, tx, actor.Workspace, actor.Issue)
	if err != nil {
		return 0, err
	}
	sh, _ := h.infoFlowScoped(tx)
	t, _, manage, err := sh.infoFlowWebAuthority(ctx, actor.Workspace, actor.Issue, actor.User, b)
	if err != nil {
		return 0, err
	}
	if !manage {
		return 0, infoFlowActionFailure(403, "Only the leader owner, workspace admin or current binder can manage group binding")
	}
	if t.LeaderAgentID == "" || !slices.Contains(t.AgentIDs, t.LeaderAgentID) {
		return 0, infoFlowActionFailure(409, "Group binding requires an available assigned agent or squad leader")
	}
	if group != "" && group != groupID {
		return 0, infoFlowActionFailure(409, "Issue is already bound to another group; unbind it first")
	}
	if b.ID.Valid && sh.infoFlowGroupView(ctx, b, actor.Workspace).State == "active" {
		return 200, nil
	}
	var priorGroup, state string
	var expires time.Time
	err = tx.QueryRow(ctx, `SELECT group_id,state,expires_at FROM infoflow_web_group_request WHERE issue_id=$1 FOR UPDATE`, actor.Issue).Scan(&priorGroup, &state, &expires)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	if infoFlowWebRequestPending(state, expires) {
		if priorGroup != groupID {
			return 0, infoFlowActionFailure(409, "Another group binding is awaiting confirmation")
		}
		return 202, nil
	}
	topology, _ := json.Marshal(t)
	_, err = tx.Exec(ctx, `INSERT INTO infoflow_web_group_request(id,issue_id,workspace_id,group_id,requester_id,generation,topology,original_binding_id,original_generation,state,expires_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'queued',clock_timestamp()+interval '20 minutes')
 ON CONFLICT(issue_id) DO UPDATE SET workspace_id=EXCLUDED.workspace_id,group_id=EXCLUDED.group_id,requester_id=EXCLUDED.requester_id,generation=EXCLUDED.generation,topology=EXCLUDED.topology,original_binding_id=EXCLUDED.original_binding_id,original_generation=EXCLUDED.original_generation,state='queued',reason='',lease_token=NULL,lease_robot_id=NULL,lease_agent_id=NULL,lease_until=NULL,next_at=clock_timestamp(),expires_at=EXCLUDED.expires_at,updated_at=clock_timestamp()`, dbid.NewV7(), actor.Issue, actor.Workspace, groupID, actor.User, dbid.NewV7(), topology, b.ID, b.Generation)
	return 202, err
}

func (h *Handler) infoFlowRequestPrivateBinding(ctx context.Context, tx pgx.Tx, actor infoFlowWebActor) (int, error) {
	if err := infoFlowPrivateWebLock(ctx, tx, actor.Issue, actor.User); err != nil {
		return 0, err
	}
	var live pgtype.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM issue WHERE id=$1 AND workspace_id=$2 FOR SHARE`, actor.Issue, actor.Workspace).Scan(&live); err != nil {
		return 0, err
	}
	sh, _ := h.infoFlowScoped(tx)
	sender, err := sh.infoFlowWebSender(ctx, actor.Workspace, actor.User)
	if err != nil {
		return 0, err
	}
	if sender == "" {
		return 0, infoFlowActionFailure(403, "Cannot verify private conversation identity")
	}
	rows, err := infoFlowOwnPrivateBindings(ctx, tx, actor.Workspace, actor.Issue, actor.User, sender)
	if err != nil {
		return 0, err
	}
	manager := infoFlowBinding{}
	if len(rows) > 0 {
		manager = rows[0].Binding
	}
	t, _, manage, err := sh.infoFlowWebAuthority(ctx, actor.Workspace, actor.Issue, actor.User, manager)
	if err != nil {
		return 0, err
	}
	if t.AssigneeType != "agent" || t.LeaderAgentID == "" || !slices.Contains(t.AgentIDs, t.LeaderAgentID) {
		return 0, infoFlowActionFailure(409, "Private conversations require a single assigned agent")
	}
	if !manage {
		return 0, infoFlowActionFailure(403, "Insufficient binding management permissions")
	}
	for _, row := range rows {
		if row.Agent == t.LeaderAgentID && row.Binding.RobotChatID != "" && sh.infoFlowPrivateView(ctx, row.Binding, actor.Workspace, row.Agent).State == "active" {
			return 200, nil
		}
	}
	var state string
	var expires time.Time
	var version int64
	err = tx.QueryRow(ctx, `SELECT state,expires_at,assignment_version FROM infoflow_web_private_request WHERE issue_id=$1 AND requester_id=$2 FOR UPDATE`, actor.Issue, actor.User).Scan(&state, &expires, &version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	if infoFlowWebRequestPending(state, expires) && version == t.AssignmentVersion {
		return 202, nil
	}
	_, err = tx.Exec(ctx, `INSERT INTO infoflow_web_private_request(id,issue_id,workspace_id,requester_id,sender,agent_id,assignment_version,generation,binding_snapshot,state,expires_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'queued',clock_timestamp()+interval '20 minutes')
 ON CONFLICT(issue_id,requester_id) DO UPDATE SET workspace_id=EXCLUDED.workspace_id,sender=EXCLUDED.sender,agent_id=EXCLUDED.agent_id,assignment_version=EXCLUDED.assignment_version,generation=EXCLUDED.generation,binding_snapshot=EXCLUDED.binding_snapshot,state='queued',reason='',lease_token=NULL,lease_robot_id=NULL,lease_agent_id=NULL,lease_until=NULL,next_at=clock_timestamp(),expires_at=EXCLUDED.expires_at,updated_at=clock_timestamp()`, dbid.NewV7(), actor.Issue, actor.Workspace, actor.User, sender, t.LeaderAgentID, t.AssignmentVersion, dbid.NewV7(), infoFlowPrivateSnapshot(rows))
	return 202, err
}
