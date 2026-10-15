package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const infoFlowTestToken = "test-only-relay-token-at-least-32-characters"

type infoFlowTestFixture struct {
	h                                   *Handler
	req                                 infoFlowRelayRequest
	owner, user, member, issue, runtime string
}

func newInfoFlowFixture(t *testing.T) infoFlowTestFixture {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil || cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Database != "infoflow_binding_test" {
		t.Fatal("requires explicitly configured loopback infoflow_binding_test database")
	}
	for _, fallback := range cfg.ConnConfig.Fallbacks {
		if fallback.Host != "127.0.0.1" {
			t.Fatal("database fallback must be loopback")
		}
	}
	t.Setenv("INFOFLOW_ISSUE_RELAY_TOKEN", infoFlowTestToken)
	t.Setenv("INFOFLOW_ISSUE_IDENTITY_DOMAIN", "example.test")
	sender := "relay-" + uuid.NewString()
	owner := dbfx.User(t, "Relay owner", sender+"@example.test")
	dbfx.Member(t, testWorkspaceID, owner, "owner")
	userSender := "member-" + uuid.NewString()
	user := dbfx.User(t, "Relay member", userSender+"@example.test")
	member := dbfx.Member(t, testWorkspaceID, user, "admin")
	runtime := dbfx.Runtime(t, "InfoFlow local fixture", testutil.Cols{"owner_id": owner, "visibility": "public"})
	agent := dbfx.Agent(t, "InfoFlow fixture agent", runtime, testutil.Cols{"owner_id": owner})
	issue := dbfx.Issue(t, "InfoFlow secret title", testutil.Cols{"creator_id": owner, "assignee_type": "agent", "assignee_id": agent})
	h := *testHandler
	h.Bus = events.New()
	req := infoFlowRelayRequest{infoFlowScope: infoFlowScope{RobotID: uuid.NewString(), AgentID: agent, WorkspaceID: testWorkspaceID}, Conversation: infoFlowConversation{Kind: "group", ID: fmt.Sprint(time.Now().UnixNano())}, Sender: sender, MessageID: uuid.NewString(), Operation: "bind", IssueRef: issue}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, sql := range []string{
			`DELETE FROM infoflow_issue_delivery WHERE binding_id IN (SELECT id FROM infoflow_issue_binding WHERE robot_id=$1)`,
			`DELETE FROM infoflow_issue_source WHERE binding_id IN (SELECT id FROM infoflow_issue_binding WHERE robot_id=$1)`,
			`DELETE FROM infoflow_issue_binding WHERE robot_id=$1`,
		} {
			if _, err := testPool.Exec(ctx, sql, req.RobotID); err != nil {
				t.Errorf("relay cleanup: %v", err)
			}
		}
		// Normal handlers create these rows rather than fixture Insert.
		for _, sql := range []string{`DELETE FROM task_comment_input WHERE task_id IN (SELECT id FROM agent_task_queue WHERE agent_id=$1)`, `DELETE FROM agent_task_queue WHERE agent_id=$1`, `DELETE FROM attachment WHERE uploader_id=$2`, `DELETE FROM comment WHERE issue_id IN (SELECT id FROM issue WHERE creator_id=$2)`, `DELETE FROM issue WHERE creator_id=$2`} {
			// Exec accepts unused named positions only when both are referenced.
			if strings.Contains(sql, "$2") {
				sql = strings.ReplaceAll(sql, "$2", "$1")
				_, _ = testPool.Exec(ctx, sql, owner)
			} else {
				_, _ = testPool.Exec(ctx, sql, agent)
			}
		}
	})
	return infoFlowTestFixture{h: &h, req: req, owner: owner, user: user, member: member, issue: issue, runtime: runtime}
}

func infoFlowCall(t *testing.T, h *Handler, req infoFlowRelayRequest, status int) infoFlowRelayResponse {
	t.Helper()
	var out infoFlowRelayResponse
	r := testutil.WithHeaders(testutil.JSONRequest("POST", "/api/integrations/infoflow/issue-relay", req), "Authorization", "Bearer "+infoFlowTestToken,
		"X-User-ID", uuid.NewString(), "X-User-Email", "forged@example.test", "X-Agent-ID", req.AgentID, "X-Task-ID", uuid.NewString(), "X-Actor-Source", "task_token")
	testutil.Call(t, h.InfoFlowIssueRelay, r).Want(status).JSON(&out)
	if status != 200 && (!out.Handled || !out.Silent) {
		t.Fatalf("failure fell through: %+v", out)
	}
	return out
}

// Pin once when constructing a new bound-message fixture, never in the HTTP
// call helper: retries must retain the original admission version.
func infoFlowPin(t *testing.T, req *infoFlowRelayRequest) {
	t.Helper()
	dbfx.QueryRow(t, `SELECT id::text,generation::text FROM infoflow_issue_binding WHERE workspace_id=$1 AND conversation_kind=$2 AND conversation_id=$3 AND ($2='group' OR (robot_id=$4 AND agent_id=$5)) AND issue_id IS NOT NULL AND ($6='' OR issue_id::text=$6)`, req.WorkspaceID, req.Conversation.Kind, req.Conversation.ID, req.RobotID, req.AgentID, req.IssueRef).Scan(&req.ExpectedBindingID, &req.ExpectedGeneration)
}

func (f infoFlowTestFixture) bind(t *testing.T) infoFlowRelayResponse {
	t.Helper()
	out := infoFlowCall(t, f.h, f.req, 200)
	if out.Binding == nil || out.Binding.IssueID != f.issue || !out.Handled {
		t.Fatalf("bind: %+v", out)
	}
	if f.req.Conversation.Kind == "group" {
		out = infoFlowActivate(t, f, f.req, out.Binding.Generation)
	}
	return out
}

func infoFlowCount(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	dbfx.QueryRow(t, sql, args...).Scan(&n)
	return n
}

func TestInfoFlowConversationModeIsMetadataOnly(t *testing.T) {
	for _, kind := range []string{"private", "group"} {
		t.Run(kind, func(t *testing.T) {
			f := newInfoFlowFixture(t)
			var email string
			dbfx.QueryRow(t, `SELECT email FROM "user" WHERE id=$1`, f.user).Scan(&email)
			req := f.req
			req.Sender = strings.Split(email, "@")[0]
			req.Conversation.Kind = kind
			if kind == "private" {
				req.Conversation.ID = req.Sender
			}
			lookup := req
			lookup.Operation, lookup.MessageID = "binding", ""
			lookup.IssueRef = ""
			if got := infoFlowCall(t, f.h, lookup, 200); got.ConversationMode != "chat" || got.Binding != nil {
				t.Fatalf("unbound mode: %+v", got)
			}
			gotBind := infoFlowCall(t, f.h, req, 200)
			if got := gotBind; got.ConversationMode != "issue" || got.Binding == nil {
				t.Fatalf("bound mode: %+v", got)
			}
			if kind == "group" {
				infoFlowActivate(t, f, req, gotBind.Binding.Generation)
			}
			message := req
			message.Operation, message.MessageID, message.Content = "message", uuid.NewString(), "请检查日志"
			infoFlowPin(t, &message)
			got := infoFlowCall(t, f.h, message, 200)
			if got.ConversationMode != "issue" || got.CommentID == "" || !strings.Contains(got.Notice, "没有智能体权限") {
				t.Fatalf("comment-only mode: %+v", got)
			}
			var content string
			dbfx.QueryRow(t, `SELECT content FROM comment WHERE id=$1`, got.CommentID).Scan(&content)
			if !strings.HasSuffix(content, message.Content) || strings.Contains(content, "[聊天模式]") || strings.Contains(content, "[任务模式]") {
				t.Fatalf("IM presentation leaked into issue comment: %q", content)
			}
			dbfx.Exec(t, `DELETE FROM member WHERE id=$1`, f.member)
			denied := infoFlowCall(t, f.h, message, 200)
			if denied.ConversationMode != "issue" || denied.CanComment || !strings.Contains(denied.Notice, "没有工作区权限") {
				t.Fatalf("denied mode: %+v", denied)
			}
			dbfx.Exec(t, `INSERT INTO member(id,workspace_id,user_id,role) VALUES($1,$2,$3,'admin')`, f.member, testWorkspaceID, f.user)
			unbind := req
			unbind.Operation, unbind.MessageID = "unbind", uuid.NewString()
			infoFlowPin(t, &unbind)
			unbound := infoFlowCall(t, f.h, unbind, 200)
			if unbound.ConversationMode != "chat" || unbound.Binding == nil || unbound.Binding.IssueID != f.issue {
				t.Fatalf("unbind should retain link but switch mode: %+v", unbound)
			}
			if replay := infoFlowCall(t, f.h, unbind, 200); replay.ConversationMode != "chat" {
				t.Fatalf("cached unbind mode: %+v", replay)
			}
			req.MessageID = uuid.NewString()
			infoFlowCall(t, f.h, req, 200)
			if replay := infoFlowCall(t, f.h, unbind, 200); replay.ConversationMode != "issue" {
				t.Fatalf("cached notice must use current route mode: %+v", replay)
			}
		})
	}
}

func TestInfoFlowAdmissionAndOriginalAuthor(t *testing.T) {
	f := newInfoFlowFixture(t)
	f.bind(t)
	req := f.req
	req.Operation = "message"
	infoFlowPin(t, &req)
	req.Content = "Please inspect"
	req.MessageID = uuid.NewString()
	// Unknown identity gets only owner/link metadata and no persisted input.
	req.Sender = "missing-" + uuid.NewString()
	out := infoFlowCall(t, f.h, req, 200)
	if !out.Handled || out.Silent || out.CanComment || out.Binding == nil || out.Binding.Title != "" || !strings.Contains(out.Notice, "没有工作区权限") {
		t.Fatalf("unmapped: %+v", out)
	}
	if infoFlowCount(t, `SELECT count(*) FROM comment WHERE issue_id=$1`, f.issue) != 0 {
		t.Fatal("unmapped user wrote comment")
	}
	// Current workspace admin is not allowed to invoke another user's private agent.
	var email string
	dbfx.QueryRow(t, `SELECT email FROM "user" WHERE id=$1`, f.user).Scan(&email)
	req.Sender = strings.Split(email, "@")[0]
	req.MessageID = uuid.NewString()
	req.Content = fmt.Sprintf("[@Spoof](mention://agent/%s) [@All](mention://all/all) work", uuid.NewString())
	out = infoFlowCall(t, f.h, req, 200)
	if out.CommentID == "" || !out.CanComment || !strings.Contains(out.Notice, "仅新增评论") {
		t.Fatalf("no-invoke member: %+v", out)
	}
	if infoFlowCount(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1`, f.issue) != 0 {
		t.Fatal("workspace admin bypassed invoke")
	}
	var authorType, author, content string
	var taskIsNull bool
	dbfx.QueryRow(t, `SELECT author_type,author_id::text,content,source_task_id IS NULL FROM comment WHERE id=$1`, out.CommentID).Scan(&authorType, &author, &content, &taskIsNull)
	if authorType != "member" || author != f.user || !taskIsNull || strings.Contains(content, "mention://all") {
		t.Fatalf("forged identity: %s %s %t %q", authorType, author, taskIsNull, content)
	}
	// A real invocation grant queues through CreateComment and preserves the human.
	dbfx.Exec(t, `UPDATE agent SET permission_mode='public_to' WHERE id=$1`, f.req.AgentID)
	dbfx.Insert(t, "agent_invocation_target", testutil.Cols{"agent_id": f.req.AgentID, "target_type": "member", "target_id": f.user, "created_by": f.owner})
	req.MessageID = uuid.NewString()
	req.Content = "Allowed work"
	out = infoFlowCall(t, f.h, req, 200)
	if out.CommentID == "" || out.Notice != "" {
		t.Fatalf("invoke: %+v", out)
	}
	var origin, trigger string
	dbfx.QueryRow(t, `SELECT originator_user_id::text,trigger_comment_id::text FROM agent_task_queue WHERE issue_id=$1 AND agent_id=$2`, f.issue, f.req.AgentID).Scan(&origin, &trigger)
	if origin != f.user || trigger != out.CommentID {
		t.Fatalf("lost original invoking human: origin=%s trigger=%s", origin, trigger)
	}
	// Loss of membership overrides the cached success and never repeats a write.
	dbfx.Exec(t, `DELETE FROM member WHERE id=$1`, f.member)
	out = infoFlowCall(t, f.h, req, 200)
	if out.CanComment || !strings.Contains(out.Notice, "没有工作区权限") || out.CommentID != "" {
		t.Fatalf("revoked retry: %+v", out)
	}
	if infoFlowCount(t, `SELECT count(*) FROM comment WHERE issue_id=$1 AND author_type='member'`, f.issue) != 2 {
		t.Fatal("retry duplicated comment")
	}
}

func TestInfoFlowDisabledAndMalformedIdentity(t *testing.T) {
	f := newInfoFlowFixture(t)
	f.bind(t)
	// This is an existing platform emergency-disabled UUID, not an integration allowlist.
	id := "514492f7-b30f-4147-bd33-c0e8ce5d6d4f"
	sender := "disabled-" + uuid.NewString()
	dbfx.User(t, "Disabled fixture", sender+"@example.test", testutil.Cols{"id": id})
	dbfx.Member(t, testWorkspaceID, id, "owner")
	req := f.req
	req.Operation = "message"
	req.MessageID = uuid.NewString()
	req.Content = "No run"
	req.Sender = sender
	out := infoFlowCall(t, f.h, req, 200)
	if out.CanComment || out.CommentID != "" || !strings.Contains(out.Notice, "没有工作区权限") {
		t.Fatalf("disabled: %+v", out)
	}
	for _, bad := range []string{"user@example.test", "../../owner", " sender", "user\nname"} {
		req.Sender = bad
		infoFlowCall(t, f.h, req, 400)
	}
	req = f.req
	req.Conversation = infoFlowConversation{Kind: "private", ID: "other"}
	infoFlowCall(t, f.h, req, 400)
	t.Setenv("INFOFLOW_ISSUE_RELAY_TOKEN", "short")
	infoFlowCall(t, f.h, f.req, 503)
}

func TestInfoFlowUnboundChatBypassKeepsAcceptedReplaysOwned(t *testing.T) {
	f := newInfoFlowFixture(t)
	outsider := "outsider-" + uuid.NewString()
	dbfx.User(t, "Nonmember fixture", outsider+"@example.test")
	req := f.req
	req.Operation, req.Sender, req.Content = "message", outsider, "Ordinary unbound Chat"
	req.IssueRef = ""
	for _, sender := range []string{outsider, "unmapped-" + uuid.NewString()} {
		req.Sender = sender
		req.MessageID = uuid.NewString()
		if out := infoFlowCall(t, f.h, req, 200); out.Handled || out.Binding != nil || out.Notice != "" {
			t.Fatalf("new unbound nonmember was denied: %+v", out)
		}
		preflight := req
		preflight.Operation = "binding"
		if out := infoFlowCall(t, f.h, preflight, 200); out.Handled || out.Binding != nil {
			t.Fatalf("unbound preflight denied: %+v", out)
		}
		preflight.Command = "/binding"
		if out := infoFlowCall(t, f.h, preflight, 200); !out.Handled || out.Notice != "当前会话没有绑定任务" {
			t.Fatalf("explicit unbound lookup: %+v", out)
		}
	}
	if infoFlowCount(t, `SELECT count(*) FROM infoflow_issue_binding WHERE robot_id=$1`, f.req.RobotID) != 0 {
		t.Fatal("unbound Chat created relay state")
	}
	f.bind(t)
	req.Sender = outsider
	req.MessageID = uuid.NewString()
	if out := infoFlowCall(t, f.h, req, 200); !out.Handled || out.CanComment || !strings.Contains(out.Notice, "没有工作区权限") {
		t.Fatalf("bound nonmember fell through: %+v", out)
	}
	accepted := req
	accepted.Sender = f.req.Sender
	accepted.MessageID = uuid.NewString()
	infoFlowPin(t, &accepted)
	saved := infoFlowCall(t, f.h, accepted, 200)
	if saved.CommentID == "" {
		t.Fatal("owner input was not accepted")
	}
	remove := f.req
	remove.Operation = "unbind"
	infoFlowPin(t, &remove)
	remove.MessageID = uuid.NewString()
	infoFlowCall(t, f.h, remove, 200)
	preflight := accepted
	preflight.Operation = "binding"
	if out := infoFlowCall(t, f.h, preflight, 200); !out.Handled || !out.Silent || out.CanComment {
		t.Fatalf("old accepted preflight fell through: %+v", out)
	}
	dbfx.Exec(t, `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, testWorkspaceID, f.owner)
	if out := infoFlowCall(t, f.h, accepted, 200); !out.Handled || out.CanComment || out.CommentID != "" || !strings.Contains(out.Notice, "没有工作区权限") {
		t.Fatalf("revoked accepted replay became Chat: %+v", out)
	}
	if infoFlowCount(t, `SELECT count(*) FROM comment WHERE issue_id=$1 AND author_type='member'`, f.issue) != 1 {
		t.Fatal("old replay duplicated input")
	}
	accepted.MessageID = uuid.NewString()
	accepted.ExpectedBindingID, accepted.ExpectedGeneration = "", ""
	if out := infoFlowCall(t, f.h, accepted, 200); out.Handled {
		t.Fatalf("new unbound traffic still gated: %+v", out)
	}
	for _, operation := range []string{"bind", "create", "unbind"} {
		command := accepted
		command.Operation = operation
		command.MessageID = uuid.NewString()
		if out := infoFlowCall(t, f.h, command, 200); !out.Handled || out.CanComment || !strings.Contains(out.Notice, "没有工作区权限") {
			t.Fatalf("%s bypassed membership: %+v", operation, out)
		}
	}
}

func TestInfoFlowCreateMediaPreflightIsAuthorizedAndReadOnly(t *testing.T) {
	f := newInfoFlowFixture(t)
	req := f.req
	req.Operation = "binding"
	req.Command = "create"
	req.MessageID = ""
	denied := req
	denied.Sender = "unmapped-" + uuid.NewString()
	if out := infoFlowCall(t, f.h, denied, 200); !out.Handled || out.Silent || out.CanComment || !strings.Contains(out.Notice, "没有工作区权限") {
		t.Fatalf("unmapped create media bypassed membership: %+v", out)
	}
	var email string
	dbfx.QueryRow(t, `SELECT email FROM "user" WHERE id=$1`, f.user).Scan(&email)
	denied.Sender = strings.Split(email, "@")[0]
	if out := infoFlowCall(t, f.h, denied, 200); !out.Handled || out.CanComment || !strings.Contains(out.Notice, "没有智能体权限") {
		t.Fatalf("admin create media bypassed invocation gate: %+v", out)
	}
	dbfx.Exec(t, `UPDATE member SET role='member' WHERE id=$1`, f.member)
	if out := infoFlowCall(t, f.h, denied, 200); !out.Handled || out.CanComment || !strings.Contains(out.Notice, "只有") {
		t.Fatalf("member create media bypassed binding management gate: %+v", out)
	}
	if out := infoFlowCall(t, f.h, req, 200); !out.Handled || !out.CanComment || out.Binding != nil || out.Notice != "" {
		t.Fatalf("unbound owner create media refused: %+v", out)
	}
	if infoFlowCount(t, `SELECT count(*) FROM infoflow_issue_binding WHERE robot_id=$1`, f.req.RobotID) != 0 || infoFlowCount(t, `SELECT count(*) FROM agent_task_queue WHERE agent_id=$1`, f.req.AgentID) != 0 || infoFlowCount(t, `SELECT count(*) FROM attachment WHERE uploader_id=$1`, f.owner) != 0 {
		t.Fatal("create preflight performed a mutation")
	}
	// A normal unbound message/preflight remains on the legacy Chat path.
	denied.Command = ""
	denied.IssueRef = ""
	if out := infoFlowCall(t, f.h, denied, 200); out.Handled {
		t.Fatalf("ordinary preflight changed: %+v", out)
	}
}

func TestInfoFlowConcurrentIdempotencyAndQuotes(t *testing.T) {
	f := newInfoFlowFixture(t)
	f.bind(t)
	req := f.req
	req.Operation = "message"
	infoFlowPin(t, &req)
	req.MessageID = uuid.NewString()
	req.Content = "One message"
	var wg sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := testutil.WithHeaders(testutil.JSONRequest("POST", "/relay", req), "Authorization", "Bearer "+infoFlowTestToken)
			w := httptest.NewRecorder()
			f.h.InfoFlowIssueRelay(w, r)
			results <- w
		}()
	}
	wg.Wait()
	close(results)
	var id string
	for w := range results {
		var out infoFlowRelayResponse
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.CommentID == "" {
			t.Fatalf("concurrent retry %d %s", w.Code, w.Body.String())
		}
		if id != "" && out.CommentID != id {
			t.Fatal("different comment ids")
		}
		id = out.CommentID
	}
	if infoFlowCount(t, `SELECT count(*) FROM comment WHERE issue_id=$1 AND author_type='member'`, f.issue) != 1 {
		t.Fatal("duplicate comment")
	}
	if infoFlowCount(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1`, f.issue) != 1 {
		t.Fatal("duplicate task")
	}
	conflict := req
	conflict.Content = "different body"
	infoFlowCall(t, f.h, conflict, 409)
	quote := req
	quote.MessageID = uuid.NewString()
	quote.ReplyToMessageID = req.MessageID
	quote.Content = "Quoted follow-up"
	out := infoFlowCall(t, f.h, quote, 200)
	var parent string
	dbfx.QueryRow(t, `SELECT parent_id::text FROM comment WHERE id=$1`, out.CommentID).Scan(&parent)
	if parent != id {
		t.Fatalf("quote parent %s != %s", parent, id)
	}
	quote.MessageID = uuid.NewString()
	quote.ReplyToMessageID = "external-pre-binding-message"
	out = infoFlowCall(t, f.h, quote, 200)
	if infoFlowCount(t, `SELECT count(*) FROM comment WHERE id=$1 AND parent_id IS NULL`, out.CommentID) != 1 {
		t.Fatal("unknown quote not accepted top-level")
	}
	// Same external id in a private conversation must not reuse a group receipt.
	private := f.req
	private.Conversation = infoFlowConversation{Kind: "private", ID: f.req.Sender}
	private.MessageID = uuid.NewString()
	infoFlowCall(t, f.h, private, 200)
	private.Operation = "message"
	infoFlowPin(t, &private)
	private.MessageID = req.MessageID
	private.Content = req.Content
	out = infoFlowCall(t, f.h, private, 200)
	if out.CommentID == id {
		t.Fatal("private/group source conflated")
	}
}

type infoFlowFailSourceStarter struct{ base txStarter }

func (s infoFlowFailSourceStarter) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.base.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &infoFlowFailSourceTx{Tx: tx}, nil
}

type infoFlowFailSourceTx struct{ pgx.Tx }

func (tx *infoFlowFailSourceTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if strings.HasPrefix(sql, "INSERT INTO infoflow_issue_source") {
		return pgconn.CommandTag{}, errors.New("fixture source mapping failure")
	}
	return tx.Tx.Exec(ctx, sql, args...)
}

func TestInfoFlowAtomicRollbackAndPostCommitEvents(t *testing.T) {
	f := newInfoFlowFixture(t)
	f.bind(t)
	eventCount := 0
	f.h.Bus.SubscribeAll(func(e events.Event) {
		if e.Type == "comment:created" {
			eventCount++
			if infoFlowCount(t, `SELECT count(*) FROM comment WHERE issue_id=$1`, f.issue) == 0 {
				t.Error("event observed uncommitted comment")
			}
		}
	})
	req := f.req
	req.Operation = "message"
	infoFlowPin(t, &req)
	req.MessageID = uuid.NewString()
	req.Content = "Atomic message"
	original := f.h.TxStarter
	f.h.TxStarter = infoFlowFailSourceStarter{base: original}
	infoFlowCall(t, f.h, req, 500)
	if eventCount != 0 || infoFlowCount(t, `SELECT count(*) FROM comment WHERE issue_id=$1`, f.issue) != 0 || infoFlowCount(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1`, f.issue) != 0 {
		t.Fatal("partial commit or event on rollback")
	}
	f.h.TxStarter = original
	out := infoFlowCall(t, f.h, req, 200)
	if out.CommentID == "" || eventCount != 1 {
		t.Fatalf("retry/event %s %d", out.CommentID, eventCount)
	}
}

func TestInfoFlowAttachmentsPermissionAndRetry(t *testing.T) {
	f := newInfoFlowFixture(t)
	f.bind(t)
	store := &mockStorage{}
	f.h.Storage = store
	req := f.req
	req.Operation = "message"
	infoFlowPin(t, &req)
	req.MessageID = uuid.NewString()
	req.Content = "image"
	req.Attachments = []infoFlowAttachment{{Filename: "image.png", ContentType: "image/png", DataBase64: base64.StdEncoding.EncodeToString([]byte("fixture image bytes"))}}
	denied := req
	denied.Sender = "missing-" + uuid.NewString()
	out := infoFlowCall(t, f.h, denied, 200)
	if out.CanComment || infoFlowCount(t, `SELECT count(*) FROM attachment WHERE issue_id=$1`, f.issue) != 0 {
		t.Fatal("forbidden upload")
	}
	out = infoFlowCall(t, f.h, req, 200)
	if out.CommentID == "" || infoFlowCount(t, `SELECT count(*) FROM attachment WHERE comment_id=$1 AND uploader_id=$2 AND uploader_type='member'`, out.CommentID, f.owner) != 1 {
		t.Fatal("image not linked under original member")
	}
	infoFlowCall(t, f.h, req, 200)
	if infoFlowCount(t, `SELECT count(*) FROM attachment WHERE issue_id=$1`, f.issue) != 1 {
		t.Fatal("retry uploaded twice")
	}
	missing := req
	missing.MessageID = uuid.NewString()
	missing.Attachments = nil
	missing.HasAttachments = true
	infoFlowCall(t, f.h, missing, 400)
}

func TestInfoFlowPermissionRevocationWaitsForAcceptedTransaction(t *testing.T) {
	f := newInfoFlowFixture(t)
	f.bind(t)
	dbfx.Exec(t, `UPDATE agent SET permission_mode='public_to' WHERE id=$1`, f.req.AgentID)
	dbfx.Insert(t, "agent_invocation_target", testutil.Cols{"agent_id": f.req.AgentID, "target_type": "member", "target_id": f.user, "created_by": f.owner})
	var email string
	dbfx.QueryRow(t, `SELECT email FROM "user" WHERE id=$1`, f.user).Scan(&email)
	// Block the comment INSERT after admission acquired all grant locks. Routing
	// now reads the issue earlier, so an issue lock is not an admission barrier.
	ctx := context.Background()
	blocker, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	if _, err = blocker.Exec(ctx, `LOCK TABLE comment IN SHARE MODE`); err != nil {
		t.Fatal(err)
	}
	req := f.req
	req.Sender = strings.Split(email, "@")[0]
	req.Operation = "message"
	infoFlowPin(t, &req)
	req.MessageID = uuid.NewString()
	req.Content = "permission race"
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := testutil.WithHeaders(testutil.JSONRequest("POST", "/relay", req), "Authorization", "Bearer "+infoFlowTestToken)
		w := httptest.NewRecorder()
		f.h.InfoFlowIssueRelay(w, r)
		done <- w
	}()
	// Wait for the INSERT, proving the relay passed admission.
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		err = testPool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%INSERT INTO comment%')`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("relay did not reach comment lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	revoke, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer revoke.Rollback(ctx)
	_, _ = revoke.Exec(ctx, `SET LOCAL lock_timeout='100ms'`)
	_, err = revoke.Exec(ctx, `DELETE FROM agent_invocation_target WHERE agent_id=$1`, f.req.AgentID)
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != "55P03" {
		t.Fatalf("revocation must wait for admission commit: %v", err)
	}
	_ = revoke.Rollback(ctx)
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case w := <-done:
		if w.Code != 200 {
			t.Fatalf("relay %d %s", w.Code, w.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("relay stuck")
	}
	dbfx.Exec(t, `DELETE FROM agent_invocation_target WHERE agent_id=$1`, f.req.AgentID)
	req.MessageID = uuid.NewString()
	out := infoFlowCall(t, f.h, req, 200)
	if !strings.Contains(out.Notice, "仅新增评论") || infoFlowCount(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1`, f.issue) != 1 {
		t.Fatal("revocation allowed new run")
	}
}

func TestInfoFlowEnqueueConflictSavepointKeepsTransactionUsable(t *testing.T) {
	f := newInfoFlowFixture(t)
	tx, err := testPool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	q := db.New(&infoFlowSavepointTx{Tx: tx})
	// The same sqlc-tagged unique violation the normal handler catches must not
	// poison the outer transaction; the subsequent source/comment can commit.
	_, err = q.CreateAgentTask(context.Background(), db.CreateAgentTaskParams{ID: parseUUID(uuid.NewString()), AgentID: parseUUID(f.req.AgentID), IssueID: parseUUID(f.issue), RuntimeID: parseUUID(f.runtime)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = q.CreateAgentTask(context.Background(), db.CreateAgentTaskParams{ID: parseUUID(uuid.NewString()), AgentID: parseUUID(f.req.AgentID), IssueID: parseUUID(f.issue), RuntimeID: parseUUID(f.runtime)})
	if !isUniqueViolation(err) {
		t.Fatalf("expected queue unique slot conflict: %v", err)
	}
	var one int
	if err = tx.QueryRow(context.Background(), `SELECT 1`).Scan(&one); err != nil {
		t.Fatalf("outer transaction poisoned: %v", err)
	}
}
