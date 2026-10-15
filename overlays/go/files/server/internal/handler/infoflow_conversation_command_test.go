package handler

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestInfoFlowAmbiguityIncludesScopedTaskChoices(t *testing.T) {
	m := infoFlowMulti(t)
	f := m.a
	other := f
	other.issue = dbfx.Issue(t, "Other group", testutil.Cols{"creator_id": f.owner, "assignee_type": "agent", "assignee_id": f.req.AgentID})
	other.req.IssueRef = other.issue
	other.req.Conversation.ID = "9223372036854775801"
	other.req.MessageID = uuid.NewString()
	other.bind(t)
	r := infoFlowMultiMessage(f, f.req.AgentID, "继续")
	v := infoFlowPreflight(t, f, r)
	if v.NoticeKind != "task_choices" || len(v.TaskChoices) != 2 || v.Binding != nil {
		t.Fatalf("missing candidate links: %+v", v)
	}
	seen := map[string]bool{}
	for _, c := range v.TaskChoices {
		if c.Identifier == "" || c.WorkspaceSlug == "" || c.IssueID == other.issue {
			t.Fatalf("invalid or foreign choice: %+v", c)
		}
		seen[c.IssueID] = true
	}
	if !seen[f.issue] || !seen[m.second.issue] {
		t.Fatalf("wrong choices: %+v", v.TaskChoices)
	}
	r.AgentID = m.b
	if v := infoFlowPreflight(t, f, r); len(v.TaskChoices) != 0 || v.Binding == nil || v.Binding.IssueID != f.issue {
		t.Fatalf("unrelated agent's issues leaked: %+v", v)
	}
	r.AgentID, r.Sender = f.req.AgentID, "unknown-"+uuid.NewString()
	if v := infoFlowPreflight(t, f, r); len(v.TaskChoices) != 0 || v.NoticeKind != "" || !strings.Contains(v.Notice, "没有工作区权限") {
		t.Fatalf("outsider saw choices: %+v", v)
	}
}

func TestInfoFlowConversationCommandsNeverSelectOrInvoke(t *testing.T) {
	f := newInfoFlowFixture(t)
	for count := 0; count <= 2; count++ {
		if count > 0 {
			bound := f
			if count == 2 {
				bound.issue = dbfx.Issue(t, "Another issue", testutil.Cols{"creator_id": f.owner, "assignee_type": "agent", "assignee_id": f.req.AgentID})
				bound.req.IssueRef = bound.issue
				bound.req.MessageID = uuid.NewString()
			}
			bound.bind(t)
		}
		for _, command := range []string{"help", "status"} {
			r := infoFlowMultiMessage(f, f.req.AgentID, "")
			r.Command = command
			r.ReplyToMessageID = "unrelated-quote"
			v := infoFlowPreflight(t, f, r)
			if !v.Handled || v.NoticeKind != command || v.Binding != nil || v.CanComment {
				t.Fatalf("%s with %d bindings selected a task: %+v", command, count, v)
			}
			if command == "status" && len(v.TaskChoices) != count {
				t.Fatalf("wrong overview: %+v", v)
			}
			if command == "help" && len(v.TaskChoices) != 0 {
				t.Fatal("help contains task information")
			}
		}
	}
	if n := infoFlowCount(t, `SELECT count(*) FROM comment WHERE issue_id=$1`, f.issue); n != 0 {
		t.Fatal("commands wrote comments")
	}
	if n := infoFlowCount(t, `SELECT count(*) FROM agent_task_queue WHERE agent_id=$1`, f.req.AgentID); n != 0 {
		t.Fatal("commands invoked agent")
	}
	for _, command := range []string{"help", "status"} {
		r := infoFlowMultiMessage(f, f.req.AgentID, "")
		r.Command, r.Sender = command, "unknown-"+uuid.NewString()
		if v := infoFlowPreflight(t, f, r); v.NoticeKind != "" || len(v.TaskChoices) != 0 || !strings.Contains(v.Notice, "没有工作区权限") {
			t.Fatalf("command skipped identity gate: %+v", v)
		}
	}
}

func TestInfoFlowStatusGroupWideAndPrivateAgentWide(t *testing.T) {
	m := infoFlowMulti(t)
	f := m.a
	// A separate agent's issue bound to the same group belongs in group status.
	other := f
	other.req.AgentID = m.b
	other.issue = dbfx.Issue(t, "B only", testutil.Cols{"creator_id": f.owner, "assignee_type": "agent", "assignee_id": m.b})
	other.req.IssueRef, other.req.MessageID = other.issue, uuid.NewString()
	other.bind(t)
	unbound := dbfx.Issue(t, "A unbound", testutil.Cols{"creator_id": f.owner, "assignee_type": "agent", "assignee_id": f.req.AgentID, "status": "in_review"})
	private := f
	private.issue = dbfx.Issue(t, "A private", testutil.Cols{"creator_id": f.owner, "assignee_type": "agent", "assignee_id": f.req.AgentID})
	private.req.Conversation = infoFlowConversation{Kind: "private", ID: f.req.Sender}
	private.req.IssueRef, private.req.MessageID = private.issue, uuid.NewString()
	private.bind(t)
	for _, kind := range []string{"group", "private"} {
		r := infoFlowMultiMessage(f, f.req.AgentID, "")
		r.Command = "status"
		want := map[string]string{f.issue: "group", m.second.issue: "group", other.issue: "group"}
		if kind == "private" {
			r.Conversation = private.req.Conversation
			want = map[string]string{f.issue: "group", m.second.issue: "group", unbound: "", private.issue: "private"}
		}
		v := infoFlowPreflight(t, f, r)
		if v.NoticeKind != "status" || len(v.TaskChoices) != len(want) {
			t.Fatalf("%s wrong status scope: %+v", kind, v)
		}
		for _, choice := range v.TaskChoices {
			expectedKind, ok := want[choice.IssueID]
			if !ok || choice.BindingKinds != expectedKind || choice.Identifier == "" || choice.Status == "" {
				t.Fatalf("%s wrong choice: %+v", kind, choice)
			}
			if choice.IssueID == unbound && choice.Status != "in_review" {
				t.Fatalf("status lost: %+v", choice)
			}
		}
	}
}

func TestInfoFlowStatusWorkspacePermissionAndPages(t *testing.T) {
	f := newInfoFlowFixture(t)
	f.bind(t)
	ws := dbfx.Workspace(t, "Status foreign workspace", "status-"+uuid.NewString())
	foreign := dbfx.Issue(t, "Foreign status issue", testutil.Cols{"workspace_id": ws, "creator_id": f.owner, "assignee_type": "agent", "assignee_id": f.req.AgentID})
	dbfx.Insert(t, "infoflow_issue_binding", testutil.Cols{"id": uuid.NewString(), "robot_id": f.req.RobotID, "workspace_id": ws, "agent_id": f.req.AgentID, "conversation_kind": "group", "conversation_id": f.req.Conversation.ID, "issue_id": foreign, "generation": uuid.NewString()})
	r := infoFlowMultiMessage(f, f.req.AgentID, "")
	r.Command, r.StatusPage = "status", 2
	v := infoFlowPreflight(t, f, r)
	if v.StatusPage != 2 || len(v.TaskChoices) != 1 || v.TaskChoices[0].IssueID != f.issue {
		t.Fatalf("foreign workspace leaked or page lost: %+v", v)
	}
	dbfx.Member(t, ws, f.owner, "member")
	if v := infoFlowPreflight(t, f, r); len(v.TaskChoices) != 2 {
		t.Fatalf("authorized group-wide task missing: %+v", v)
	}
	r.Conversation = infoFlowConversation{Kind: "private", ID: r.Sender}
	if v := infoFlowPreflight(t, f, r); len(v.TaskChoices) != 1 || v.TaskChoices[0].IssueID != f.issue {
		t.Fatalf("private status crossed agent workspace: %+v", v)
	}
	for _, page := range []int{-1, 100001} {
		r.StatusPage = page
		r.Operation = "binding"
		infoFlowCall(t, f.h, r, 400)
	}
}

func TestInfoFlowExplicitTaskStatusRemainsReadOnly(t *testing.T) {
	m := infoFlowMulti(t)
	f := m.a
	dbfx.Exec(t, `UPDATE issue SET status='in_progress' WHERE id=$1`, m.second.issue)
	r := infoFlowMultiMessage(f, f.req.AgentID, "")
	r.Command, r.IssueRef = "task_status", m.two.Identifier
	r.ReplyToMessageID = "unrelated-quote"
	v := infoFlowPreflight(t, f, r)
	if v.Binding == nil || v.Binding.IssueID != m.second.issue || v.Notice != m.two.Identifier+"：in_progress" {
		t.Fatalf("explicit status selected wrong issue: %+v", v)
	}
	if n := infoFlowCount(t, `SELECT count(*) FROM comment WHERE issue_id IN ($1,$2)`, f.issue, m.second.issue); n != 0 {
		t.Fatal("status created comments")
	}
	if n := infoFlowCount(t, `SELECT count(*) FROM agent_task_queue WHERE agent_id=$1`, f.req.AgentID); n != 0 {
		t.Fatal("status invoked agent")
	}
}

func TestInfoFlowStatusTitlesAndResponsibleAgents(t *testing.T) {
	m := infoFlowMulti(t)
	f := m.a
	dbfx.Exec(t, `UPDATE agent SET name='Leader A' WHERE id=$1`, f.req.AgentID)
	dbfx.Exec(t, `UPDATE issue SET title='PiCoKV reproduction' WHERE id=$1`, f.issue)
	var squad string
	dbfx.QueryRow(t, `SELECT assignee_id::text FROM issue WHERE id=$1`, f.issue).Scan(&squad)
	// A leader may also have a membership row; the display must not duplicate it.
	dbfx.Insert(t, "squad_member", testutil.Cols{"squad_id": squad, "member_type": "agent", "member_id": f.req.AgentID, "role": "member"})
	ws := dbfx.Workspace(t, "Foreign agent roster", "roster-"+uuid.NewString())
	foreign := dbfx.Agent(t, "Foreign must not appear", f.runtime, testutil.Cols{"workspace_id": ws, "owner_id": f.owner})
	dbfx.Insert(t, "squad_member", testutil.Cols{"squad_id": squad, "member_type": "agent", "member_id": foreign, "role": "member"})
	r := infoFlowMultiMessage(f, f.req.AgentID, "")
	r.Command = "status"
	v := infoFlowPreflight(t, f, r)
	for _, choice := range v.TaskChoices {
		want := []string{"Leader A", "D"}
		if choice.IssueID == f.issue {
			want = []string{"Leader A", "B"}
			if choice.Title != "PiCoKV reproduction" {
				t.Fatalf("task title missing: %+v", choice)
			}
		}
		if !slices.Equal(choice.AgentNames, want) {
			t.Fatalf("wrong current assignment roster: %+v", choice)
		}
	}
	// Current assignment wins over the robot/roster saved at binding time.
	dbfx.Exec(t, `UPDATE issue SET assignee_type='agent',assignee_id=$2 WHERE id=$1`, f.issue, m.b)
	v = infoFlowPreflight(t, f, r)
	for _, choice := range v.TaskChoices {
		if choice.IssueID == f.issue && !slices.Equal(choice.AgentNames, []string{"B"}) {
			t.Fatalf("stale squad names after reassignment: %+v", choice)
		}
	}
}

func TestInfoFlowStatusDoesNotInventAgentOwners(t *testing.T) {
	m := infoFlowMulti(t)
	f := m.a
	dbfx.Exec(t, `UPDATE agent SET archived_at=now() WHERE id=$1`, m.b)
	dbfx.Exec(t, `UPDATE issue SET assignee_type='member',assignee_id=$2 WHERE id=$1`, m.second.issue, f.member)
	r := infoFlowMultiMessage(f, f.req.AgentID, "")
	r.Command = "status"
	v := infoFlowPreflight(t, f, r)
	if len(v.TaskChoices) != 2 {
		t.Fatalf("group-wide task overview changed: %+v", v)
	}
	for _, choice := range v.TaskChoices {
		if choice.Title == "" {
			t.Fatalf("missing title: %+v", choice)
		}
		if choice.IssueID == f.issue && len(choice.AgentNames) != 1 {
			t.Fatalf("archived member displayed: %+v", choice)
		}
		if choice.IssueID == m.second.issue && len(choice.AgentNames) != 0 {
			t.Fatalf("human-assigned task invented agents: %+v", choice)
		}
	}
}
