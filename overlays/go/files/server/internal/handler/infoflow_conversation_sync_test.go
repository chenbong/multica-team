package handler

import (
	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
	"testing"
)

func infoFlowSyncCall(t *testing.T, f infoFlowTestFixture, user, binding, generation string, enabled any, status int) infoFlowConversationsView {
	t.Helper()
	r := testutil.WithURLParams(testutil.WithHeaders(testutil.JSONRequest("PUT", "/sync",
		map[string]any{"expected_generation": generation, "sync_enabled": enabled}),
		"X-User-ID", user, "X-Workspace-ID", f.req.WorkspaceID), "id", f.issue, "bindingId", binding)
	c := testutil.Call(t, f.h.UpdateIssueInfoFlowSync, r).Want(status)
	var out infoFlowConversationsView
	if status == 200 {
		c.JSON(&out)
	}
	return out
}

func TestInfoFlowSyncPauseCancelsPendingAndResumeIsFutureOnly(t *testing.T) {
	f := newInfoFlowNotifyFixture(t)
	bound := f.bind(t).Binding
	_, input, task := infoFlowNotifyInput(t, f, f.req.Sender, nil)
	infoFlowAgentReply(t, f, f.issue, input.CommentID, task, "Pending before pause")
	queued := infoFlowPoll(t, f, 20)
	if len(queued) != 1 || !broadcastCheck(t, f, queued[0].ID) {
		t.Fatal("missing eligible pending delivery")
	}
	notices := infoFlowCount(t, `SELECT count(*) FROM infoflow_conversation_notice WHERE binding_id=$1`, bound.BindingID)
	view := infoFlowSyncCall(t, f, f.owner, bound.BindingID, bound.Generation, false, 200)
	if view.Group.Binding == nil || view.Group.Binding.SyncEnabled {
		t.Fatal("pause not persisted")
	}
	if broadcastCheck(t, f, queued[0].ID) || len(infoFlowPoll(t, f, 20)) != 0 {
		t.Fatal("stale snapshot remained sendable")
	}
	_, during, duringTask := infoFlowNotifyInput(t, f, f.req.Sender, nil)
	if during.CommentID == "" {
		t.Fatal("pause blocked incoming instructions")
	}
	infoFlowAgentReply(t, f, f.issue, during.CommentID, duringTask, "Created while paused")
	if len(infoFlowPoll(t, f, 20)) != 0 {
		t.Fatal("paused content escaped")
	}
	view = infoFlowSyncCall(t, f, f.owner, bound.BindingID, bound.Generation, true, 200)
	if !view.Group.Binding.SyncEnabled || len(infoFlowPoll(t, f, 20)) != 0 || broadcastCheck(t, f, queued[0].ID) {
		t.Fatal("resume replayed historical content or revived a cancelled delivery")
	}
	fresh := infoFlowAgentReply(t, f, f.issue, during.CommentID, duringTask, "Created after resume")
	rows := infoFlowPoll(t, f, 20)
	if len(rows) != 1 || rows[0].CommentID != fresh {
		t.Fatal("new reply not resumed")
	}
	infoFlowSyncCall(t, f, f.owner, bound.BindingID, bound.Generation, true, 200)
	if !broadcastCheck(t, f, rows[0].ID) {
		t.Fatal("idempotent enable moved the cutoff")
	}
	if infoFlowCount(t, `SELECT count(*) FROM infoflow_issue_delivery WHERE binding_id=$1 AND cancelled_at IS NOT NULL`, bound.BindingID) != 1 {
		t.Fatal("pending cancellation not recorded")
	}
	if infoFlowCount(t, `SELECT count(*) FROM infoflow_conversation_notice WHERE binding_id=$1`, bound.BindingID) != notices {
		t.Fatal("toggle emitted conversation notices")
	}
}

func TestInfoFlowSyncGroupAndPrivateAreIndependent(t *testing.T) {
	f := newInfoFlowNotifyFixture(t)
	group := f.bind(t).Binding
	private := f
	private.req.Conversation = infoFlowConversation{Kind: "private", ID: f.req.Sender}
	private.req.MessageID = uuid.NewString()
	pb := private.bind(t).Binding
	view := infoFlowSyncCall(t, f, f.owner, group.BindingID, group.Generation, false, 200)
	if len(view.Private.Bindings) != 1 || !view.Private.Bindings[0].SyncEnabled {
		t.Fatal("group pause changed private sync")
	}
	parent := dbfx.Comment(t, f.issue, "Web instruction", testutil.Cols{"author_id": f.owner})
	task := dbfx.Task(t, f.req.AgentID, testutil.Cols{"issue_id": f.issue, "runtime_id": f.runtime, "trigger_comment_id": parent})
	infoFlowAgentReply(t, f, f.issue, parent, task, "Private-only result")
	rows := infoFlowDrainBroadcast(t, f)
	if len(rows) != 2 || rows[0].Conversation.Kind != "private" || rows[1].Conversation.Kind != "private" {
		t.Fatal("expected private-only result")
	}
	infoFlowSyncCall(t, private, f.owner, pb.BindingID, pb.Generation, false, 200)
	infoFlowSyncCall(t, f, f.owner, group.BindingID, group.Generation, true, 200)
	infoFlowAgentReply(t, f, f.issue, parent, task, "Group-only result")
	rows = infoFlowDrainBroadcast(t, f)
	if len(rows) != 1 || rows[0].Conversation.Kind != "group" {
		t.Fatal("expected group-only result")
	}
}

func TestInfoFlowSyncUsesBindingPermissionsAndVersion(t *testing.T) {
	f := newInfoFlowNotifyFixture(t)
	bound := f.bind(t).Binding
	outsider := dbfx.User(t, "Non-manager", "outsider-"+uuid.NewString()+"@example.test")
	dbfx.Member(t, f.req.WorkspaceID, outsider, "member")
	infoFlowSyncCall(t, f, outsider, bound.BindingID, bound.Generation, false, 403)
	infoFlowSyncCall(t, f, f.owner, bound.BindingID, uuid.NewString(), false, 409)
	infoFlowSyncCall(t, f, f.owner, bound.BindingID, bound.Generation, "false", 400)
	infoFlowSyncCall(t, f, f.owner, bound.BindingID, bound.Generation, nil, 400)
	private := f
	private.req.Conversation = infoFlowConversation{Kind: "private", ID: f.req.Sender}
	private.req.MessageID = uuid.NewString()
	pb := private.bind(t).Binding
	admin := dbfx.User(t, "Other admin", "admin-"+uuid.NewString()+"@example.test")
	dbfx.Member(t, f.req.WorkspaceID, admin, "admin")
	infoFlowSyncCall(t, private, admin, pb.BindingID, pb.Generation, false, 403)
	view := infoFlowSyncCall(t, private, f.owner, pb.BindingID, pb.Generation, false, 200)
	if view.Private.Bindings[0].SyncEnabled {
		t.Fatal("private owner could not pause")
	}
}

func TestInfoFlowSyncDefaultsOnAndExplicitRebindRestoresDefault(t *testing.T) {
	f := newInfoFlowNotifyFixture(t)
	bound := f.bind(t).Binding
	view := infoFlowConversationCall(t, f, "GET", f.owner, "", nil, 200)
	if view.Group.Binding == nil || !view.Group.Binding.SyncEnabled {
		t.Fatal("new binding not enabled by default")
	}
	infoFlowSyncCall(t, f, f.owner, bound.BindingID, bound.Generation, false, 200)
	infoFlowConversationCall(t, f, "DELETE", f.owner, bound.BindingID, nil, 200)
	f.req.MessageID = uuid.NewString()
	f.bind(t)
	view = infoFlowConversationCall(t, f, "GET", f.owner, "", nil, 200)
	if view.Group.Binding == nil || !view.Group.Binding.SyncEnabled {
		t.Fatal("new bind retained a previous pause")
	}
	infoFlowSyncCall(t, f, f.owner, bound.BindingID, bound.Generation, false, 404)
}
