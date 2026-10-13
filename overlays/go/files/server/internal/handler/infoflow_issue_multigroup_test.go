package handler

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
)

type infoFlowMultiFixture struct {
	a, second infoFlowTestFixture
	b, d      string
	one, two  *infoFlowBindingView
}

func infoFlowMulti(t *testing.T) infoFlowMultiFixture {
	t.Helper()
	f := newInfoFlowFixture(t)
	b := dbfx.Agent(t, "B", f.runtime, testutil.Cols{"owner_id": f.owner})
	d := dbfx.Agent(t, "D", f.runtime, testutil.Cols{"owner_id": f.owner})
	second := f
	second.issue = dbfx.Issue(t, "Second squad issue", testutil.Cols{"creator_id": f.owner, "assignee_type": "agent", "assignee_id": f.req.AgentID})
	second.req.IssueRef = second.issue
	second.req.MessageID = uuid.NewString()
	for i, pair := range []struct{ issue, member string }{{f.issue, b}, {second.issue, d}} {
		squad := dbfx.Insert(t, "squad", testutil.Cols{"workspace_id": f.req.WorkspaceID, "name": fmt.Sprint("Squad", i), "leader_id": f.req.AgentID, "creator_id": f.owner})
		dbfx.Insert(t, "squad_member", testutil.Cols{"squad_id": squad, "member_type": "agent", "member_id": pair.member, "role": "member"})
		dbfx.Exec(t, `UPDATE issue SET assignee_type='squad',assignee_id=$2 WHERE id=$1`, pair.issue, squad)
	}
	one := f.bind(t).Binding
	two := second.bind(t).Binding
	return infoFlowMultiFixture{a: f, second: second, b: b, d: d, one: one, two: two}
}

func infoFlowPreflight(t *testing.T, f infoFlowTestFixture, req infoFlowRelayRequest) infoFlowRelayResponse {
	t.Helper()
	req.Operation = "binding"
	return infoFlowCall(t, f.h, req, 200)
}

func infoFlowMultiMessage(f infoFlowTestFixture, agent, content string) infoFlowRelayRequest {
	r := f.req
	r.Operation = "message"
	r.IssueRef = ""
	r.MessageID = uuid.NewString()
	r.AgentID = agent
	r.Content = content
	return r
}

func infoFlowAdmit(t *testing.T, f infoFlowTestFixture, req *infoFlowRelayRequest) infoFlowRelayResponse {
	t.Helper()
	v := infoFlowPreflight(t, f, *req)
	if v.Binding == nil || !v.CanComment {
		t.Fatalf("unexpected preflight: %+v", v)
	}
	req.ExpectedBindingID = v.Binding.BindingID
	req.ExpectedGeneration = v.Binding.Generation
	return infoFlowCall(t, f.h, *req, 200)
}

func TestInfoFlowMultiGroupRouteMatrix(t *testing.T) {
	m := infoFlowMulti(t)
	f := m.a
	for _, tc := range []struct{ name, agent, content, ref, want string }{
		{"ambiguous A", f.req.AgentID, "continue", "", ""},
		{"unique B", m.b, "continue", "", f.issue},
		{"unique D", m.d, "continue", "", m.second.issue},
		{"body key", f.req.AgentID, "继续 " + m.two.Identifier, "", m.second.issue},
		{"explicit UUID", f.req.AgentID, "continue", f.issue, f.issue},
		{"invalid key", f.req.AgentID, "continue", "MISSING-999", ""},
		{"two keys", f.req.AgentID, m.one.Identifier + " " + m.two.Identifier, "", ""},
		{"model name is not task", m.b, "使用 GPT-6-sol", "", f.issue},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := infoFlowMultiMessage(f, tc.agent, tc.content)
			r.IssueRef = tc.ref
			v := infoFlowPreflight(t, f, r)
			if tc.want == "" {
				if !v.Handled || v.Binding != nil || v.CanComment || v.Notice == "" {
					t.Fatalf("ambiguous/invalid fell through: %+v", v)
				}
				return
			}
			if v.Binding == nil || v.Binding.IssueID != tc.want || !v.CanComment {
				t.Fatalf("wrong route: %+v", v)
			}
		})
	}
	if n := infoFlowCount(t, `SELECT count(*) FROM comment WHERE issue_id IN ($1,$2)`, f.issue, m.second.issue); n != 0 {
		t.Fatal("preflight wrote comments")
	}
	unrelated := dbfx.Agent(t, "Unrelated", f.runtime, testutil.Cols{"owner_id": f.owner})
	r := infoFlowMultiMessage(f, unrelated, "continue")
	if v := infoFlowPreflight(t, f, r); !v.Handled || v.CanComment || v.Binding != nil {
		t.Fatalf("zero candidates became owner Chat: %+v", v)
	}
	r = infoFlowMultiMessage(f, f.req.AgentID, "continue")
	r.Sender = "unknown-" + uuid.NewString()
	if v := infoFlowPreflight(t, f, r); v.Binding != nil || !strings.Contains(v.Notice, "没有工作区权限") || strings.Contains(v.Notice, m.one.Identifier) {
		t.Fatalf("outsider saw choices: %+v", v)
	}
}

func TestInfoFlowMultiGroupQuotesAndPinnedRetries(t *testing.T) {
	m := infoFlowMulti(t)
	f := m.a
	in := infoFlowMultiMessage(f, m.b, "input one")
	first := infoFlowAdmit(t, f, &in)
	var task string
	dbfx.QueryRow(t, `SELECT id::text FROM agent_task_queue WHERE trigger_comment_id=$1`, first.CommentID).Scan(&task)
	worker := f
	worker.req.AgentID = m.b
	reply := infoFlowAgentReply(t, worker, f.issue, first.CommentID, task, "B answer")
	deliveries := infoFlowPoll(t, f, 20)
	if len(deliveries) != 1 || deliveries[0].Binding.BindingID != m.one.BindingID {
		t.Fatalf("wrong outbox: %+v", deliveries)
	}
	infoFlowAck(t, f, deliveries[0].ID, []string{"multi-out-" + in.MessageID}, 200)
	for _, quote := range []string{in.MessageID, "multi-out-" + in.MessageID} {
		r := infoFlowMultiMessage(f, f.req.AgentID, "关于 "+m.two.Identifier)
		r.IssueRef = m.two.Identifier
		r.ReplyToMessageID = quote
		v := infoFlowPreflight(t, f, r)
		if v.Binding == nil || v.Binding.IssueID != f.issue {
			t.Fatalf("quote must win: %+v", v)
		}
		if quote != in.MessageID {
			out := infoFlowAdmit(t, f, &r)
			if infoFlowCount(t, `SELECT count(*) FROM comment WHERE id=$1 AND issue_id=$2 AND parent_id=$3`, out.CommentID, f.issue, reply) != 1 {
				t.Fatal("outbound quote lost parent/issue")
			}
		}
	}
	r := infoFlowMultiMessage(f, m.b, "not committed")
	v := infoFlowPreflight(t, f, r)
	r.ExpectedBindingID = v.Binding.BindingID
	r.ExpectedGeneration = v.Binding.Generation
	r.IssueRef = m.two.Identifier
	infoFlowCall(t, f.h, r, 409)
	// A second robot handling the same external message cannot write to another binding.
	duplicate := in
	duplicate.AgentID = m.d
	duplicate.IssueRef = m.two.Identifier
	duplicate.ExpectedBindingID = m.two.BindingID
	duplicate.ExpectedGeneration = m.two.Generation
	infoFlowCall(t, f.h, duplicate, 409)
	if infoFlowCount(t, `SELECT count(*) FROM comment WHERE issue_id=$1 AND author_type='member'`, m.second.issue) != 0 {
		t.Fatal("retargeted old message")
	}
	// A genuinely unknown external quote may continue to an explicit selector.
	r = infoFlowMultiMessage(f, f.req.AgentID, "continue")
	r.ReplyToMessageID = "unknown-" + uuid.NewString()
	r.IssueRef = m.two.Identifier
	if v := infoFlowPreflight(t, f, r); v.Binding == nil || v.Binding.IssueID != m.second.issue {
		t.Fatalf("unknown external quote blocked explicit route: %+v", v)
	}
}

func TestInfoFlowMultiGroupIndependentUnbindAndReceipt(t *testing.T) {
	m := infoFlowMulti(t)
	f := m.a
	in := infoFlowMultiMessage(f, m.b, "original request")
	saved := infoFlowAdmit(t, f, &in)
	remove := f.req
	remove.Operation = "unbind"
	remove.MessageID = uuid.NewString()
	remove.IssueRef = ""
	if v := infoFlowCall(t, f.h, remove, 200); v.Binding != nil || !v.Handled || v.Notice == "" {
		t.Fatalf("ambiguous unbind: %+v", v)
	}
	remove.IssueRef = m.one.Identifier
	remove.ExpectedBindingID = m.one.BindingID
	remove.ExpectedGeneration = m.one.Generation
	unbound := infoFlowCall(t, f.h, remove, 200)
	if unbound.ConversationMode != "issue" || unbound.Binding == nil || unbound.Binding.IssueID != f.issue {
		t.Fatalf("unbind lost remaining mode: %+v", unbound)
	}
	if again := infoFlowCall(t, f.h, remove, 200); again.ConversationMode != "issue" || again.Binding == nil || again.Binding.Identifier != m.one.Identifier || again.CanReconcileGroup {
		t.Fatalf("cached unbind lost mode: %+v", again)
	}
	if again := infoFlowCall(t, f.h, in, 200); again.CommentID != saved.CommentID || again.Binding != nil {
		t.Fatalf("receipt adopted remaining issue: %+v", again)
	}
	if g := infoFlowReadGroups(t, f, f.owner, 200); len(g) != 0 {
		t.Fatal("unbound issue still has group link")
	}
	if g := infoFlowReadGroups(t, m.second, f.owner, 200); len(g) != 1 {
		t.Fatal("unbinding issue1 hid issue2")
	}
	continued := infoFlowMultiMessage(f, f.req.AgentID, "continue remaining")
	if got := infoFlowAdmit(t, f, &continued); got.Binding.IssueID != m.second.issue || got.CommentID == "" {
		t.Fatalf("remaining issue unavailable: %+v", got)
	}
	// An uncommitted unbind cannot silently select the remaining task.
	remove.MessageID = uuid.NewString()
	remove.IssueRef = ""
	infoFlowCall(t, f.h, remove, 409)
	if n := infoFlowCount(t, `SELECT count(*) FROM infoflow_issue_binding WHERE id=$1 AND issue_id=$2`, m.two.BindingID, m.second.issue); n != 1 {
		t.Fatal("pending unbind removed different issue")
	}
	// Rebinding creates a new binding; tombstoned receipts still win.
	f.req.MessageID = uuid.NewString()
	rebound := f.bind(t)
	if rebound.Binding.BindingID == m.one.BindingID {
		t.Fatal("tombstone reused")
	}
	if again := infoFlowCall(t, f.h, in, 200); again.CommentID != saved.CommentID || again.Binding != nil {
		t.Fatalf("old receipt adopted rebind: %+v", again)
	}
}

func TestInfoFlowMultiGroupRequiredBindingStatusAndIndependentLease(t *testing.T) {
	m := infoFlowMulti(t)
	f := m.a
	req := infoFlowGroupStatusRequest{infoFlowScope: f.req.infoFlowScope, Conversation: f.req.Conversation, Generation: m.one.Generation, LeaderAgentID: f.req.AgentID, LeaderRobotID: f.req.RobotID, LeaderPresent: true}
	r := testutil.WithHeaders(testutil.JSONRequest("POST", "/status", req), "Authorization", "Bearer "+infoFlowTestToken)
	testutil.Call(t, f.h.InfoFlowIssueGroupStatus, r).Want(400)
	req.BindingID = m.two.BindingID
	infoFlowGroupStatus(t, f, req, 409)
	req.BindingID = m.one.BindingID
	req.LeaderPresent = false
	infoFlowGroupStatus(t, f, req, 200)
	if v := infoFlowPreflight(t, f, infoFlowMultiMessage(f, m.b, "one")); v.CanComment {
		t.Fatal("pending lease admitted issue1")
	}
	if v := infoFlowPreflight(t, f, infoFlowMultiMessage(f, m.d, "two")); !v.CanComment || v.Binding.IssueID != m.second.issue {
		t.Fatal("issue1 lease overwrote issue2")
	}
	var out struct {
		Groups []infoFlowBoundGroup `json:"groups"`
	}
	r = testutil.WithHeaders(testutil.JSONRequest("POST", "/groups", f.req.infoFlowScope), "Authorization", "Bearer "+infoFlowTestToken)
	testutil.Call(t, f.h.InfoFlowIssueGroups, r).Want(200).JSON(&out)
	seen := map[string]bool{}
	for _, g := range out.Groups {
		if g.Conversation == f.req.Conversation {
			seen[g.Binding.BindingID] = true
		}
	}
	if len(seen) != 2 || !seen[m.one.BindingID] || !seen[m.two.BindingID] {
		t.Fatalf("multi-binding discovery: %+v", out)
	}
}

func TestInfoFlowOneIssueConcurrentGroupClaims(t *testing.T) {
	f := newInfoFlowFixture(t)
	start := make(chan struct{})
	done := make(chan *httptest.ResponseRecorder, 2)
	for i := range 2 {
		r := f.req
		r.Conversation.ID = fmt.Sprint(880000+i) + f.req.Conversation.ID[8:]
		r.MessageID = uuid.NewString()
		go func() {
			<-start
			w := httptest.NewRecorder()
			request := testutil.WithHeaders(testutil.JSONRequest("POST", "/relay", r), "Authorization", "Bearer "+infoFlowTestToken)
			f.h.InfoFlowIssueRelay(w, request)
			done <- w
		}()
	}
	close(start)
	success := 0
	for range 2 {
		w := <-done
		var v infoFlowRelayResponse
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil {
			t.Fatalf("claim failed: %d %s", w.Code, w.Body.String())
		}
		if v.Binding != nil {
			success++
		} else if !strings.Contains(v.Notice, "其他群") {
			t.Fatalf("claim denial: %+v", v)
		}
	}
	if success != 1 || infoFlowCount(t, `SELECT count(*) FROM infoflow_issue_binding WHERE issue_id=$1 AND conversation_kind='group'`, f.issue) != 1 {
		t.Fatal("issue claimed by two groups")
	}
}

func TestInfoFlowKnownForeignQuotesNeverChooseUniqueIssue(t *testing.T) {
	f := newInfoFlowFixture(t)
	f.bind(t)
	foreign := f
	foreign.issue = dbfx.Issue(t, "Different group issue", testutil.Cols{"creator_id": f.owner, "assignee_type": "agent", "assignee_id": f.req.AgentID})
	foreign.req.IssueRef = foreign.issue
	foreign.req.Conversation.ID = f.req.Conversation.ID[:18] + "1"
	foreign.req.MessageID = uuid.NewString()
	foreign.bind(t)
	foreignInput, _, _ := infoFlowInput(t, foreign, foreign.req.Conversation)
	private := f.req
	private.Conversation = infoFlowConversation{Kind: "private", ID: f.req.Sender}
	private.MessageID = uuid.NewString()
	infoFlowCall(t, f.h, private, 200)
	privateInput, _, _ := infoFlowInput(t, f, private.Conversation)
	for _, id := range []string{foreignInput.MessageID, privateInput.MessageID} {
		r := infoFlowMultiMessage(f, f.req.AgentID, "continue")
		r.ReplyToMessageID = id
		if v := infoFlowPreflight(t, f, r); !v.Handled || v.Binding != nil || v.CanComment || v.Notice == "" {
			t.Fatalf("foreign quote guessed unique target: %+v", v)
		}
	}
}

func TestInfoFlowUnboundGroupTaskLikeTextStaysChat(t *testing.T) {
	f := newInfoFlowFixture(t)
	for _, content := range []string{"INFE-1 进度如何", "使用 GPT-6-sol", "2026-1", "4A-1"} {
		r := infoFlowMultiMessage(f, f.req.AgentID, content)
		if v := infoFlowPreflight(t, f, r); v.Handled || v.ConversationMode != "chat" {
			t.Fatalf("unbound text hijacked: %+v", v)
		}
	}
}

func TestInfoFlowNumericPrefixesAndCompletedAssociations(t *testing.T) {
	m := infoFlowMulti(t)
	f := m.a
	var original string
	dbfx.QueryRow(t, `SELECT issue_prefix FROM workspace WHERE id=$1`, f.req.WorkspaceID).Scan(&original)
	t.Cleanup(func() { dbfx.Exec(t, `UPDATE workspace SET issue_prefix=$2 WHERE id=$1`, f.req.WorkspaceID, original) })
	var number int
	dbfx.QueryRow(t, `SELECT number FROM issue WHERE id=$1`, f.issue).Scan(&number)
	for _, prefix := range []string{"2026", "4A"} {
		dbfx.Exec(t, `UPDATE workspace SET issue_prefix=$2 WHERE id=$1`, f.req.WorkspaceID, prefix)
		r := infoFlowMultiMessage(f, f.req.AgentID, fmt.Sprintf("ı 请检查 %s-%d，使用 GPT-6-sol", strings.ToLower(prefix), number))
		if v := infoFlowPreflight(t, f, r); v.Binding == nil || v.Binding.IssueID != f.issue {
			t.Fatalf("numeric prefix route: %+v", v)
		}
	}
	// Completion is not an implicit unbind/current-task switch. Preserve the
	// ambiguity until the user names or quotes the desired task.
	dbfx.Exec(t, `UPDATE issue SET status='done' WHERE id=$1`, f.issue)
	r := infoFlowMultiMessage(f, f.req.AgentID, "进度如何")
	if v := infoFlowPreflight(t, f, r); v.Binding != nil || !strings.Contains(v.Notice, "多个任务") {
		t.Fatalf("completion silently selected another task: %+v", v)
	}
	r.IssueRef = f.issue
	if v := infoFlowPreflight(t, f, r); v.Binding == nil || v.Binding.IssueID != f.issue {
		t.Fatalf("explicit completed task unavailable: %+v", v)
	}
}

func TestInfoFlowMultiBindIdempotenceAndControlQuotePriority(t *testing.T) {
	m := infoFlowMulti(t)
	f := m.a
	in := infoFlowMultiMessage(f, m.b, "first input")
	infoFlowAdmit(t, f, &in)
	rebind := m.second.req
	rebind.MessageID = uuid.NewString()
	rebind.ReplyToMessageID = in.MessageID
	got := infoFlowCall(t, f.h, rebind, 200)
	if got.Binding == nil || got.Binding.BindingID != m.two.BindingID || got.Binding.Generation != m.two.Generation || got.Binding.Group.State != "active" {
		t.Fatalf("idempotent bind reset/selected quote: %+v", got)
	}
	lookup := infoFlowMultiMessage(f, f.req.AgentID, "/binding "+m.two.Identifier)
	lookup.Command = "binding"
	lookup.IssueRef = m.two.Identifier
	lookup.ReplyToMessageID = in.MessageID
	if v := infoFlowPreflight(t, f, lookup); v.Binding == nil || v.Binding.IssueID != m.second.issue {
		t.Fatalf("control argument lost to quote: %+v", v)
	}
	remove := lookup
	remove.Operation = "unbind"
	remove.ExpectedBindingID = m.two.BindingID
	remove.ExpectedGeneration = m.two.Generation
	if v := infoFlowCall(t, f.h, remove, 200); v.Binding == nil || v.Binding.IssueID != m.second.issue {
		t.Fatalf("removed quoted task instead of explicit task: %+v", v)
	}
	if infoFlowCount(t, `SELECT count(*) FROM infoflow_issue_binding WHERE id=$1 AND issue_id=$2`, m.one.BindingID, f.issue) != 1 {
		t.Fatal("control quote removed wrong issue")
	}
}

func TestInfoFlowMultiConcurrentExternalMessageAndPrivateAgent(t *testing.T) {
	m := infoFlowMulti(t)
	f := m.a
	messageID := uuid.NewString()
	done := make(chan *httptest.ResponseRecorder, 2)
	start := make(chan struct{})
	for _, spec := range []struct {
		agent string
		view  *infoFlowBindingView
	}{{m.b, m.one}, {m.d, m.two}} {
		r := infoFlowMultiMessage(f, spec.agent, "one external message")
		r.MessageID = messageID
		r.ExpectedBindingID = spec.view.BindingID
		r.ExpectedGeneration = spec.view.Generation
		go func() {
			<-start
			w := httptest.NewRecorder()
			request := testutil.WithHeaders(testutil.JSONRequest("POST", "/relay", r), "Authorization", "Bearer "+infoFlowTestToken)
			f.h.InfoFlowIssueRelay(w, request)
			done <- w
		}()
	}
	close(start)
	success, conflict := 0, 0
	for range 2 {
		w := <-done
		switch w.Code {
		case 200:
			var v infoFlowRelayResponse
			if json.Unmarshal(w.Body.Bytes(), &v) != nil || v.CommentID == "" {
				t.Fatalf("accepted request lacks comment: %s", w.Body.String())
			}
			success++
		case 409:
			conflict++
		default:
			t.Fatalf("duplicate status: %d %s", w.Code, w.Body.String())
		}
	}
	if success != 1 || conflict != 1 || infoFlowCount(t, `SELECT count(*) FROM infoflow_issue_source WHERE message_id=$1`, messageID) != 1 || infoFlowCount(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id IN ($1,$2)`, f.issue, m.second.issue) != 1 {
		t.Fatal("one external input started two tasks")
	}
	var email string
	dbfx.QueryRow(t, `SELECT email FROM "user" WHERE id=$1`, f.user).Scan(&email)
	r := infoFlowMultiMessage(f, m.d, "member comment only")
	r.Sender = strings.Split(email, "@")[0]
	before := infoFlowCount(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id IN ($1,$2)`, f.issue, m.second.issue)
	v := infoFlowAdmit(t, f, &r)
	if v.CommentID == "" || v.Binding.IssueID != m.second.issue || !strings.Contains(v.Notice, "没有智能体权限") || infoFlowCount(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id IN ($1,$2)`, f.issue, m.second.issue) != before {
		t.Fatalf("private agent permission lost in routing: %+v", v)
	}
	if infoFlowCount(t, `SELECT count(*) FROM comment WHERE id=$1 AND author_id=$2 AND issue_id=$3`, v.CommentID, f.user, m.second.issue) != 1 {
		t.Fatal("comment used owner identity")
	}
}

func TestInfoFlowSameGroupForeignWorkspaceQuoteIsNotUniqueFallback(t *testing.T) {
	f := newInfoFlowFixture(t)
	f.bind(t)
	ws := dbfx.Workspace(t, "Foreign quote workspace", "quote-"+uuid.NewString())
	dbfx.Member(t, ws, f.owner, "owner")
	otherAgent := dbfx.Agent(t, "Foreign quote agent", f.runtime, testutil.Cols{"workspace_id": ws, "owner_id": f.owner})
	otherIssue := dbfx.Issue(t, "Foreign quote issue", testutil.Cols{"workspace_id": ws, "assignee_type": "agent", "assignee_id": otherAgent})
	other := f.req
	other.WorkspaceID = ws
	other.AgentID = otherAgent
	other.IssueRef = otherIssue
	other.MessageID = uuid.NewString()
	v := infoFlowCall(t, f.h, other, 200)
	if v.Binding == nil {
		t.Fatalf("foreign binding failed: %+v", v)
	}
	// A successfully stored binding command is also provenance in that group;
	// another workspace must not use it to select its own unique task.
	r := infoFlowMultiMessage(f, f.req.AgentID, "continue")
	r.ReplyToMessageID = other.MessageID
	if got := infoFlowPreflight(t, f, r); got.Binding != nil || got.CanComment || !got.Handled || got.Notice == "" {
		t.Fatalf("foreign workspace quote guessed target: %+v", got)
	}
}

func TestInfoFlowMultiOutboxKeepsBindingsAndAuthorsSeparate(t *testing.T) {
	m := infoFlowMulti(t)
	f := m.a
	for _, spec := range []struct{ agent, issue, answer string }{{m.b, f.issue, "B answer"}, {m.d, m.second.issue, "D answer"}} {
		r := infoFlowMultiMessage(f, spec.agent, "trigger "+spec.answer)
		v := infoFlowAdmit(t, f, &r)
		var task string
		dbfx.QueryRow(t, `SELECT id::text FROM agent_task_queue WHERE trigger_comment_id=$1`, v.CommentID).Scan(&task)
		worker := f
		worker.req.AgentID = spec.agent
		infoFlowAgentReply(t, worker, spec.issue, v.CommentID, task, spec.answer)
	}
	deliveries := infoFlowPoll(t, f, 20)
	if len(deliveries) != 2 {
		t.Fatalf("missing multi-task output: %+v", deliveries)
	}
	for _, d := range deliveries {
		if d.Conversation != f.req.Conversation || d.Binding == nil {
			t.Fatalf("wrong destination: %+v", d)
		}
		switch d.AuthorAgentID {
		case m.b:
			if d.IssueID != f.issue || d.Binding.BindingID != m.one.BindingID || d.Binding.Identifier != m.one.Identifier || d.Content != "B answer" {
				t.Fatalf("B leak: %+v", d)
			}
		case m.d:
			if d.IssueID != m.second.issue || d.Binding.BindingID != m.two.BindingID || d.Binding.Identifier != m.two.Identifier || d.Content != "D answer" {
				t.Fatalf("D leak: %+v", d)
			}
		default:
			t.Fatalf("unknown author: %+v", d)
		}
		infoFlowAck(t, f, d.ID, []string{uuid.NewString()}, 200)
	}
	if out := infoFlowPoll(t, f, 20); len(out) != 0 {
		t.Fatal("ack looked up wrong binding in same group")
	}
}
