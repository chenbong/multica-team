package handler

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestInfoFlowBroadcastOnePrivatePerTask(t *testing.T) {
	f := infoFlowConversationFixture(t)
	f.req.Conversation = infoFlowConversation{Kind: "private", ID: f.req.Sender}
	first := f.bind(t)
	repeat := f.req
	repeat.MessageID = uuid.NewString()
	if got := infoFlowCall(t, f.h, repeat, 200); got.Binding == nil || got.Binding.BindingID != first.Binding.BindingID {
		t.Fatal("idempotent bind must remain supported")
	}
	other := f.req
	other.MessageID, other.RobotID = uuid.NewString(), "another-fixture-robot"
	got := infoFlowCall(t, f.h, other, 200)
	if got.Binding != nil || !strings.Contains(got.Notice, "已绑定其他私聊") {
		t.Fatalf("second private conversation accepted: %+v", got)
	}
	if n := infoFlowCount(t, `SELECT count(*) FROM infoflow_issue_binding WHERE issue_id=$1 AND conversation_kind='private'`, f.issue); n != 1 {
		t.Fatalf("expected exactly one private binding, got %d", n)
	}
	infoFlowConversationCall(t, f, "POST", f.user, "", map[string]string{"kind": "private"}, 409)
	group := f
	group.req.Conversation = infoFlowConversation{Kind: "group", ID: "123456789"}
	group.req.MessageID = uuid.NewString()
	group.bind(t)
}

func infoFlowDrainBroadcast(t *testing.T, f infoFlowTestFixture) []infoFlowDelivery {
	t.Helper()
	var all []infoFlowDelivery
	for i := 0; i < 20; i++ {
		rows := infoFlowPoll(t, f, 20)
		if len(rows) == 0 {
			return all
		}
		for _, row := range rows {
			all = append(all, row)
			infoFlowAck(t, f, row.ID, nil, 200)
		}
	}
	t.Fatal("broadcast drain did not terminate")
	return nil
}

func broadcastCheck(t *testing.T, f infoFlowTestFixture, id string) bool {
	t.Helper()
	r := testutil.WithHeaders(testutil.JSONRequest("POST", "/check", infoFlowDeliveryRequest{infoFlowScope: f.req.infoFlowScope}), "Authorization", "Bearer "+infoFlowTestToken)
	r = withURLParam(r, "id", id)
	var result struct {
		Allowed bool `json:"allowed"`
	}
	testutil.Call(t, f.h.InfoFlowIssueDeliveryCheck, r).Want(http.StatusOK).JSON(&result)
	return result.Allowed
}

func TestInfoFlowBroadcastOldWebInputNewReply(t *testing.T) {
	f := newInfoFlowNotifyFixture(t)
	old := dbfx.Comment(t, f.issue, "Old report instruction", testutil.Cols{"author_id": f.owner})
	task := dbfx.Task(t, f.req.AgentID, testutil.Cols{"issue_id": f.issue, "runtime_id": f.runtime, "trigger_comment_id": old})
	before := infoFlowAgentReply(t, f, f.issue, old, task, "Before binding")
	f.bind(t)
	after := infoFlowAgentReply(t, f, f.issue, old, task, "After binding")
	got := infoFlowPoll(t, f, 20)
	if len(got) != 1 || got[0].CommentID != after || got[0].CommentID == before || !got[0].RequiresCheck {
		t.Fatalf("new reply to old instruction was lost or history replayed: %+v", got)
	}
	if !slices.Equal(got[0].NotifyUsers, []string{f.req.Sender}) {
		t.Fatalf("web requester: %v", got[0].NotifyUsers)
	}
	if !broadcastCheck(t, f, got[0].ID) {
		t.Fatal("active delivery denied")
	}
}

func TestInfoFlowBroadcastPrivateInputFansOutWithoutEcho(t *testing.T) {
	f := newInfoFlowNotifyFixture(t)
	f.bind(t)
	private := f
	private.req.Conversation = infoFlowConversation{Kind: "private", ID: f.req.Sender}
	private.req.MessageID = uuid.NewString()
	private.bind(t)
	_, input, task := infoFlowNotifyInput(t, private, f.req.Sender, nil)
	reply := infoFlowAgentReply(t, f, f.issue, input.CommentID, task, "Shared result")
	var groupCount, privateCount int
	for i := 0; i < 4; i++ {
		for _, d := range infoFlowPoll(t, f, 20) {
			if d.Conversation.Kind == "private" {
				privateCount++
			} else {
				groupCount++
			}
			if d.CommentID == input.CommentID {
				if d.Conversation == private.req.Conversation {
					t.Fatal("original message echoed to sender")
				}
				if d.AuthorType != "user" || len(d.NotifyUsers) != 0 {
					t.Fatal("human mirror triggered AT")
				}
			} else if d.CommentID != reply {
				t.Fatal("unexpected content")
			}
			infoFlowAck(t, f, d.ID, nil, 200)
		}
	}
	if groupCount != 2 || privateCount != 1 {
		t.Fatalf("fanout counts group=%d private=%d", groupCount, privateCount)
	}
}

func TestInfoFlowBroadcastDeploymentCutoffAndUnbind(t *testing.T) {
	f := newInfoFlowNotifyFixture(t)
	f.bind(t)
	_, input, task := infoFlowNotifyInput(t, f, f.req.Sender, nil)
	old := infoFlowAgentReply(t, f, f.issue, input.CommentID, task, "Unsent before cutover")
	prior := infoFlowPoll(t, f, 20)
	if len(prior) != 1 {
		t.Fatalf("fixture: %+v", prior)
	}
	dbfx.Exec(t, `UPDATE infoflow_issue_binding SET broadcast_started_at=clock_timestamp() WHERE id=$1`, prior[0].Binding.BindingID)
	if broadcastCheck(t, f, prior[0].ID) {
		t.Fatal("old queued delivery crossed cutover")
	}
	if got := infoFlowPoll(t, f, 20); len(got) != 0 {
		t.Fatalf("cutover replayed %s: %+v", old, got)
	}
	fresh := infoFlowAgentReply(t, f, f.issue, input.CommentID, task, "New after cutover")
	got := infoFlowPoll(t, f, 20)
	if len(got) != 1 || got[0].CommentID != fresh {
		t.Fatalf("fresh reply: %+v", got)
	}
	unbind := f.req
	unbind.Operation = "unbind"
	unbind.MessageID = uuid.NewString()
	infoFlowPin(t, &unbind)
	infoFlowCall(t, f.h, unbind, 200)
	if broadcastCheck(t, f, got[0].ID) {
		t.Fatal("unbound destination remained sendable")
	}
	f.req.MessageID = uuid.NewString()
	f.bind(t)
	if again := infoFlowPoll(t, f, 20); len(again) != 0 {
		t.Fatalf("rebind replayed queued work: %+v", again)
	}
}

func TestInfoFlowBroadcastWakeupExplicitOnly(t *testing.T) {
	f := newInfoFlowNotifyFixture(t)
	f.bind(t)
	memberName := "explicit-" + uuid.NewString()
	person := dbfx.User(t, "Explicit recipient", memberName+"@example.test")
	dbfx.Member(t, testWorkspaceID, person, "member")
	for _, instruction := range []string{"Write report", "Write report and notify @" + memberName} {
		raw, _ := json.Marshal(map[string]any{"wakeup_id": uuid.NewString(), "wakeup_instruction": instruction})
		task := dbfx.Task(t, f.req.AgentID, testutil.Cols{"issue_id": f.issue, "runtime_id": f.runtime, "originator_user_id": f.owner, "accountable_user_id": f.owner, "context": string(raw)})
		reply := infoFlowAgentReply(t, f, f.issue, "", task, "Scheduled output")
		got := infoFlowPoll(t, f, 20)
		if len(got) != 1 || got[0].CommentID != reply {
			t.Fatalf("scheduled reply not broadcast: %+v", got)
		}
		want := []string{}
		if instruction != "Write report" {
			want = []string{memberName}
		}
		if !slices.Equal(got[0].NotifyUsers, want) {
			t.Fatalf("wakeup inherited creator or lost explicit audience: %v", got[0].NotifyUsers)
		}
		infoFlowAck(t, f, got[0].ID, nil, 200)
	}
}

func TestInfoFlowBroadcastEditsDoNotReplaceOrReplaySentContent(t *testing.T) {
	f := newInfoFlowNotifyFixture(t)
	f.bind(t)
	comment := dbfx.Comment(t, f.issue, "Original web text", testutil.Cols{"author_id": f.owner})
	rows := infoFlowPoll(t, f, 20)
	if len(rows) != 1 || rows[0].CommentID != comment {
		t.Fatalf("fixture: %+v", rows)
	}
	dbfx.Exec(t, "UPDATE comment SET content='Edited web text' WHERE id=$1", comment)
	pending := infoFlowPoll(t, f, 20)
	if len(pending) != 1 || pending[0].ID != rows[0].ID || pending[0].Content != "Original web text" {
		t.Fatal("edit changed pinned delivery")
	}
	infoFlowAck(t, f, rows[0].ID, nil, 200)
	if again := infoFlowPoll(t, f, 20); len(again) != 0 {
		t.Fatal("edit replayed old comment")
	}
	dbfx.Exec(t, "UPDATE comment SET deleted_at=now() WHERE id=$1", comment)
	if again := infoFlowPoll(t, f, 20); len(again) != 0 {
		t.Fatal("delete generated another delivery")
	}
}

func TestInfoFlowBroadcastUnknownMetadataDoesNotInventAudience(t *testing.T) {
	f := newInfoFlowNotifyFixture(t)
	f.bind(t)
	task := dbfx.Task(t, f.req.AgentID, testutil.Cols{"issue_id": f.issue, "runtime_id": f.runtime, "context": "[]"})
	reply := infoFlowAgentReply(t, f, f.issue, "", task, "Reply with unknown context")
	rows := infoFlowPoll(t, f, 20)
	if len(rows) != 1 || rows[0].CommentID != reply || len(rows[0].NotifyUsers) != 0 {
		t.Fatalf("metadata blocked content or invented recipients: %+v", rows)
	}
}
