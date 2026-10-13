package handler

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
)

type infoFlowCountingStorage struct {
	mockStorage
	uploadCalls int
}

func (s *infoFlowCountingStorage) Upload(ctx context.Context, key string, data []byte, contentType, filename string) (string, error) {
	s.uploadCalls++
	return s.mockStorage.Upload(ctx, key, data, contentType, filename)
}

func TestInfoFlowGroupRosterNamesAreCurrentAndScoped(t *testing.T) {
	f := newInfoFlowFixture(t)
	worker := dbfx.Agent(t, "Private worker", f.runtime, testutil.Cols{"owner_id": f.owner})
	other := dbfx.Agent(t, "Unrelated agent", f.runtime, testutil.Cols{"owner_id": f.owner})
	squad := dbfx.Insert(t, "squad", testutil.Cols{"workspace_id": f.req.WorkspaceID, "name": "Roster squad", "leader_id": f.req.AgentID, "creator_id": f.owner})
	dbfx.Insert(t, "squad_member", testutil.Cols{"squad_id": squad, "member_type": "agent", "member_id": worker, "role": "member"})
	dbfx.Exec(t, `UPDATE issue SET assignee_type='squad',assignee_id=$2 WHERE id=$1`, f.issue, squad)
	f.bind(t)
	req := f.req
	req.Operation = "binding"
	view := infoFlowCall(t, f.h, req, 200).Binding.Group
	if len(view.AgentNames) != 2 || view.AgentNames[worker] != "Private worker" || view.AgentNames[f.req.AgentID] == "" || view.AgentNames[other] != "" {
		t.Fatalf("incorrect scoped roster: %+v", view.AgentNames)
	}
	dbfx.Exec(t, `UPDATE agent SET name='Renamed worker' WHERE id=$1`, worker)
	view = infoFlowCall(t, f.h, req, 200).Binding.Group
	if view.AgentNames[worker] != "Renamed worker" || view.State != "active" {
		t.Fatalf("name refresh changed topology or returned stale data: %+v", view)
	}
}

func TestInfoFlowRebindRequiresOriginalAuthority(t *testing.T) {
	for _, authority := range []string{"none", "owner", "admin", "binder", "leader_owner"} {
		t.Run(authority, func(t *testing.T) {
			f := newInfoFlowFixture(t)
			bound := f.bind(t)
			dbfx.Exec(t, `UPDATE member SET role='member' WHERE id=$1`, f.member)
			bobAgent := dbfx.Agent(t, "Bob's target", f.runtime, testutil.Cols{"owner_id": f.user})
			target := dbfx.Issue(t, "Bob's issue", testutil.Cols{"creator_id": f.owner, "assignee_type": "agent", "assignee_id": bobAgent})
			var email string
			dbfx.QueryRow(t, `SELECT email FROM "user" WHERE id=$1`, f.user).Scan(&email)
			switch authority {
			case "owner", "admin":
				dbfx.Exec(t, `UPDATE member SET role=$2 WHERE id=$1`, f.member, authority)
			case "binder":
				dbfx.Exec(t, `UPDATE infoflow_issue_binding SET binder_id=$2 WHERE id=$1`, bound.Binding.BindingID, f.user)
			case "leader_owner":
				dbfx.Exec(t, `UPDATE agent SET owner_id=$2 WHERE id=$1`, f.req.AgentID, f.user)
			}
			beforeTasks := infoFlowCount(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id IN ($1,$2)`, f.issue, target)
			req := f.req
			req.Sender, req.AgentID, req.RobotID = strings.Split(email, "@")[0], bobAgent, "bob-robot"
			for _, destination := range []string{f.issue, target} {
				req.MessageID, req.IssueRef = uuid.NewString(), destination
				got := infoFlowCall(t, f.h, req, 200)
				var issue, generation, binder string
				dbfx.QueryRow(t, `SELECT issue_id::text,generation::text,binder_id::text FROM infoflow_issue_binding WHERE id=$1`, bound.Binding.BindingID).Scan(&issue, &generation, &binder)
				if authority == "none" {
					if !strings.Contains(got.Notice, "只有") || issue != f.issue || generation != bound.Binding.Generation || binder != f.owner {
						t.Fatalf("unauthorized rebind changed original binding: %+v %s/%s/%s", got, issue, generation, binder)
					}
					if n := infoFlowCount(t, `SELECT count(*) FROM infoflow_issue_source WHERE binding_id=$1 AND message_id=$2`, bound.Binding.BindingID, req.MessageID); n != 0 {
						t.Fatal("denied command saved a mutation receipt")
					}
				} else if issue != destination || binder != f.user || got.Binding == nil || got.Binding.IssueID != destination {
					t.Fatalf("authorized rebind refused: %+v", got)
				}
			}
			if n := infoFlowCount(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id IN ($1,$2)`, f.issue, target); n != beforeTasks {
				t.Fatal("binding mutation enqueued work")
			}
			if authority == "none" {
				// The same ordinary member may establish a NEW group binding to
				// their own target; the restriction is authority over the old one.
				req.Conversation.ID += "1"
				req.Conversation.ID = req.Conversation.ID[:18]
				req.MessageID = uuid.NewString()
				req.IssueRef = f.issue
				if got := infoFlowCall(t, f.h, req, 200); got.Binding != nil || !strings.Contains(got.Notice, "只有") {
					t.Fatalf("first binding bypassed target owner: %+v", got)
				}
				req.MessageID, req.IssueRef = uuid.NewString(), target
				if got := infoFlowCall(t, f.h, req, 200); got.Binding == nil || got.Binding.IssueID != target {
					t.Fatalf("first binding to owned target denied: %+v", got)
				}
				t.Cleanup(func() {
					dbfx.Exec(t, `DELETE FROM infoflow_issue_source WHERE binding_id IN (SELECT id FROM infoflow_issue_binding WHERE robot_id='bob-robot')`)
					dbfx.Exec(t, `DELETE FROM infoflow_issue_binding WHERE robot_id='bob-robot'`)
				})
			}
		})
	}
}

func TestInfoFlowPinnedMessagesCannotFollowBindingChanges(t *testing.T) {
	for _, kind := range []string{"private", "group"} {
		for _, change := range []string{"rebind", "unbind"} {
			t.Run(kind+"/"+change, func(t *testing.T) {
				f := newInfoFlowFixture(t)
				if kind == "private" {
					f.req.Conversation = infoFlowConversation{Kind: kind, ID: f.req.Sender}
				}
				f.bind(t)
				accepted := f.req
				accepted.Operation, accepted.MessageID, accepted.Content = "message", uuid.NewString(), "Already accepted"
				infoFlowPin(t, &accepted)
				saved := infoFlowCall(t, f.h, accepted, 200)
				if saved.CommentID == "" {
					t.Fatal("missing accepted comment")
				}
				pending := accepted
				pending.MessageID, pending.Content = uuid.NewString(), "Original issue only"
				// Fail AFTER writing the comment, before commit, proving that only
				// a committed receipt is allowed to bypass the generation fence.
				original := f.h.TxStarter
				f.h.TxStarter = infoFlowFailSourceStarter{base: original}
				infoFlowCall(t, f.h, pending, 500)
				f.h.TxStarter = original
				target := dbfx.Issue(t, "Replacement issue", testutil.Cols{"creator_id": f.owner, "assignee_type": "agent", "assignee_id": f.req.AgentID})
				mutation := f.req
				mutation.MessageID = uuid.NewString()
				mutation.Operation = "unbind"
				if change == "rebind" {
					mutation.Operation, mutation.IssueRef = "bind", target
				}
				changed := infoFlowCall(t, f.h, mutation, 200)
				if change == "rebind" && kind == "group" {
					infoFlowActivate(t, f, mutation, changed.Binding.Generation)
				}
				infoFlowCall(t, f.h, pending, 409)
				// Fence before uploads as well, not merely roll the SQL write back
				// after having sent private bytes to object storage.
				pending.HasAttachments = true
				pending.Attachments = []infoFlowAttachment{{Filename: "x.png", ContentType: "image/png", DataBase64: base64.StdEncoding.EncodeToString([]byte("fixture"))}}
				store := &infoFlowCountingStorage{}
				f.h.Storage = store
				infoFlowCall(t, f.h, pending, 409)
				if store.uploadCalls != 0 || store.uploadStreamCalls != 0 {
					t.Fatal("stale input uploaded a file")
				}
				if replay := infoFlowCall(t, f.h, accepted, 200); replay.CommentID != saved.CommentID {
					t.Fatalf("accepted retry lost receipt: %+v", replay)
				}
				if n := infoFlowCount(t, `SELECT count(*) FROM comment WHERE issue_id IN ($1,$2) AND author_type='member'`, f.issue, target); n != 1 {
					t.Fatalf("stale message created %d comments", n)
				}
				if n := infoFlowCount(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1`, target); n != 0 {
					t.Fatal("stale input invoked replacement issue")
				}
				if n := infoFlowCount(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id IN ($1,$2)`, f.issue, target); n != 1 {
					t.Fatalf("rollback/replay changed original task count: %d", n)
				}
				if n := infoFlowCount(t, `SELECT count(*) FROM infoflow_issue_source WHERE binding_id=$1 AND message_id=$2`, accepted.ExpectedBindingID, pending.MessageID); n != 0 {
					t.Fatal("stale input created a receipt")
				}
			})
		}
	}
}

func TestInfoFlowBoundMessageRequiresBothVersionFields(t *testing.T) {
	for _, kind := range []string{"private", "group"} {
		t.Run(kind, func(t *testing.T) {
			f := newInfoFlowFixture(t)
			if kind == "private" {
				f.req.Conversation = infoFlowConversation{Kind: kind, ID: f.req.Sender}
			}
			bound := f.bind(t)
			for _, pin := range [][2]string{{"", ""}, {bound.Binding.BindingID, ""}, {"", bound.Binding.Generation}, {uuid.NewString(), bound.Binding.Generation}, {bound.Binding.BindingID, uuid.NewString()}} {
				req := f.req
				req.Operation, req.MessageID, req.Content = "message", uuid.NewString(), "must be rejected"
				req.ExpectedBindingID, req.ExpectedGeneration = pin[0], pin[1]
				infoFlowCall(t, f.h, req, 409)
			}
			for _, pin := range [][2]string{{"not-a-uuid", bound.Binding.Generation}, {bound.Binding.BindingID, "not-a-uuid"}} {
				req := f.req
				req.Operation, req.MessageID, req.Content = "message", uuid.NewString(), "invalid UUID"
				req.ExpectedBindingID, req.ExpectedGeneration = pin[0], pin[1]
				infoFlowCall(t, f.h, req, 400)
			}
			if infoFlowCount(t, `SELECT count(*) FROM comment WHERE issue_id=$1`, f.issue) != 0 || infoFlowCount(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1`, f.issue) != 0 {
				t.Fatal("version failure wrote comment/task")
			}
		})
	}
}

func TestInfoFlowSquadDispatchPreservesLeaderRole(t *testing.T) {
	for _, kind := range []string{"private", "group"} {
		for _, target := range []string{"leader", "leader_no_invoke", "worker", "single"} {
			t.Run(kind+"/"+target, func(t *testing.T) {
				f := newInfoFlowFixture(t)
				worker := dbfx.Agent(t, "Worker", f.runtime, testutil.Cols{"owner_id": f.owner})
				squad := dbfx.Insert(t, "squad", testutil.Cols{"workspace_id": f.req.WorkspaceID, "name": "Execution squad", "leader_id": f.req.AgentID, "creator_id": f.owner})
				dbfx.Insert(t, "squad_member", testutil.Cols{"squad_id": squad, "member_type": "agent", "member_id": worker, "role": "member"})
				if target != "single" {
					dbfx.Exec(t, `UPDATE issue SET assignee_type='squad',assignee_id=$2 WHERE id=$1`, f.issue, squad)
				}
				if kind == "private" {
					f.req.Conversation = infoFlowConversation{Kind: kind, ID: f.req.Sender}
				}
				f.bind(t)
				req := f.req
				if target == "worker" {
					req.AgentID, req.RobotID = worker, "worker-"+uuid.NewString()
					if kind == "private" {
						if out := infoFlowCall(t, f.h, req, 200); out.Binding == nil {
							t.Fatal("worker private bind failed")
						}
						t.Cleanup(func() {
							dbfx.Exec(t, `DELETE FROM infoflow_issue_source WHERE binding_id IN (SELECT id FROM infoflow_issue_binding WHERE robot_id=$1)`, req.RobotID)
							dbfx.Exec(t, `DELETE FROM infoflow_issue_binding WHERE robot_id=$1`, req.RobotID)
						})
					}
				}
				if target == "leader_no_invoke" {
					var email string
					dbfx.QueryRow(t, `SELECT email FROM "user" WHERE id=$1`, f.user).Scan(&email)
					if kind == "private" {
						req.Sender = strings.Split(email, "@")[0]
						req.Conversation.ID = req.Sender
						req.MessageID = uuid.NewString()
						if out := infoFlowCall(t, f.h, req, 200); out.Binding == nil {
							t.Fatal("no-invoke private binding failed")
						}
					} else {
						req.Sender = strings.Split(email, "@")[0]
					}
				}
				req.Operation, req.MessageID = "message", uuid.NewString()
				req.Content = "please work [@outer [@member](mention://agent/" + worker + ")](mention://squad/" + squad + ")"
				infoFlowPin(t, &req)
				if infoFlowCount(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1`, f.issue) != 0 {
					t.Fatal("test requires a fresh queue")
				}
				out := infoFlowCall(t, f.h, req, 200)
				if out.CommentID == "" {
					t.Fatalf("comment not saved: %+v", out)
				}
				var content string
				dbfx.QueryRow(t, `SELECT content FROM comment WHERE id=$1`, out.CommentID).Scan(&content)
				mentions := util.ParseMentions(content)
				wantType, wantID := "agent", req.AgentID
				if strings.HasPrefix(target, "leader") {
					wantType, wantID = "squad", squad
				}
				if len(mentions) != 1 || mentions[0].Type != wantType || mentions[0].ID != wantID {
					t.Fatalf("incorrect dispatch mention: %+v", mentions)
				}
				count := infoFlowCount(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1`, f.issue)
				if target == "leader_no_invoke" {
					if count != 0 || !strings.Contains(out.Notice, "仅新增评论") {
						t.Fatalf("no-invoke leader ran: %d %+v", count, out)
					}
					return
				}
				if count != 1 {
					t.Fatalf("wanted one fresh task, got %d", count)
				}
				var isLeader bool
				var actualSquad, actualAgent string
				dbfx.QueryRow(t, `SELECT is_leader_task,COALESCE(squad_id::text,''),agent_id::text FROM agent_task_queue WHERE issue_id=$1`, f.issue).Scan(&isLeader, &actualSquad, &actualAgent)
				if actualAgent != req.AgentID || isLeader != (target == "leader") {
					t.Fatalf("wrong execution role leader=%v agent=%s", isLeader, actualAgent)
				}
				if target == "leader" && actualSquad != squad || target != "leader" && actualSquad != "" {
					t.Fatalf("wrong execution squad: %s", actualSquad)
				}
			})
		}
	}
}
