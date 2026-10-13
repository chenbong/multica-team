package handler

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func infoFlowPoll(t *testing.T, f infoFlowTestFixture, limit int) []infoFlowDelivery {
	t.Helper()
	var out struct {
		Deliveries []infoFlowDelivery `json:"deliveries"`
	}
	r := testutil.WithHeaders(testutil.JSONRequest("POST", "/deliveries", infoFlowDeliveryRequest{infoFlowScope: f.req.infoFlowScope, Limit: limit}), "Authorization", "Bearer "+infoFlowTestToken)
	testutil.Call(t, f.h.InfoFlowIssueDeliveries, r).Want(200).JSON(&out)
	return out.Deliveries
}

func infoFlowAck(t *testing.T, f infoFlowTestFixture, id string, ids []string, status int) {
	t.Helper()
	r := testutil.WithURLParams(testutil.WithHeaders(testutil.JSONRequest("POST", "/ack", infoFlowDeliveryRequest{infoFlowScope: f.req.infoFlowScope, MessageIDs: ids}), "Authorization", "Bearer "+infoFlowTestToken), "id", id)
	testutil.Call(t, f.h.InfoFlowIssueDeliveryAck, r).Want(status)
}

func infoFlowInput(t *testing.T, f infoFlowTestFixture, conversation infoFlowConversation) (infoFlowRelayRequest, infoFlowRelayResponse, string) {
	t.Helper()
	req := f.req
	req.Conversation = conversation
	req.Operation = "message"
	req.MessageID = uuid.NewString()
	req.Content = "Input for " + conversation.Kind
	out := infoFlowCall(t, f.h, req, 200)
	if out.CommentID == "" {
		t.Fatalf("missing input: %+v", out)
	}
	var task string
	dbfx.QueryRow(t, `SELECT id::text FROM agent_task_queue WHERE trigger_comment_id=$1`, out.CommentID).Scan(&task)
	return req, out, task
}

func infoFlowAgentReply(t *testing.T, f infoFlowTestFixture, issue, parent, task, content string) string {
	t.Helper()
	body := map[string]any{"content": content}
	if parent != "" {
		body["parent_id"] = parent
	}
	r := testutil.WithURLParams(testutil.WithHeaders(testutil.JSONRequest("POST", "/comments", body), "X-Workspace-ID", f.req.WorkspaceID, "X-User-ID", f.owner, "X-Agent-ID", f.req.AgentID, "X-Task-ID", task, "X-Actor-Source", "task_token"), "id", issue)
	var out CommentResponse
	testutil.Call(t, f.h.CreateComment, r).Want(201).JSON(&out)
	if out.AuthorType != "agent" {
		t.Fatalf("reply actor is %s", out.AuthorType)
	}
	return out.ID
}

func TestInfoFlowDeliverySourceAckEchoAndQuotedChunks(t *testing.T) {
	f := newInfoFlowFixture(t)
	dbfx.Comment(t, f.issue, "pre-bind output", testutil.Cols{"author_type": "agent", "author_id": f.req.AgentID})
	f.bind(t)
	input, out, task := infoFlowInput(t, f, f.req.Conversation)
	reply := infoFlowAgentReply(t, f, f.issue, out.CommentID, task, "Answer without any mention")
	deliveries := infoFlowPoll(t, f, 20)
	if len(deliveries) != 1 {
		t.Fatalf("deliveries %+v", deliveries)
	}
	d := deliveries[0]
	if d.CommentID != reply || d.ReplyToMessageID != input.MessageID || d.Conversation != f.req.Conversation || d.Content != "Answer without any mention" || d.Binding == nil || d.Binding.WorkspaceSlug == "" || d.Binding.Identifier == "" {
		t.Fatalf("wrong return routing: %+v", d)
	}
	// Durable polling repeats the same id across a fresh Handler object.
	h2 := *f.h
	f.h = &h2
	if again := infoFlowPoll(t, f, 20); len(again) != 1 || again[0].ID != d.ID {
		t.Fatalf("retry: %+v", again)
	}
	wrong := f
	wrong.req.AgentID = uuid.NewString()
	infoFlowAck(t, wrong, d.ID, nil, 404)
	infoFlowAck(t, f, d.ID, []string{"chunk-one", "chunk-two"}, 200)
	infoFlowAck(t, f, d.ID, []string{"chunk-one", "chunk-two"}, 200)
	if got := infoFlowPoll(t, f, 20); len(got) != 0 {
		t.Fatalf("acked output replayed: %+v", got)
	}
	quote := input
	quote.MessageID = uuid.NewString()
	quote.ReplyToMessageID = "chunk-two"
	quote.Content = "Reply to second output chunk"
	quoted := infoFlowCall(t, f.h, quote, 200)
	if infoFlowCount(t, `SELECT count(*) FROM comment WHERE id=$1 AND parent_id=$2`, quoted.CommentID, reply) != 1 {
		t.Fatal("chunk quote lost direct parent")
	}
	before := infoFlowCount(t, `SELECT count(*) FROM comment WHERE issue_id=$1`, f.issue)
	echo := input
	echo.MessageID = "chunk-one"
	echo.Content = d.Content
	echoOut := infoFlowCall(t, f.h, echo, 200)
	if !echoOut.Handled || !echoOut.Silent || infoFlowCount(t, `SELECT count(*) FROM comment WHERE issue_id=$1`, f.issue) != before {
		t.Fatal("outbound echo became new input")
	}
}

func TestInfoFlowDeliveryPrivateGroupIsolationAndAmbiguity(t *testing.T) {
	f := newInfoFlowFixture(t)
	f.bind(t)
	_, group, groupTask := infoFlowInput(t, f, f.req.Conversation)
	private := f.req
	private.Conversation = infoFlowConversation{Kind: "private", ID: f.req.Sender}
	private.MessageID = uuid.NewString()
	infoFlowCall(t, f.h, private, 200)
	_, priv, privateTask := infoFlowInput(t, f, private.Conversation)
	privateReply := infoFlowAgentReply(t, f, f.issue, priv.CommentID, privateTask, "private answer")
	groupReply := infoFlowAgentReply(t, f, f.issue, group.CommentID, groupTask, "public group answer")
	deliveries := infoFlowPoll(t, f, 20)
	if len(deliveries) != 2 {
		t.Fatalf("want distinct destinations: %+v", deliveries)
	}
	for _, d := range deliveries {
		if (d.CommentID == privateReply && d.Conversation != private.Conversation) || (d.CommentID == groupReply && d.Conversation != f.req.Conversation) {
			t.Fatalf("privacy leak: %+v", d)
		}
		infoFlowAck(t, f, d.ID, []string{uuid.NewString()}, 200)
	}
	// Reading private context does not make an otherwise group-origin reply
	// ambiguous; only actual trigger/coalesced lineage defines provenance.
	dbfx.Exec(t, `UPDATE agent_task_queue SET delivered_comment_ids=ARRAY[$2::uuid] WHERE id=$1`, groupTask, priv.CommentID)
	readReply := infoFlowAgentReply(t, f, f.issue, group.CommentID, groupTask, "group answer after context read")
	deliveries = infoFlowPoll(t, f, 20)
	if len(deliveries) != 1 || deliveries[0].CommentID != readReply || deliveries[0].Conversation.Kind != "group" {
		t.Fatalf("context read incorrectly suppressed: %+v", deliveries)
	}
	infoFlowAck(t, f, deliveries[0].ID, nil, 200)
	// A run actually triggered by both destinations must never broadcast the
	// combined private content, regardless of which parent the agent chose.
	dbfx.Exec(t, `UPDATE agent_task_queue SET coalesced_comment_ids=ARRAY[$2::uuid] WHERE id=$1`, groupTask, priv.CommentID)
	infoFlowAgentReply(t, f, f.issue, group.CommentID, groupTask, "combined PRIVATE answer")
	if got := infoFlowPoll(t, f, 20); len(got) != 0 {
		t.Fatalf("ambiguous output leaked: %+v", got)
	}
	// The private recipient loses workspace access; the original private run
	// may finish, but no new answer is returned for that destination.
	dbfx.Exec(t, `DELETE FROM member WHERE user_id=$1 AND workspace_id=$2`, f.owner, testWorkspaceID)
	infoFlowAgentReply(t, f, f.issue, priv.CommentID, privateTask, "private after membership loss")
	if got := infoFlowPoll(t, f, 20); len(got) != 0 {
		t.Fatalf("revoked private destination delivered: %+v", got)
	}
}

func TestInfoFlowCreateSwitchUnbindAndStaleDelivery(t *testing.T) {
	f := newInfoFlowFixture(t)
	// Fixture.Insert intentionally bypasses the application's issue counter.
	dbfx.Exec(t, `UPDATE workspace SET issue_counter=GREATEST(issue_counter,(SELECT COALESCE(MAX(number),0) FROM issue WHERE workspace_id=$1)) WHERE id=$1`, testWorkspaceID)
	create := f.req
	create.MessageID = uuid.NewString()
	create.Operation = "create"
	create.Content = "A new InfoFlow task " + uuid.NewString()
	create.IssueRef = ""
	out := infoFlowCall(t, f.h, create, 200)
	if out.Binding == nil || out.Binding.IssueID == f.issue || !strings.Contains(out.Notice, "已创建") {
		t.Fatalf("create: %+v", out)
	}
	issue := out.Binding.IssueID
	var task, origin, creator string
	dbfx.QueryRow(t, `SELECT t.id::text,t.originator_user_id::text,i.creator_id::text FROM agent_task_queue t JOIN issue i ON i.id=t.issue_id WHERE t.issue_id=$1 AND t.agent_id=$2`, issue, f.req.AgentID).Scan(&task, &origin, &creator)
	if origin != f.owner || creator != f.owner {
		t.Fatalf("create attribution %s %s", origin, creator)
	}
	retry := infoFlowCall(t, f.h, create, 200)
	if retry.Binding.IssueID != issue {
		t.Fatal("create duplicated")
	}
	infoFlowActivate(t, f, create, out.Binding.Generation)
	infoFlowAgentReply(t, f, issue, "", task, "new issue assignment answer")
	deliveries := infoFlowPoll(t, f, 20)
	if len(deliveries) != 1 {
		t.Fatalf("create-origin output: %+v", deliveries)
	}
	// Switch while output is pending cancels the old generation, including ack.
	f.bind(t)
	if got := infoFlowPoll(t, f, 20); len(got) != 0 {
		t.Fatalf("old issue routed after switch: %+v", got)
	}
	infoFlowAck(t, f, deliveries[0].ID, nil, 409)
	msg := f.req
	msg.Operation = "message"
	msg.MessageID = uuid.NewString()
	msg.Content = "retry after unbind"
	before := infoFlowCall(t, f.h, msg, 200)
	unbind := f.req
	unbind.Operation = "unbind"
	unbind.MessageID = uuid.NewString()
	unbound := infoFlowCall(t, f.h, unbind, 200)
	if unbound.Binding == nil || unbound.Binding.IssueID != f.issue {
		t.Fatal("unbind lost footer metadata")
	}
	if again := infoFlowCall(t, f.h, msg, 200); !again.Handled || again.CommentID != before.CommentID {
		t.Fatalf("old accepted input fell back after unbind: %+v", again)
	}
	msg.MessageID = uuid.NewString()
	if next := infoFlowCall(t, f.h, msg, 200); next.Handled {
		t.Fatalf("new unbound message did not fall through: %+v", next)
	}
	lookup := f.req
	lookup.Operation = "binding"
	lookup.Command = "/binding"
	lookup.MessageID = ""
	if status := infoFlowCall(t, f.h, lookup, 200); !status.Handled || status.Notice == "" {
		t.Fatalf("explicit lookup: %+v", status)
	}
	bad := f.req
	bad.MessageID = uuid.NewString()
	bad.IssueRef = "NOPE-999"
	if status := infoFlowCall(t, f.h, bad, 200); !status.Handled || status.Notice == "" {
		t.Fatal("bad bind jammed retry queue")
	}
}

func TestInfoFlowPollFairnessAcrossFailingDestinations(t *testing.T) {
	f := newInfoFlowFixture(t)
	for i := 0; i < 3; i++ {
		req := f.req
		req.Conversation.ID = fmt.Sprintf("%s%d", f.req.Conversation.ID[:17], i)
		req.MessageID = uuid.NewString()
		bound := infoFlowCall(t, f.h, req, 200)
		infoFlowActivate(t, f, req, bound.Binding.Generation)
		_, out, task := infoFlowInput(t, f, req.Conversation)
		infoFlowAgentReply(t, f, f.issue, out.CommentID, task, "one answer")
	}
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		got := infoFlowPoll(t, f, 1)
		if len(got) != 1 {
			t.Fatalf("poll: %+v", got)
		}
		seen[got[0].Conversation.ID] = true
	}
	if len(seen) != 3 {
		t.Fatalf("unacked destination starved others: %+v", seen)
	}
}

func TestInfoFlowBindAuthorizationAndIndependentScope(t *testing.T) {
	f := newInfoFlowFixture(t)
	f.bind(t)
	var email string
	dbfx.QueryRow(t, `SELECT email FROM "user" WHERE id=$1`, f.user).Scan(&email)
	dbfx.Exec(t, `UPDATE member SET role='member' WHERE id=$1`, f.member)
	req := f.req
	req.Sender = strings.Split(email, "@")[0]
	req.MessageID = uuid.NewString()
	req.Operation = "unbind"
	if out := infoFlowCall(t, f.h, req, 200); !strings.Contains(out.Notice, "只有") {
		t.Fatalf("ordinary member mutated binding: %+v", out)
	}
	// An existing binder can manage after stepping down from admin.
	dbfx.Exec(t, `UPDATE infoflow_issue_binding SET binder_id=$1 WHERE robot_id=$2`, f.user, f.req.RobotID)
	req.MessageID = uuid.NewString()
	out := infoFlowCall(t, f.h, req, 200)
	if !strings.Contains(out.Notice, "已解除") {
		t.Fatalf("binder cannot unbind: %+v", out)
	}
	// A different robot with exactly the same external conversation has no binding.
	req = f.req
	req.RobotID = uuid.NewString()
	req.Operation = "binding"
	req.MessageID = ""
	if out := infoFlowCall(t, f.h, req, 200); out.Handled || out.Binding != nil {
		t.Fatalf("robot scope conflated: %+v", out)
	}
}
