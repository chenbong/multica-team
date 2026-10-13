package handler

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
)

func infoFlowGroupStatus(t *testing.T, f infoFlowTestFixture, req infoFlowGroupStatusRequest, status int) infoFlowRelayResponse {
	t.Helper()
	var out infoFlowRelayResponse
	r := testutil.WithHeaders(testutil.JSONRequest("POST", "/group-status", req), "Authorization", "Bearer "+infoFlowTestToken)
	testutil.Call(t, f.h.InfoFlowIssueGroupStatus, r).Want(status).JSON(&out)
	return out
}

func TestInfoFlowSanitizeNestedMentions(t *testing.T) {
	for _, kind := range []string{"agent", "squad", "member", "all", "issue"} {
		id := uuid.NewString()
		if kind == "all" {
			id = "all"
		}
		input := fmt.Sprintf("[@target](mention://%s/%s)", kind, id)
		for depth := range 128 {
			input = fmt.Sprintf("[@outer %s](mention://%s/%s)", input, kind, id)
			if m := util.ParseMentions(infoFlowSanitizeMentions(input)); len(m) != 0 {
				t.Fatalf("%s depth%d still invokes: %+v", kind, depth, m)
			}
		}
	}
}

func TestInfoFlowMultipleGroupsConcurrentComments(t *testing.T) {
	f := newInfoFlowFixture(t)
	f.bind(t)
	second := f.req
	second.Conversation.ID = fmt.Sprint(time.Now().UnixNano())
	second.MessageID = uuid.NewString()
	b := infoFlowCall(t, f.h, second, 200)
	infoFlowActivate(t, f, second, b.Binding.Generation)
	done := make(chan *httptest.ResponseRecorder, 2)
	start := make(chan struct{})
	for _, req := range []infoFlowRelayRequest{f.req, second} {
		req.Operation = "message"
		req.MessageID = uuid.NewString()
		req.Content = "concurrent group input"
		go func() {
			<-start
			r := testutil.WithHeaders(testutil.JSONRequest("POST", "/relay", req), "Authorization", "Bearer "+infoFlowTestToken)
			w := httptest.NewRecorder()
			f.h.InfoFlowIssueRelay(w, r)
			done <- w
		}()
	}
	close(start)
	for range 2 {
		select {
		case w := <-done:
			var out infoFlowRelayResponse
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.CommentID == "" {
				t.Fatalf("concurrent group relay failed: %d %s", w.Code, w.Body.String())
			}
		case <-time.After(10 * time.Second):
			t.Fatal("concurrent groups deadlocked")
		}
	}
	if n := infoFlowCount(t, `SELECT count(*) FROM comment WHERE issue_id=$1 AND author_type='member'`, f.issue); n != 2 {
		t.Fatalf("lost group input: %d", n)
	}
}

func TestInfoFlowNestedExternalMentionCannotInvokeOtherAgent(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		t.Run(fmt.Sprint(allowed), func(t *testing.T) {
			f := newInfoFlowFixture(t)
			f.bind(t)
			c := dbfx.Agent(t, "Callable external agent C", f.runtime, testutil.Cols{"owner_id": f.user})
			squad := dbfx.Insert(t, "squad", testutil.Cols{"workspace_id": f.req.WorkspaceID, "name": "Outside squad", "leader_id": c, "creator_id": f.user})
			if allowed {
				dbfx.Exec(t, `UPDATE agent SET permission_mode='public_to' WHERE id=$1`, f.req.AgentID)
				dbfx.Insert(t, "agent_invocation_target", testutil.Cols{"agent_id": f.req.AgentID, "target_type": "member", "target_id": f.user, "created_by": f.owner})
			}
			var email string
			dbfx.QueryRow(t, `SELECT email FROM "user" WHERE id=$1`, f.user).Scan(&email)
			req := f.req
			req.Operation = "message"
			req.MessageID = uuid.NewString()
			req.Sender = strings.Split(email, "@")[0]
			req.Content = fmt.Sprintf("[@outer [@C](mention://agent/%s)](mention://agent/%s) [@outer [@squad](mention://squad/%s)](mention://squad/%s)", c, c, squad, squad)
			got := infoFlowCall(t, f.h, req, 200)
			if got.CommentID == "" {
				t.Fatalf("comment missing %+v", got)
			}
			var content string
			dbfx.QueryRow(t, `SELECT content FROM comment WHERE id=$1`, got.CommentID).Scan(&content)
			mentions := util.ParseMentions(content)
			if len(mentions) != 1 || mentions[0].ID != f.req.AgentID {
				t.Fatalf("external target survived: %+v", mentions)
			}
			want := 0
			if allowed {
				want = 1
			}
			if n := infoFlowCount(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1`, f.issue); n != want {
				t.Fatalf("wanted %d task only for addressed agent, got%d", want, n)
			}
			if n := infoFlowCount(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1 AND agent_id<>$2`, f.issue, f.req.AgentID); n != 0 {
				t.Fatal("nested mention invoked other agent")
			}
		})
	}
}

func infoFlowActivate(t *testing.T, f infoFlowTestFixture, req infoFlowRelayRequest, generation string) infoFlowRelayResponse {
	t.Helper()
	out := infoFlowGroupStatus(t, f, infoFlowGroupStatusRequest{infoFlowScope: req.infoFlowScope, Conversation: req.Conversation, Generation: generation, LeaderAgentID: req.AgentID, LeaderRobotID: req.RobotID, LeaderPresent: true}, 200)
	if out.Binding == nil || out.Binding.Group == nil || out.Binding.Group.State != "active" {
		t.Fatalf("activate: %+v", out)
	}
	return out
}

func infoFlowReadGroups(t *testing.T, f infoFlowTestFixture, user string, status int) []map[string]string {
	t.Helper()
	var out struct {
		Groups []map[string]string `json:"groups"`
	}
	r := testutil.WithURLParams(testutil.WithHeaders(testutil.JSONRequest("GET", "/groups", nil), "X-User-ID", user, "X-Workspace-ID", f.req.WorkspaceID), "id", f.issue)
	c := testutil.Call(t, f.h.GetIssueInfoFlowGroups, r).Want(status)
	if status == 200 {
		c.JSON(&out)
	}
	return out.Groups
}

func TestInfoFlowGroupPendingLeaseAndLink(t *testing.T) {
	f := newInfoFlowFixture(t)
	f.req.Conversation.ID = "9223372036854775807"
	bound := infoFlowCall(t, f.h, f.req, 200)
	if bound.Binding == nil || bound.Binding.BindingID == "" || bound.Binding.Group == nil || bound.Binding.Group.State != "pending" || bound.CanComment || !bound.CanReconcileGroup {
		t.Fatalf("pending contract: %+v", bound)
	}
	message := f.req
	message.Operation = "message"
	message.MessageID = uuid.NewString()
	message.Content = "pending input"
	if got := infoFlowCall(t, f.h, message, 200); got.CanComment || got.CommentID != "" || got.Notice == "" {
		t.Fatalf("pending admitted input: %+v", got)
	}
	if g := infoFlowReadGroups(t, f, f.owner, 200); len(g) != 0 {
		t.Fatal("pending group leaked to issue UI")
	}
	infoFlowActivate(t, f, f.req, bound.Binding.Generation)
	groups := infoFlowReadGroups(t, f, f.owner, 200)
	if len(groups) != 1 || groups[0]["group_id"] != f.req.Conversation.ID {
		t.Fatalf("groups: %+v", groups)
	}
	u, err := url.Parse(groups[0]["url"])
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(u.Query().Get("data"))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		APIName string
		Version int
		Data    struct {
			ID   string
			Type string
		}
	}
	if json.Unmarshal(raw, &payload) != nil || payload.APIName != "BdHiJs.service.hi.open" || payload.Version != 65 || payload.Data.ID != f.req.Conversation.ID || payload.Data.Type != "2" {
		t.Fatalf("incorrect AppLink: %s", raw)
	}
	if got := infoFlowCall(t, f.h, f.req, 200); got.Binding.Group.State != "active" || !strings.Contains(got.Notice, "已启用") {
		t.Fatalf("bind replay did not refresh: %+v", got)
	}
	infoFlowReadGroups(t, f, uuid.NewString(), 404)
	_, input, task := infoFlowInput(t, f, f.req.Conversation)
	infoFlowAgentReply(t, f, f.issue, input.CommentID, task, "reply")
	d := infoFlowPoll(t, f, 20)
	if len(d) != 1 {
		t.Fatalf("delivery: %+v", d)
	}
	dbfx.Exec(t, `UPDATE infoflow_issue_binding SET leader_verified_at=clock_timestamp()-interval '46 seconds' WHERE id=$1`, bound.Binding.BindingID)
	if g := infoFlowReadGroups(t, f, f.owner, 200); len(g) != 0 {
		t.Fatal("expired link exposed")
	}
	if got := infoFlowCall(t, f.h, message, 200); got.CanComment || got.CommentID != "" {
		t.Fatal("expired input accepted")
	}
	if got := infoFlowPoll(t, f, 20); len(got) != 0 {
		t.Fatal("expired delivery polled")
	}
	infoFlowAck(t, f, d[0].ID, nil, 409)
	if got := infoFlowCall(t, f.h, f.req, 200); got.Binding.Group.State != "pending" || strings.Contains(got.Notice, "已启用") {
		t.Fatalf("cached active lie: %+v", got)
	}
	infoFlowActivate(t, f, f.req, bound.Binding.Generation)
	status := infoFlowGroupStatusRequest{infoFlowScope: f.req.infoFlowScope, Conversation: f.req.Conversation, Generation: bound.Binding.Generation, LeaderAgentID: f.req.AgentID, LeaderRobotID: f.req.RobotID, LeaderPresent: false}
	if got := infoFlowGroupStatus(t, f, status, 200); got.Binding.Group.State != "pending" {
		t.Fatal("leader loss did not suspend")
	}
	status.LeaderPresent = true
	status.Generation = uuid.NewString()
	infoFlowGroupStatus(t, f, status, 409)
	status.Generation = bound.Binding.Generation
	status.LeaderRobotID = "not-scope-robot"
	infoFlowGroupStatus(t, f, status, 409)
	t.Setenv("INFOFLOW_ISSUE_RELAY_TOKEN", "")
	if g := infoFlowReadGroups(t, f, f.owner, 200); len(g) != 0 {
		t.Fatal("disabled API did not return empty")
	}
}

func TestInfoFlowGroupTopologyAndParticipantRouting(t *testing.T) {
	f := newInfoFlowFixture(t)
	member := dbfx.Agent(t, "Member B", f.runtime, testutil.Cols{"owner_id": f.owner})
	unrelated := dbfx.Agent(t, "Unrelated", f.runtime, testutil.Cols{"owner_id": f.owner})
	squad := dbfx.Insert(t, "squad", testutil.Cols{"workspace_id": f.req.WorkspaceID, "name": "Relay squad", "leader_id": f.req.AgentID, "creator_id": f.owner})
	dbfx.Insert(t, "squad_member", testutil.Cols{"squad_id": squad, "member_type": "agent", "member_id": member, "role": "member"})
	dbfx.Exec(t, `UPDATE issue SET assignee_type='squad',assignee_id=$2 WHERE id=$1`, f.issue, squad)
	bound := f.bind(t)
	if len(bound.Binding.Group.AgentIDs) != 2 {
		t.Fatalf("roster: %+v", bound.Binding.Group)
	}
	other := f
	other.req.AgentID = member
	other.req.RobotID = "member-robot"
	lookup := other.req
	lookup.Operation = "binding"
	lookup.MessageID = ""
	if got := infoFlowCall(t, f.h, lookup, 200); got.Binding.BindingID != bound.Binding.BindingID || !got.CanComment {
		t.Fatalf("robot-independent lookup: %+v", got)
	}
	_, input, task := infoFlowInput(t, f, f.req.Conversation)
	infoFlowAgentReply(t, f, f.issue, input.CommentID, task, "Leader answer")
	memberReply := dbfx.Comment(t, f.issue, "Member answer", testutil.Cols{"author_type": "agent", "author_id": member, "parent_id": input.CommentID})
	dbfx.Comment(t, f.issue, "unrelated secret", testutil.Cols{"author_type": "agent", "author_id": unrelated, "parent_id": input.CommentID})
	if got := infoFlowPoll(t, other, 20); len(got) != 0 {
		t.Fatal("noncoordinator polled")
	}
	seen := map[string]bool{}
	for range 2 {
		d := infoFlowPoll(t, f, 20)
		if len(d) != 1 {
			t.Fatalf("poll: %+v", d)
		}
		seen[d[0].AuthorAgentID] = true
		if d[0].CommentID == memberReply && (d[0].AuthorName != "Member B" || d[0].Content != "Member answer") {
			t.Fatalf("author/body: %+v", d[0])
		}
		infoFlowAck(t, other, d[0].ID, nil, 404)
		externalIDs := []string{}
		if d[0].AuthorAgentID == f.req.AgentID {
			externalIDs = []string{"leader-output"}
		}
		infoFlowAck(t, f, d[0].ID, externalIDs, 200)
	}
	if !seen[f.req.AgentID] || !seen[member] {
		t.Fatalf("missing participant: %+v", seen)
	}
	if d := infoFlowPoll(t, f, 20); len(d) != 0 {
		t.Fatal("unrelated answer leaked")
	}
	req := other.req
	req.Operation = "message"
	req.MessageID = uuid.NewString()
	req.Content = "B please work"
	if got := infoFlowCall(t, f.h, req, 200); got.CommentID == "" {
		t.Fatalf("member route denied: %+v", got)
	}
	// Workspace admins do not inherit invocation rights. Even quoting an
	// accessible leader must not wake it when the addressed member is private.
	dbfx.Exec(t, `UPDATE agent SET permission_mode='public_to' WHERE id=$1`, f.req.AgentID)
	dbfx.Insert(t, "agent_invocation_target", testutil.Cols{"agent_id": f.req.AgentID, "target_type": "member", "target_id": f.user, "created_by": f.owner})
	var email string
	dbfx.QueryRow(t, `SELECT email FROM "user" WHERE id=$1`, f.user).Scan(&email)
	denied := req
	denied.Sender = strings.Split(email, "@")[0]
	denied.MessageID = uuid.NewString()
	denied.ReplyToMessageID = "leader-output"
	if got := infoFlowCall(t, f.h, denied, 200); got.CommentID == "" || !strings.Contains(got.Notice, "仅新增评论") {
		t.Fatalf("squad permission: %+v", got)
	}
	if n := infoFlowCount(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1 AND originator_user_id=$2`, f.issue, f.user); n != 0 {
		t.Fatal("no-invoke comment woke another participant")
	}
	dbfx.Exec(t, `DELETE FROM member WHERE id=$1`, f.member)
	denied.MessageID = uuid.NewString()
	if got := infoFlowCall(t, f.h, denied, 200); got.CommentID != "" || got.CanComment || got.CanReconcileGroup || !strings.Contains(got.Notice, "没有工作区权限") {
		t.Fatalf("squad workspace denial: %+v", got)
	}
	req.AgentID = unrelated
	req.MessageID = uuid.NewString()
	if got := infoFlowCall(t, f.h, req, 200); got.CommentID != "" || got.CanComment || !strings.Contains(got.Notice, "不属于") {
		t.Fatalf("unrelated route accepted: %+v", got)
	}
	// New leader requires fresh presence before either old/new coordinator runs.
	dbfx.Exec(t, `UPDATE squad SET leader_id=$2 WHERE id=$1`, squad, member)
	req = f.req
	req.Operation = "binding"
	req.MessageID = ""
	if got := infoFlowCall(t, f.h, req, 200); got.Binding.Group.State != "pending" || got.CanComment || got.Binding.Group.LeaderAgentID != member {
		t.Fatalf("stale leader: %+v", got)
	}
	if d := infoFlowPoll(t, f, 20); len(d) != 0 {
		t.Fatal("old coordinator kept polling")
	}
	status := infoFlowGroupStatusRequest{infoFlowScope: f.req.infoFlowScope, Conversation: f.req.Conversation, Generation: bound.Binding.Generation, LeaderAgentID: f.req.AgentID, LeaderRobotID: f.req.RobotID, LeaderPresent: true}
	infoFlowGroupStatus(t, f, status, 409)
	infoFlowActivate(t, other, other.req, bound.Binding.Generation)
	// Single assignment is a different topology even when its leader is equal.
	dbfx.Exec(t, `UPDATE issue SET assignee_type='agent',assignee_id=$2 WHERE id=$1`, f.issue, member)
	if got := infoFlowCall(t, f.h, req, 200); got.Binding.Group.State != "pending" {
		t.Fatal("squad -> single not fenced")
	}
	infoFlowActivate(t, other, other.req, bound.Binding.Generation)
	if got := infoFlowCall(t, f.h, req, 200); got.CanComment {
		t.Fatal("old squad member still admitted after single assignment")
	}
}

func TestInfoFlowGroupRejectHumanUnassignedAndInvalidIDs(t *testing.T) {
	for _, kind := range []string{"member", ""} {
		t.Run(kind, func(t *testing.T) {
			f := newInfoFlowFixture(t)
			if kind == "" {
				dbfx.Exec(t, `UPDATE issue SET assignee_type=NULL,assignee_id=NULL WHERE id=$1`, f.issue)
			} else {
				dbfx.Exec(t, `UPDATE issue SET assignee_type='member',assignee_id=$2 WHERE id=$1`, f.issue, f.owner)
			}
			got := infoFlowCall(t, f.h, f.req, 200)
			if got.Binding != nil || got.CanComment || got.CanReconcileGroup || !strings.Contains(got.Notice, "分配") {
				t.Fatalf("invalid assignee accepted: %+v", got)
			}
			if infoFlowCount(t, `SELECT count(*) FROM infoflow_issue_binding WHERE robot_id=$1`, f.req.RobotID) != 0 {
				t.Fatal("rejected bind persisted")
			}
		})
	}
	for _, id := range []string{"0", "01", "-1", "1.2", "9223372036854775808", "a", "1\n", "+1"} {
		t.Run(id, func(t *testing.T) {
			f := newInfoFlowFixture(t)
			f.req.Conversation.ID = id
			infoFlowCall(t, f.h, f.req, 400)
		})
	}
}

func TestInfoFlowGroupGlobalUniqueAndTrustedDiscovery(t *testing.T) {
	f := newInfoFlowFixture(t)
	bound := f.bind(t)
	ws := dbfx.Workspace(t, "Other group workspace", "if-group-"+uuid.NewString())
	dbfx.Member(t, ws, f.owner, "owner")
	otherAgent := dbfx.Agent(t, "Other scope", f.runtime, testutil.Cols{"workspace_id": ws, "owner_id": f.owner})
	otherIssue := dbfx.Issue(t, "Other issue", testutil.Cols{"workspace_id": ws, "assignee_type": "agent", "assignee_id": otherAgent})
	req := f.req
	req.WorkspaceID = ws
	req.AgentID = otherAgent
	req.RobotID = "other-workspace"
	req.IssueRef = otherIssue
	req.MessageID = uuid.NewString()
	if got := infoFlowCall(t, f.h, req, 200); got.Binding != nil || got.CanReconcileGroup || !strings.Contains(got.Notice, "其他工作区") {
		t.Fatalf("cross-workspace bind: %+v", got)
	}
	var out struct {
		Groups []infoFlowBoundGroup `json:"groups"`
	}
	r := testutil.WithHeaders(testutil.JSONRequest("POST", "/issue-groups", f.req.infoFlowScope), "Authorization", "Bearer "+infoFlowTestToken)
	testutil.Call(t, f.h.InfoFlowIssueGroups, r).Want(200).JSON(&out)
	found := false
	for _, g := range out.Groups {
		if g.Binding.BindingID == bound.Binding.BindingID {
			found = true
		}
	}
	if !found {
		t.Fatal("discovery omitted group")
	}
	req = f.req
	req.Operation = "unbind"
	req.MessageID = uuid.NewString()
	infoFlowCall(t, f.h, req, 200)
	req.WorkspaceID = ws
	req.AgentID = otherAgent
	req.RobotID = f.req.RobotID
	req.IssueRef = otherIssue
	req.MessageID = uuid.NewString()
	req.Operation = "bind"
	if got := infoFlowCall(t, f.h, req, 200); got.Binding == nil || got.Binding.IssueID != otherIssue {
		t.Fatalf("unbound cross-workspace transfer rejected: %+v", got)
	}
}
