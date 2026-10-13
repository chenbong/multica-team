package handler

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func infoFlowConversationFixture(t *testing.T) infoFlowTestFixture {
	f := newInfoFlowWebFixture(t)
	t.Cleanup(func() { dbfx.Exec(t, `DELETE FROM infoflow_web_private_request WHERE requester_id=$1`, f.owner) })
	return f
}

func infoFlowConversationCall(t *testing.T, f infoFlowTestFixture, method, user, binding string, body any, status int) infoFlowConversationsView {
	t.Helper()
	r := testutil.WithURLParams(testutil.WithHeaders(testutil.JSONRequest(method, "/conversations", body), "X-User-ID", user, "X-Workspace-ID", f.req.WorkspaceID), "id", f.issue, "bindingId", binding)
	h := f.h.GetIssueInfoFlowConversations
	if method == "POST" {
		h = f.h.BindIssueInfoFlowConversation
	}
	if method == "DELETE" {
		h = f.h.DeleteIssueInfoFlowConversation
	}
	c := testutil.Call(t, h, r).Want(status)
	var out infoFlowConversationsView
	if status == 200 || status == 202 {
		c.JSON(&out)
	}
	return out
}

type infoFlowPrivateClaim struct {
	RequestID  string `json:"request_id"`
	Generation string `json:"generation"`
	LeaseToken string `json:"lease_token"`
	Sender     string `json:"sender"`
	AgentID    string `json:"agent_id"`
	Version    string `json:"assignment_version"`
}

func infoFlowPrivateClaimCall(t *testing.T, f infoFlowTestFixture) []infoFlowPrivateClaim {
	t.Helper()
	r := testutil.WithHeaders(testutil.JSONRequest("POST", "/private-jobs", f.req.infoFlowScope), "Authorization", "Bearer "+infoFlowTestToken)
	var out struct {
		Jobs []infoFlowPrivateClaim `json:"jobs"`
	}
	testutil.Call(t, f.h.InfoFlowWebPrivateJobs, r).Want(200).JSON(&out)
	return out.Jobs
}

func infoFlowPrivateResultFor(f infoFlowTestFixture, j infoFlowPrivateClaim) infoFlowPrivateResult {
	return infoFlowPrivateResult{infoFlowScope: f.req.infoFlowScope, RequestID: j.RequestID, Generation: j.Generation, LeaseToken: j.LeaseToken, TargetAgentID: j.AgentID, TargetRobotID: f.req.RobotID, RobotChatID: "9007199254740993", RobotName: "Fixture robot"}
}

func infoFlowPrivateResultCall(t *testing.T, f infoFlowTestFixture, req infoFlowPrivateResult, status int) string {
	t.Helper()
	r := testutil.WithHeaders(testutil.JSONRequest("POST", "/private-result", req), "Authorization", "Bearer "+infoFlowTestToken)
	c := testutil.Call(t, f.h.InfoFlowWebPrivateResult, r).Want(status)
	var out struct {
		State string `json:"state"`
	}
	if status == 200 {
		c.JSON(&out)
	}
	return out.State
}

func TestInfoFlowPrivateMultiTaskRoutingAndSelectiveUnbind(t *testing.T) {
	f := infoFlowConversationFixture(t)
	f.req.Conversation = infoFlowConversation{Kind: "private", ID: f.req.Sender}
	first := f.bind(t)
	second := dbfx.Issue(t, "Second private task", testutil.Cols{"creator_id": f.owner, "assignee_type": "agent", "assignee_id": f.req.AgentID})
	bind := f.req
	bind.MessageID, bind.IssueRef = uuid.NewString(), second
	other := infoFlowCall(t, f.h, bind, 200)
	if other.Binding == nil || other.Binding.BindingID == first.Binding.BindingID || infoFlowCount(t, `SELECT count(*) FROM infoflow_issue_binding WHERE robot_id=$1 AND issue_id IS NOT NULL`, f.req.RobotID) != 2 {
		t.Fatal("private bind was not additive")
	}
	lookup := f.req
	lookup.Operation, lookup.MessageID, lookup.IssueRef = "binding", "", ""
	ambiguous := infoFlowCall(t, f.h, lookup, 200)
	if ambiguous.Binding != nil || !strings.Contains(ambiguous.Notice, "多个任务") {
		t.Fatalf("ambiguous route: %+v", ambiguous)
	}
	input := f.req
	input.Operation, input.MessageID, input.Content = "message", uuid.NewString(), "Original task"
	input.ExpectedBindingID, input.ExpectedGeneration = first.Binding.BindingID, first.Binding.Generation
	one := infoFlowCall(t, f.h, input, 200)
	input.MessageID, input.IssueRef, input.ReplyToMessageID = uuid.NewString(), "", input.MessageID
	input.Content = other.Binding.Identifier + " but quoting the first task"
	quoted := infoFlowCall(t, f.h, input, 200)
	if one.CommentID == "" || quoted.Binding == nil || quoted.Binding.IssueID != f.issue || quoted.CommentID == "" {
		t.Fatal("private quote did not take priority")
	}
	input.ReplyToMessageID, input.MessageID, input.IssueRef = "", uuid.NewString(), second
	input.ExpectedBindingID, input.ExpectedGeneration = other.Binding.BindingID, other.Binding.Generation
	if got := infoFlowCall(t, f.h, input, 200); got.Binding.IssueID != second || got.CommentID == "" {
		t.Fatal("explicit second task did not route")
	}
	unbind := f.req
	unbind.Operation, unbind.MessageID = "unbind", uuid.NewString()
	unbind.ExpectedBindingID, unbind.ExpectedGeneration = first.Binding.BindingID, first.Binding.Generation
	if got := infoFlowCall(t, f.h, unbind, 200); got.ConversationMode != "issue" {
		t.Fatal("removing one task lost remaining task mode")
	}
	if got := infoFlowCall(t, f.h, lookup, 200); got.Binding == nil || got.Binding.IssueID != second {
		t.Fatal("remaining unique task not routable")
	}
}

func TestInfoFlowPrivateAssignmentABARequiresExplicitRebind(t *testing.T) {
	f := infoFlowConversationFixture(t)
	f.req.Conversation = infoFlowConversation{Kind: "private", ID: f.req.Sender}
	bound := f.bind(t)
	other := dbfx.Agent(t, "New responsible agent", f.runtime, testutil.Cols{"owner_id": f.owner})
	input := f.req
	input.Operation, input.MessageID, input.Content = "message", uuid.NewString(), "accepted before reassignment"
	input.ExpectedBindingID, input.ExpectedGeneration = bound.Binding.BindingID, bound.Binding.Generation
	accepted := infoFlowCall(t, f.h, input, 200)
	var task string
	dbfx.QueryRow(t, `SELECT id::text FROM agent_task_queue WHERE issue_id=$1 AND agent_id=$2`, f.issue, f.req.AgentID).Scan(&task)
	infoFlowAgentReply(t, f, f.issue, accepted.CommentID, task, "Old private reply")
	deliveries := infoFlowPoll(t, f, 20)
	if len(deliveries) != 1 {
		t.Fatal("missing original private delivery")
	}
	dbfx.Exec(t, `UPDATE issue SET assignee_id=$2 WHERE id=$1`, f.issue, other)
	dbfx.Exec(t, `UPDATE issue SET assignee_id=$2 WHERE id=$1`, f.issue, f.req.AgentID)
	if len(infoFlowPoll(t, f, 20)) != 0 {
		t.Fatal("old private output followed reassignment")
	}
	infoFlowAck(t, f, deliveries[0].ID, nil, 409)
	lookup := input
	lookup.Operation, lookup.MessageID = "binding", ""
	got := infoFlowCall(t, f.h, lookup, 200)
	if got.Binding.Private.State != "needs_rebind" || got.CanComment {
		t.Fatalf("ABA restored private binding: %+v", got)
	}
	if replay := infoFlowCall(t, f.h, input, 200); !replay.Handled || replay.CommentID != "" {
		t.Fatal("old receipt followed reassignment")
	}
	input.MessageID = uuid.NewString()
	if denied := infoFlowCall(t, f.h, input, 200); denied.CommentID != "" || denied.CanComment {
		t.Fatal("stale private invoked new assignment")
	}
	if accepted.CommentID == "" || infoFlowCount(t, `SELECT count(*) FROM comment WHERE issue_id=$1 AND author_type='member'`, f.issue) != 1 {
		t.Fatal("stale input wrote comments")
	}
	bind := f.req
	bind.MessageID = uuid.NewString()
	rebound := infoFlowCall(t, f.h, bind, 200)
	if rebound.Binding.Generation == bound.Binding.Generation || rebound.Binding.Private.State != "active" {
		t.Fatal("explicit rebind did not rotate")
	}
	infoFlowCall(t, f.h, input, 409)
	input.MessageID = uuid.NewString()
	input.ExpectedGeneration = rebound.Binding.Generation
	if infoFlowCall(t, f.h, input, 200).CommentID == "" {
		t.Fatal("explicitly rebound task not callable")
	}
}

func TestInfoFlowAssignmentVersionIgnoresPresentationChanges(t *testing.T) {
	f := infoFlowConversationFixture(t)
	f.req.Conversation = infoFlowConversation{Kind: "private", ID: f.req.Sender}
	bound := f.bind(t)
	dbfx.Exec(t, `UPDATE issue SET title='New title',description='New description',assignee_id=assignee_id WHERE id=$1`, f.issue)
	dbfx.Exec(t, `UPDATE agent SET name='Renamed agent' WHERE id=$1`, f.req.AgentID)
	lookup := f.req
	lookup.Operation, lookup.MessageID = "binding", ""
	got := infoFlowCall(t, f.h, lookup, 200)
	if got.Binding.Generation != bound.Binding.Generation || got.Binding.Private.State != "active" {
		t.Fatal("cosmetic edit invalidated assignment")
	}
}

func TestInfoFlowGroupAssignmentRechecksPresenceAndPinsOldInput(t *testing.T) {
	f := infoFlowConversationFixture(t)
	bound := f.bind(t)
	input := f.req
	input.Operation, input.MessageID, input.Content = "message", uuid.NewString(), "Prepared before reassignment"
	input.ExpectedBindingID, input.ExpectedGeneration = bound.Binding.BindingID, bound.Binding.Generation
	other := dbfx.Agent(t, "New group leader", f.runtime, testutil.Cols{"owner_id": f.owner})
	dbfx.Exec(t, `UPDATE issue SET assignee_id=$2 WHERE id=$1`, f.issue, other)
	lookup := f.req
	lookup.Operation, lookup.MessageID = "binding", ""
	got := infoFlowCall(t, f.h, lookup, 200)
	if got.Binding.Group.State != "pending" || got.CanComment || got.Binding.Group.LeaderAgentID != other {
		t.Fatal("group did not fence reassignment")
	}
	status := infoFlowGroupStatusRequest{infoFlowScope: f.req.infoFlowScope, Conversation: f.req.Conversation, BindingID: bound.Binding.BindingID, Generation: bound.Binding.Generation, LeaderAgentID: other, LeaderRobotID: "replacement-robot", LeaderPresent: true}
	status.AgentID, status.RobotID = other, status.LeaderRobotID
	infoFlowGroupStatus(t, f, status, 409)
	status.AssignmentVersion = infoFlowAssignmentText(got.Binding.Group.AssignmentVersion)
	active := infoFlowGroupStatus(t, f, status, 200)
	if !active.BindingRevalidated || active.Binding.Generation == bound.Binding.Generation || active.Binding.Group.State != "active" {
		t.Fatal("fresh proof did not rotate assignment generation")
	}
	input.AgentID, input.RobotID = other, status.RobotID
	infoFlowCall(t, f.h, input, 409)
	input.ExpectedGeneration = active.Binding.Generation
	if infoFlowCall(t, f.h, input, 200).CommentID == "" {
		t.Fatal("fresh group binding not callable")
	}
}

func TestInfoFlowUnifiedPrivateVerifiedWebAndPrivacy(t *testing.T) {
	f := infoFlowConversationFixture(t)
	group := f.bind(t)
	view := infoFlowConversationCall(t, f, "POST", f.owner, "", map[string]string{"kind": "private"}, 202)
	if !view.Private.Eligible || view.Private.Request == nil || len(view.Private.Bindings) != 0 || view.Group.Binding == nil {
		t.Fatal("private queued before metadata or lost group")
	}
	requestID := view.Private.Request.RequestID
	view = infoFlowConversationCall(t, f, "POST", f.owner, "", map[string]string{"kind": "private"}, 202)
	if view.Private.Request.RequestID != requestID {
		t.Fatal("duplicate submit reset request")
	}
	jobs := infoFlowPrivateClaimCall(t, f)
	if len(jobs) != 1 || jobs[0].Sender != f.req.Sender || jobs[0].AgentID != f.req.AgentID || jobs[0].Version != "0" {
		t.Fatalf("claim: %+v", jobs)
	}
	if len(infoFlowPrivateClaimCall(t, f)) != 0 {
		t.Fatal("lease claimed twice")
	}
	if state := infoFlowPrivateResultCall(t, f, infoFlowPrivateResultFor(f, jobs[0]), 200); state != "active" {
		t.Fatal(state)
	}
	view = infoFlowConversationCall(t, f, "GET", f.owner, "", nil, 200)
	if len(view.Private.Bindings) != 1 || view.Private.Bindings[0].RobotChatID != "9007199254740993" || view.Private.Bindings[0].State != "active" || view.Group.Binding.BindingID != group.Binding.BindingID {
		t.Fatalf("verified view: %+v", view)
	}
	id := view.Private.Bindings[0].BindingID
	foreign := infoFlowConversationCall(t, f, "GET", f.user, "", nil, 200)
	if len(foreign.Private.Bindings) != 0 || foreign.Private.Request != nil {
		t.Fatal("admin saw another user's DM")
	}
	infoFlowConversationCall(t, f, "DELETE", f.user, id, nil, 403)
	view = infoFlowConversationCall(t, f, "DELETE", f.owner, id, nil, 200)
	if len(view.Private.Bindings) != 0 || view.Group.Binding == nil {
		t.Fatal("private removal affected group")
	}
	if infoFlowCount(t, `SELECT count(*) FROM comment WHERE issue_id=$1`, f.issue) != 0 || infoFlowCount(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1`, f.issue) != 0 {
		t.Fatal("binding created work")
	}
}

func TestInfoFlowPrivateWebRejectsMixedTargetsAndSquads(t *testing.T) {
	f := infoFlowConversationFixture(t)
	for _, body := range []any{map[string]any{"kind": "private", "group_id": nil}, map[string]string{"kind": "private", "sender": "another"}, map[string]string{"kind": "private", "group_id": "123"}, map[string]string{"kind": "group"}, map[string]any{"kind": "group", "group_id": 123}} {
		infoFlowConversationCall(t, f, "POST", f.owner, "", body, 400)
	}
	squad := dbfx.Insert(t, "squad", testutil.Cols{"workspace_id": f.req.WorkspaceID, "name": "Private forbidden squad", "leader_id": f.req.AgentID, "creator_id": f.owner})
	f.req.Conversation = infoFlowConversation{Kind: "private", ID: f.req.Sender}
	bound := f.bind(t)
	dbfx.Exec(t, `UPDATE issue SET assignee_type='squad',assignee_id=$2 WHERE id=$1`, f.issue, squad)
	infoFlowConversationCall(t, f, "POST", f.owner, "", map[string]string{"kind": "private"}, 409)
	view := infoFlowConversationCall(t, f, "GET", f.owner, "", nil, 200)
	if view.Private.Eligible || !view.Private.CanManage || len(view.Private.Bindings) != 1 || view.Private.Bindings[0].State != "needs_rebind" {
		t.Fatal("squad did not invalidate retained private binding")
	}
	infoFlowConversationCall(t, f, "DELETE", f.owner, bound.Binding.BindingID, nil, 200)
	if infoFlowCount(t, `SELECT count(*) FROM infoflow_web_private_request WHERE issue_id=$1`, f.issue) != 0 {
		t.Fatal("invalid private requests stored")
	}
	for _, source := range []string{"task_token", "cloud_pat"} {
		r := testutil.WithHeaders(testutil.JSONRequest(http.MethodPost, "/private", map[string]string{"kind": "private"}), "X-Actor-Source", source)
		testutil.Call(t, f.h.BindIssueInfoFlowConversation, r).Want(403)
	}
}

func TestInfoFlowUnassignedGroupCanBeRemovedButNotReactivated(t *testing.T) {
	f := infoFlowConversationFixture(t)
	bound := f.bind(t)
	dbfx.Exec(t, `UPDATE issue SET assignee_type=NULL,assignee_id=NULL WHERE id=$1`, f.issue)
	view := infoFlowConversationCall(t, f, "GET", f.owner, "", nil, 200)
	if view.Group.Binding == nil || view.Group.Binding.State != "pending" || !view.Group.CanManage {
		t.Fatal("invalid group trapped its authorized manager")
	}
	infoFlowConversationCall(t, f, "POST", f.owner, "", map[string]string{"kind": "group", "group_id": f.req.Conversation.ID}, 409)
	view = infoFlowConversationCall(t, f, "DELETE", f.owner, bound.Binding.BindingID, nil, 200)
	if view.Group.Binding != nil {
		t.Fatal("invalid group could not be removed")
	}
}

func TestInfoFlowPrivateWebStaleResultsCannotReactivate(t *testing.T) {
	for _, change := range []string{"assignment", "membership", "lease", "generation", "unbind", "archive", "scope"} {
		t.Run(change, func(t *testing.T) {
			f := infoFlowConversationFixture(t)
			f.req.Conversation = infoFlowConversation{Kind: "private", ID: f.req.Sender}
			bound := f.bind(t)
			infoFlowConversationCall(t, f, "POST", f.owner, "", map[string]string{"kind": "private"}, 202)
			jobs := infoFlowPrivateClaimCall(t, f)
			if len(jobs) != 1 {
				t.Fatal(jobs)
			}
			req := infoFlowPrivateResultFor(f, jobs[0])
			want := 200
			switch change {
			case "assignment":
				dbfx.Exec(t, `UPDATE issue SET assignee_type=NULL,assignee_id=NULL WHERE id=$1`, f.issue)
			case "membership":
				dbfx.Exec(t, `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, f.req.WorkspaceID, f.owner)
			case "lease":
				dbfx.Exec(t, `UPDATE infoflow_web_private_request SET lease_until=clock_timestamp()-interval '1 second' WHERE issue_id=$1`, f.issue)
				want = 409
			case "generation":
				req.Generation = uuid.NewString()
				want = 409
			case "unbind":
				infoFlowConversationCall(t, f, "DELETE", f.owner, bound.Binding.BindingID, nil, 200)
				want = 409
			case "archive":
				dbfx.Exec(t, `UPDATE agent SET archived_at=clock_timestamp() WHERE id=$1`, f.req.AgentID)
				want = 400
			case "scope":
				req.RobotID = uuid.NewString()
				want = 409
			}
			if state := infoFlowPrivateResultCall(t, f, req, want); want == 200 && state != "failed" {
				t.Fatal("stale result activated", state)
			}
			if n := infoFlowCount(t, `SELECT count(*) FROM infoflow_issue_binding WHERE issue_id=$1 AND robot_chat_id<>''`, f.issue); n != 0 {
				t.Fatal("stale result wrote robot metadata")
			}
		})
	}
}

func TestInfoFlowPendingPrivateRequestBlocksTransfer(t *testing.T) {
	f := infoFlowConversationFixture(t)
	infoFlowConversationCall(t, f, "POST", f.owner, "", map[string]string{"kind": "private"}, 202)
	target := dbfx.Workspace(t, "Private binding transfer destination", "private-transfer-"+uuid.NewString())
	dbfx.Member(t, target, f.owner, "owner")
	dbfx.Exec(t, `INSERT INTO issue_status(workspace_id,key,name,category,color,position) VALUES($1,'cancelled','Cancelled','closed','#808080',0),($2,'cancelled','Cancelled','closed','#808080',0) ON CONFLICT DO NOTHING`, target, f.req.WorkspaceID)
	dbfx.Exec(t, `UPDATE issue SET status='cancelled' WHERE id=$1`, f.issue)
	preview, err := f.h.runIssueTransfer(context.Background(), parseUUID(f.issue), parseUUID(f.req.WorkspaceID), parseUUID(target), parseUUID(f.owner), issueTransferRequest{}, false)
	if err != nil || !containsTransferBlocker(preview, "infoflow_pending_private_binding") {
		t.Fatalf("pending private request ignored: %+v %v", preview, err)
	}
}
