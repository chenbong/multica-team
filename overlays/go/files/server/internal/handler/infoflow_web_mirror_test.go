package handler

import (
	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
	"testing"
)

func TestInfoFlowPlatformCommentsMirrorToEachActiveDestinationWithoutIMEcho(t *testing.T) {
	f := newInfoFlowFixture(t)
	f.bind(t)
	private := f.req
	private.Conversation = infoFlowConversation{Kind: "private", ID: f.req.Sender}
	private.MessageID = uuid.NewString()
	infoFlowCall(t, f.h, private, 200)
	web := dbfx.Comment(t, f.issue, "Web authored message", testutil.Cols{"author_type": "member", "author_id": f.owner})
	reply := dbfx.Comment(t, f.issue, "Web response", testutil.Cols{"author_type": "agent", "author_id": f.req.AgentID, "parent_id": web})
	standalone := dbfx.Comment(t, f.issue, "Standalone task output", testutil.Cols{"author_type": "agent", "author_id": f.req.AgentID})
	seen := map[string]bool{}
	for i := 0; i < 4; i++ {
		deliveries := infoFlowPoll(t, f, 20)
		if len(deliveries) == 0 {
			break
		}
		for _, d := range deliveries {
			key := d.Conversation.Kind + ":" + d.CommentID
			if seen[key] {
				t.Fatal("already acked mirror repeated")
			}
			seen[key] = true
			if d.CommentID == web {
				if d.AuthorType != "user" || d.AuthorName != f.req.Sender {
					t.Fatalf("incorrect user attribution: %+v", d)
				}
			} else if d.CommentID == reply || d.CommentID == standalone {
				if d.AuthorType != "agent" {
					t.Fatalf("incorrect agent attribution: %+v", d)
				}
			} else {
				t.Fatalf("unrelated output broadcast: %+v", d)
			}
			infoFlowAck(t, f, d.ID, []string{uuid.NewString()}, 200)
		}
	}
	if len(seen) != 6 {
		t.Fatalf("expected all new task comments in both destinations, got %+v", seen)
	}
	_, _, _ = infoFlowInput(t, f, f.req.Conversation)
	if got := infoFlowPoll(t, f, 20); len(got) != 1 || got[0].Conversation.Kind != "private" {
		t.Fatalf("IM input should mirror only to the other conversation: %+v", got)
	}
}

func infoFlowNoticePoll(t *testing.T, f infoFlowTestFixture) []infoFlowConversationNotice {
	t.Helper()
	var out struct {
		Notices []infoFlowConversationNotice `json:"notices"`
	}
	r := testutil.WithHeaders(testutil.JSONRequest("POST", "/notices", f.req.infoFlowScope), "Authorization", "Bearer "+infoFlowTestToken)
	testutil.Call(t, f.h.InfoFlowConversationNotices, r).Want(200).JSON(&out)
	return out.Notices
}

func TestInfoFlowWebBindingNoticesAndUnbindRemainDurableAndScoped(t *testing.T) {
	for _, kind := range []string{"private", "group"} {
		t.Run(kind, func(t *testing.T) {
			f := newInfoFlowFixture(t)
			if kind == "private" {
				f.req.Conversation = infoFlowConversation{Kind: kind, ID: f.req.Sender}
			}
			bound := f.bind(t)
			notices := infoFlowNoticePoll(t, f)
			if len(notices) != 1 || notices[0].Kind != "bound" || notices[0].Conversation != f.req.Conversation || notices[0].Identifier == "" || notices[0].Title != "InfoFlow secret title" {
				t.Fatalf("bound notice: %+v", notices)
			}
			f.bind(t)
			dbfx.Exec(t, `UPDATE infoflow_conversation_notice SET next_at=clock_timestamp()-interval '1 second' WHERE id=$1`, notices[0].ID)
			if again := infoFlowNoticePoll(t, f); len(again) != 1 || again[0].ID != notices[0].ID {
				t.Fatalf("bind duplicated: %+v", again)
			}
			r := testutil.WithURLParams(testutil.WithHeaders(testutil.JSONRequest("POST", "/ack", infoFlowDeliveryRequest{infoFlowScope: f.req.infoFlowScope, MessageIDs: []string{"notice-message"}}), "Authorization", "Bearer "+infoFlowTestToken), "id", notices[0].ID)
			testutil.Call(t, f.h.InfoFlowConversationNoticeAck, r).Want(200)
			if again := infoFlowNoticePoll(t, f); len(again) != 0 {
				t.Fatal("acked notice replayed")
			}
			r = testutil.WithURLParams(testutil.WithHeaders(testutil.JSONRequest("DELETE", "/binding", nil), "X-User-ID", f.owner, "X-Workspace-ID", f.req.WorkspaceID), "id", f.issue, "bindingId", bound.Binding.BindingID)
			testutil.Call(t, f.h.DeleteIssueInfoFlowConversation, r).Want(200)
			notices = infoFlowNoticePoll(t, f)
			if len(notices) != 1 || notices[0].Kind != "unbound" || notices[0].Conversation != f.req.Conversation {
				t.Fatalf("unbind notice: %+v", notices)
			}
			wrong := f
			wrong.req.WorkspaceID = uuid.NewString()
			if leak := infoFlowNoticePoll(t, wrong); len(leak) != 0 {
				t.Fatalf("workspace leak: %+v", leak)
			}
			dbfx.Exec(t, `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, f.req.WorkspaceID, f.owner)
			if revoked := infoFlowNoticePoll(t, f); len(revoked) != 0 {
				t.Fatalf("revoked notice exposed: %+v", revoked)
			}
		})
	}
}

func TestInfoFlowTaskHeaderTitlesFollowRenamesWithoutChangingBinding(t *testing.T) {
	f := newInfoFlowFixture(t)
	bound := f.bind(t)
	const title = "任务已改名 **保留原始名称**"
	dbfx.Exec(t, `UPDATE issue SET title=$2 WHERE id=$1`, f.issue, title)
	lookup := f.req
	lookup.Operation, lookup.MessageID = "binding", ""
	current := infoFlowCall(t, f.h, lookup, 200)
	if current.Binding == nil || current.Binding.Title != title || current.Binding.Generation != bound.Binding.Generation {
		t.Fatalf("title changed task routing: %+v", current.Binding)
	}
	notices := infoFlowNoticePoll(t, f)
	if len(notices) != 1 || notices[0].Title != title {
		t.Fatalf("notice did not carry current authorized title: %+v", notices)
	}
	lookup.Sender = "unregistered-header-reader"
	denied := infoFlowCall(t, f.h, lookup, 200)
	if denied.Binding != nil && denied.Binding.Title != "" {
		t.Fatal("denied task header exposed its title")
	}
}

func TestInfoFlowOldBindingHistoryDoesNotCreateNoticeOnLeaseRenewal(t *testing.T) {
	f := newInfoFlowFixture(t)
	bound := f.bind(t)
	dbfx.Exec(t, `DELETE FROM infoflow_conversation_notice WHERE binding_id=$1`, bound.Binding.BindingID)
	infoFlowActivate(t, f, f.req, bound.Binding.Generation)
	if notices := infoFlowNoticePoll(t, f); len(notices) != 0 {
		t.Fatalf("old binding replayed on rollout: %+v", notices)
	}
}

func TestInfoFlowMirrorsBeforeBindingAndAfterRemovalAreExcluded(t *testing.T) {
	f := newInfoFlowFixture(t)
	dbfx.Comment(t, f.issue, "old web message", testutil.Cols{"author_type": "member", "author_id": f.owner})
	bound := f.bind(t)
	if got := infoFlowPoll(t, f, 20); len(got) != 0 {
		t.Fatalf("history replayed: %+v", got)
	}
	dbfx.Comment(t, f.issue, "new web message", testutil.Cols{"author_type": "member", "author_id": f.owner})
	r := testutil.WithURLParams(testutil.WithHeaders(testutil.JSONRequest("DELETE", "/binding", nil), "X-User-ID", f.owner, "X-Workspace-ID", f.req.WorkspaceID), "id", f.issue, "bindingId", bound.Binding.BindingID)
	testutil.Call(t, f.h.DeleteIssueInfoFlowConversation, r).Want(200)
	if got := infoFlowPoll(t, f, 20); len(got) != 0 {
		t.Fatalf("after unbind mirror: %+v", got)
	}
}

func TestInfoFlowNoticeCheckFencesRemovalAndRevocationAfterPoll(t *testing.T) {
	f := newInfoFlowFixture(t)
	bound := f.bind(t)
	notices := infoFlowNoticePoll(t, f)
	if len(notices) != 1 {
		t.Fatal("missing notice")
	}
	check := func(id string, want bool) {
		r := testutil.WithURLParams(testutil.WithHeaders(testutil.JSONRequest("POST", "/check", f.req.infoFlowScope), "Authorization", "Bearer "+infoFlowTestToken), "id", id)
		var out struct {
			Allowed bool `json:"allowed"`
		}
		testutil.Call(t, f.h.InfoFlowConversationNoticeCheck, r).Want(200).JSON(&out)
		if out.Allowed != want {
			t.Fatalf("check=%v want=%v", out.Allowed, want)
		}
	}
	check(notices[0].ID, true)
	r := testutil.WithURLParams(testutil.WithHeaders(testutil.JSONRequest("DELETE", "/binding", nil), "X-User-ID", f.owner, "X-Workspace-ID", f.req.WorkspaceID), "id", f.issue, "bindingId", bound.Binding.BindingID)
	testutil.Call(t, f.h.DeleteIssueInfoFlowConversation, r).Want(200)
	check(notices[0].ID, false)
	dbfx.Exec(t, `UPDATE infoflow_conversation_notice SET next_at=clock_timestamp()-interval '1 second' WHERE id=$1`, notices[0].ID)
	unbound := infoFlowNoticePoll(t, f)
	if len(unbound) != 1 || unbound[0].Kind != "unbound" {
		t.Fatalf("stale notice starved new notice: %+v", unbound)
	}
	if infoFlowCount(t, `SELECT count(*) FROM infoflow_conversation_notice WHERE id=$1 AND cancelled_at IS NOT NULL`, notices[0].ID) != 1 {
		t.Fatal("retired notice not cancelled")
	}
	check(unbound[0].ID, true)
	dbfx.Exec(t, `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, f.req.WorkspaceID, f.owner)
	check(unbound[0].ID, false)
}

func TestInfoFlowCrossIssuePrivateTaskCannotBroadcastAsWebReply(t *testing.T) {
	private := newInfoFlowFixture(t)
	private.req.Conversation = infoFlowConversation{Kind: "private", ID: private.req.Sender}
	private.bind(t)
	_, _, task := infoFlowInput(t, private, private.req.Conversation)
	dbfx.Exec(t, `UPDATE agent SET name=$2 WHERE id=$1`, private.req.AgentID, "Private source "+uuid.NewString())
	group := newInfoFlowFixture(t)
	dbfx.Exec(t, `UPDATE issue SET assignee_id=$2 WHERE id=$1`, group.issue, private.req.AgentID)
	group.req.AgentID = private.req.AgentID
	group.bind(t)
	web := dbfx.Comment(t, group.issue, "group web input", testutil.Cols{"author_type": "member", "author_id": group.owner})
	dbfx.Comment(t, group.issue, "private context from another issue", testutil.Cols{"author_type": "agent", "author_id": private.req.AgentID, "parent_id": web, "source_task_id": task})
	deliveries := infoFlowPoll(t, group, 20)
	if len(deliveries) != 1 || deliveries[0].CommentID != web {
		t.Fatalf("cross-issue private task exposed: %+v", deliveries)
	}
	infoFlowAck(t, group, deliveries[0].ID, []string{"web-mirror"}, 200)
	if leaked := infoFlowPoll(t, group, 20); len(leaked) != 0 {
		t.Fatalf("cross-issue output broadcast: %+v", leaked)
	}
}

func TestInfoFlowNoticeAttemptsRotatePastTwentyFailingDestinations(t *testing.T) {
	f := newInfoFlowFixture(t)
	for i := 0; i < 21; i++ {
		dbfx.Insert(t, "infoflow_conversation_notice", testutil.Cols{"id": uuid.NewString(), "binding_id": uuid.NewString(), "generation": uuid.NewString(), "kind": "unbound", "workspace_id": f.req.WorkspaceID, "issue_id": f.issue, "actor_id": f.owner, "robot_id": f.req.RobotID, "agent_id": f.req.AgentID, "conversation_kind": "private", "conversation_id": f.req.Sender, "identifier": "TEST-1", "workspace_slug": "test"})
	}
	first := infoFlowNoticePoll(t, f)
	if len(first) != 20 {
		t.Fatalf("first batch=%d", len(first))
	}
	// No ACK: SDK failures still advance the durable attempt time.
	second := infoFlowNoticePoll(t, f)
	if len(second) != 1 {
		t.Fatalf("later destination starved: %+v", second)
	}
	for _, n := range first {
		if n.ID == second[0].ID {
			t.Fatal("attempt did not rotate")
		}
	}
}

func TestInfoFlowFailedUnbindIsRetiredWhenSameIssueIsRebound(t *testing.T) {
	for _, kind := range []string{"private", "group"} {
		t.Run(kind, func(t *testing.T) {
			f := newInfoFlowFixture(t)
			if kind == "private" {
				f.req.Conversation = infoFlowConversation{Kind: kind, ID: f.req.Sender}
			}
			bound := f.bind(t)
			r := testutil.WithURLParams(testutil.WithHeaders(testutil.JSONRequest("DELETE", "/binding", nil), "X-User-ID", f.owner, "X-Workspace-ID", f.req.WorkspaceID), "id", f.issue, "bindingId", bound.Binding.BindingID)
			testutil.Call(t, f.h.DeleteIssueInfoFlowConversation, r).Want(200)
			pending := infoFlowNoticePoll(t, f)
			if len(pending) != 1 || pending[0].Kind != "unbound" {
				t.Fatalf("unlink: %+v", pending)
			}
			f.req.MessageID = uuid.NewString()
			f.bind(t)
			dbfx.Exec(t, `UPDATE infoflow_conversation_notice SET next_at=clock_timestamp()-interval '1 second' WHERE robot_id=$1`, f.req.RobotID)
			current := infoFlowNoticePoll(t, f)
			if len(current) != 1 || current[0].Kind != "bound" {
				t.Fatalf("stale unlink followed rebind: %+v", current)
			}
			if infoFlowCount(t, `SELECT count(*) FROM infoflow_conversation_notice WHERE id=$1 AND cancelled_at IS NOT NULL`, pending[0].ID) != 1 {
				t.Fatal("failed old unlink remains retryable")
			}
		})
	}
}
