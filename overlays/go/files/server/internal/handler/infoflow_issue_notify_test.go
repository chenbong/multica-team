package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// The existing relay fixture requires a fixed database name. These regressions
// also support schema-only clones so parallel workers never share TestMain data.
func newInfoFlowNotifyFixture(t *testing.T) infoFlowTestFixture {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil || cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 ||
		(cfg.ConnConfig.Database != "infoflow_binding_test" && !strings.HasPrefix(cfg.ConnConfig.Database, "chat_access_notify_")) {
		t.Fatal("requires loopback:55439 infoflow_binding_test or a chat_access_notify_ schema clone")
	}
	for _, fallback := range cfg.ConnConfig.Fallbacks {
		if fallback.Host != "127.0.0.1" || fallback.Port != 55439 {
			t.Fatal("unsafe database fallback")
		}
	}
	t.Setenv("INFOFLOW_ISSUE_RELAY_TOKEN", infoFlowTestToken)
	t.Setenv("INFOFLOW_ISSUE_IDENTITY_DOMAIN", "example.test")
	sender := "notify-" + uuid.NewString()
	owner := dbfx.User(t, "Notify owner", sender+"@example.test")
	dbfx.Member(t, testWorkspaceID, owner, "owner")
	runtime := dbfx.Runtime(t, "Notify runtime", testutil.Cols{"owner_id": owner, "visibility": "public"})
	agent := dbfx.Agent(t, "Notify agent", runtime, testutil.Cols{"owner_id": owner})
	issue := dbfx.Issue(t, "Notify issue", testutil.Cols{"creator_id": owner, "assignee_type": "agent", "assignee_id": agent})
	h := *testHandler
	h.Bus = events.New()
	req := infoFlowRelayRequest{infoFlowScope: infoFlowScope{RobotID: uuid.NewString(), AgentID: agent, WorkspaceID: testWorkspaceID},
		Conversation: infoFlowConversation{Kind: "group", ID: fmt.Sprint(time.Now().UnixNano())}, Sender: sender,
		MessageID: uuid.NewString(), Operation: "bind", IssueRef: issue}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, sql := range []string{
			`DELETE FROM infoflow_issue_delivery WHERE binding_id IN (SELECT id FROM infoflow_issue_binding WHERE robot_id=$1)`,
			`DELETE FROM infoflow_issue_source WHERE binding_id IN (SELECT id FROM infoflow_issue_binding WHERE robot_id=$1)`,
			`DELETE FROM infoflow_issue_binding WHERE robot_id=$1`,
		} {
			if _, err := testPool.Exec(ctx, sql, req.RobotID); err != nil {
				t.Errorf("notify cleanup: %v", err)
			}
		}
		for _, sql := range []string{
			`DELETE FROM agent_task_queue WHERE agent_id=$1`,
			`DELETE FROM comment WHERE issue_id IN (SELECT id FROM issue WHERE assignee_id=$1)`,
		} {
			if _, err := testPool.Exec(ctx, sql, agent); err != nil {
				t.Errorf("notify cleanup: %v", err)
			}
		}
	})
	return infoFlowTestFixture{h: &h, req: req, owner: owner, issue: issue, runtime: runtime}
}

func infoFlowNotifyInput(t *testing.T, f infoFlowTestFixture, sender string, users []string) (infoFlowRelayRequest, infoFlowRelayResponse, string) {
	t.Helper()
	r := f.req
	r.Operation, r.MessageID, r.Content = "message", uuid.NewString(), "Input without notification markup"
	r.Sender, r.NotifyUsers = sender, users
	infoFlowPin(t, &r)
	out := infoFlowCall(t, f.h, r, 200)
	var task string
	dbfx.QueryRow(t, `SELECT COALESCE((SELECT id::text FROM agent_task_queue WHERE trigger_comment_id=$1),'')`, out.CommentID).Scan(&task)
	return r, out, task
}

func wantInfoFlowNotifyUsers(t *testing.T, got, want []string) {
	t.Helper()
	want = slices.Clone(want)
	slices.Sort(want)
	if got == nil || !slices.Equal(got, want) {
		t.Fatalf("notify_users=%v (nil=%v), want %v", got, got == nil, want)
	}
}

func TestInfoFlowNotifyLegacyWireHash(t *testing.T) {
	r := infoFlowRelayRequest{infoFlowScope: infoFlowScope{RobotID: "r", AgentID: "a", WorkspaceID: "w"},
		Conversation: infoFlowConversation{Kind: "group", ID: "42"}, Sender: "alice", MessageID: "m", Operation: "message", Content: "unchanged"}
	const legacy = `{"robot_id":"r","agent_id":"a","workspace_id":"w","conversation":{"kind":"group","id":"42"},"sender":"alice","message_id":"m","operation":"message","content":"unchanged"}`
	for _, users := range [][]string{nil, {}} {
		r.NotifyUsers = users
		raw, err := json.Marshal(r)
		if err != nil || string(raw) != legacy {
			t.Fatalf("legacy hash input changed: %s, %v", raw, err)
		}
	}
}

func TestInfoFlowNotifySourceAndCanonicalRetry(t *testing.T) {
	f := newInfoFlowNotifyFixture(t)
	f.bind(t)
	r, out, _ := infoFlowNotifyInput(t, f, f.req.Sender, []string{"BOB", "alice", "bob", "ATALL", "all", "bot", "robot", "123456", f.req.RobotID, "bad@example.test", "bad name", strings.Repeat("x", 129)})
	var saved []string
	dbfx.QueryRow(t, `SELECT notify_users FROM infoflow_issue_source WHERE comment_id=$1`, out.CommentID).Scan(&saved)
	wantInfoFlowNotifyUsers(t, saved, []string{"alice", "bob", f.req.Sender})
	r.NotifyUsers = []string{"bob", "ALICE", f.req.Sender}
	if retry := infoFlowCall(t, f.h, r, 200); retry.CommentID != out.CommentID {
		t.Fatalf("canonical retry duplicated comment: %+v", retry)
	}
	r.NotifyUsers = []string{"charlie"}
	infoFlowCall(t, f.h, r, 409)
	var content string
	dbfx.QueryRow(t, `SELECT content FROM comment WHERE id=$1`, out.CommentID).Scan(&content)
	if strings.Contains(content, "alice") || strings.Contains(content, "bob") || !strings.HasSuffix(content, r.Content) {
		t.Fatalf("notification metadata changed issue content: %q", content)
	}
	legacy, old, _ := infoFlowNotifyInput(t, f, f.req.Sender, nil)
	raw, _ := json.Marshal(legacy)
	hash := sha256.Sum256(raw)
	var fingerprint string
	dbfx.QueryRow(t, `SELECT request_hash,notify_users FROM infoflow_issue_source WHERE comment_id=$1`, old.CommentID).Scan(&fingerprint, &saved)
	if fingerprint != hex.EncodeToString(hash[:]) || strings.Contains(string(raw), "notify_users") {
		t.Fatal("omitted field changed historical fingerprint")
	}
	wantInfoFlowNotifyUsers(t, saved, []string{f.req.Sender})
	legacy.NotifyUsers = []string{}
	if retry := infoFlowCall(t, f.h, legacy, 200); retry.CommentID != old.CommentID {
		t.Fatal("empty list changed omitted-field retry")
	}
	tooMany := legacy
	tooMany.MessageID = uuid.NewString()
	tooMany.NotifyUsers = make([]string, infoFlowMaxNotifyUsers+1)
	infoFlowCall(t, f.h, tooMany, 400)
	tooMany.NotifyUsers = nil
	for i := range infoFlowMaxNotifyUsers {
		tooMany.NotifyUsers = append(tooMany.NotifyUsers, fmt.Sprintf("person%d", i))
	}
	infoFlowCall(t, f.h, tooMany, 400) // sender also occupies a slot
}

func TestInfoFlowNotifyDeliveryRecursiveUnion(t *testing.T) {
	f := newInfoFlowNotifyFixture(t)
	f.bind(t)
	_, first, task := infoFlowNotifyInput(t, f, f.req.Sender, []string{"alice", "BOB"})
	other := "other-" + uuid.NewString()
	user := dbfx.User(t, "Other human", other+"@example.test")
	dbfx.Member(t, testWorkspaceID, user, "admin")
	_, second, _ := infoFlowNotifyInput(t, f, other, []string{"bob", "charlie"})
	infoFlowNotifyInput(t, f, f.req.Sender, []string{"unrelated-person"})
	dbfx.Exec(t, `UPDATE agent_task_queue SET coalesced_comment_ids=ARRAY[$2::uuid] WHERE id=$1`, task, second.CommentID)
	child := dbfx.Task(t, f.req.AgentID, testutil.Cols{"issue_id": f.issue, "parent_task_id": task, "runtime_id": f.runtime})
	reply := infoFlowAgentReply(t, f, f.issue, first.CommentID, child, "**Markdown reply** with no AT body changes")
	got := infoFlowPoll(t, f, 20)
	if len(got) != 1 || got[0].CommentID != reply || got[0].Content != "**Markdown reply** with no AT body changes" {
		t.Fatalf("recursive delivery: %+v", got)
	}
	wantInfoFlowNotifyUsers(t, got[0].NotifyUsers, []string{"alice", "bob", "charlie", f.req.Sender, other})
	again := infoFlowPoll(t, f, 20)
	if len(again) != 1 || again[0].ID != got[0].ID {
		t.Fatalf("pending receipt changed: %+v", again)
	}
	wantInfoFlowNotifyUsers(t, again[0].NotifyUsers, got[0].NotifyUsers)
	// Legacy rows recover their sender without guessing any other recipients.
	dbfx.Exec(t, `UPDATE infoflow_issue_source SET notify_users='{}' WHERE comment_id=$1`, second.CommentID)
	// A cycle in ancestry must terminate without duplicating notification users.
	dbfx.Exec(t, `UPDATE agent_task_queue SET retry_of_task_id=$2 WHERE id=$1`, task, child)
	again = infoFlowPoll(t, f, 20)
	if len(again) != 1 {
		t.Fatal("cyclic ancestry failed")
	}
	// Source edits cannot change an audience already returned to the bridge.
	wantInfoFlowNotifyUsers(t, again[0].NotifyUsers, got[0].NotifyUsers)
	infoFlowAck(t, f, got[0].ID, nil, 200)
	infoFlowAgentReply(t, f, f.issue, first.CommentID, child, "new reply from legacy source")
	again = infoFlowPoll(t, f, 20)
	if len(again) != 1 {
		t.Fatalf("new legacy-source reply: %+v", again)
	}
	wantInfoFlowNotifyUsers(t, again[0].NotifyUsers, []string{"alice", "bob", f.req.Sender, other})
}

type infoFlowNotifyFailAckStarter struct{ base txStarter }

func (s infoFlowNotifyFailAckStarter) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.base.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return infoFlowNotifyFailAckTx{Tx: tx}, nil
}

type infoFlowNotifyFailAckTx struct{ pgx.Tx }

func (tx infoFlowNotifyFailAckTx) Commit(context.Context) error {
	return errors.New("injected temporary ACK commit failure")
}

func TestInfoFlowNotifyDeliverySnapshotSurvivesFailedAckAndNewInputs(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%v", legacy), func(t *testing.T) {
			f := newInfoFlowNotifyFixture(t)
			bound := f.bind(t)
			_, first, task := infoFlowNotifyInput(t, f, f.req.Sender, []string{"alice"})
			reply := infoFlowAgentReply(t, f, f.issue, first.CommentID, task, "**original Markdown reply**")
			if legacy {
				var source string
				dbfx.QueryRow(t, `SELECT id::text FROM infoflow_issue_source WHERE comment_id=$1`, first.CommentID).Scan(&source)
				dbfx.Insert(t, "infoflow_issue_delivery", testutil.Cols{
					"id": uuid.NewString(), "binding_id": bound.Binding.BindingID, "generation": bound.Binding.Generation,
					"source_id": source, "comment_id": reply, "content": "**original Markdown reply**",
				}) // An old unsent delivery has a NULL snapshot.
			}
			got := infoFlowPoll(t, f, 20)
			if len(got) != 1 || got[0].CommentID != reply {
				t.Fatalf("first delivery: %+v", got)
			}
			delivery := got[0]
			wantInfoFlowNotifyUsers(t, delivery.NotifyUsers, []string{"alice", f.req.Sender})
			var saved []string
			dbfx.QueryRow(t, `SELECT notify_users FROM infoflow_issue_delivery WHERE id=$1`, delivery.ID).Scan(&saved)
			wantInfoFlowNotifyUsers(t, saved, delivery.NotifyUsers)
			// The bridge has sent both Markdown and native AT; its ACK fails
			// before commit, so the same durable receipt must be returned again.
			messageIDs := []string{"1234567890123456788", "1234567890123456789"}
			original := f.h.TxStarter
			f.h.TxStarter = infoFlowNotifyFailAckStarter{base: original}
			infoFlowAck(t, f, delivery.ID, messageIDs, 500)
			f.h.TxStarter = original
			if infoFlowCount(t, `SELECT count(*) FROM infoflow_issue_delivery WHERE id=$1 AND acked_at IS NULL AND cardinality(external_message_ids)=0`, delivery.ID) != 1 {
				t.Fatal("failed ACK was not rolled back")
			}
			other := "other-" + uuid.NewString()
			user := dbfx.User(t, "Later sender", other+"@example.test")
			dbfx.Member(t, testWorkspaceID, user, "admin")
			_, later, _ := infoFlowNotifyInput(t, f, other, []string{"bob"})
			dbfx.Exec(t, `UPDATE agent_task_queue SET coalesced_comment_ids=ARRAY[$2::uuid] WHERE id=$1`, task, later.CommentID)
			again := infoFlowPoll(t, f, 20)
			if len(again) != 1 || again[0].ID != delivery.ID || again[0].Content != delivery.Content {
				t.Fatalf("pending delivery changed after coalescing: %+v", again)
			}
			wantInfoFlowNotifyUsers(t, again[0].NotifyUsers, delivery.NotifyUsers)
			infoFlowAck(t, f, delivery.ID, messageIDs, 200)
			infoFlowAck(t, f, delivery.ID, messageIDs, 200) // ACK retry is idempotent.
			var ackIDs []string
			dbfx.QueryRow(t, `SELECT notify_users,external_message_ids FROM infoflow_issue_delivery WHERE id=$1 AND acked_at IS NOT NULL`, delivery.ID).Scan(&saved, &ackIDs)
			wantInfoFlowNotifyUsers(t, saved, delivery.NotifyUsers)
			slices.Sort(ackIDs)
			if !slices.Equal(ackIDs, messageIDs) {
				t.Fatalf("Markdown/AT receipt IDs were lost or duplicated: %v", ackIDs)
			}
			if got := infoFlowPoll(t, f, 20); len(got) != 0 {
				t.Fatalf("ACKed reply was returned again: %+v", got)
			}
			// New replies use the full new audience; the old receipt cannot
			// block the queue or cause those people to be added to old output.
			next := infoFlowAgentReply(t, f, f.issue, first.CommentID, task, "new reply")
			got = infoFlowPoll(t, f, 20)
			if len(got) != 1 || got[0].CommentID != next || got[0].ID == delivery.ID {
				t.Fatalf("later reply was blocked: %+v", got)
			}
			wantInfoFlowNotifyUsers(t, got[0].NotifyUsers, []string{"alice", "bob", f.req.Sender, other})
		})
	}
}

func TestInfoFlowNotifyAckMessageIDBoundary(t *testing.T) {
	f := newInfoFlowNotifyFixture(t)
	f.bind(t)
	_, input, task := infoFlowNotifyInput(t, f, f.req.Sender, nil)
	infoFlowAgentReply(t, f, f.issue, input.CommentID, task, "reply for all group members")
	got := infoFlowPoll(t, f, 20)
	if len(got) != 1 {
		t.Fatalf("missing delivery: %+v", got)
	}
	ids := make([]string, infoFlowMaxDeliveryMessageIDs+1)
	for i := range ids {
		ids[i] = fmt.Sprint(100000 + i)
	}
	infoFlowAck(t, f, got[0].ID, ids, 400)
	if infoFlowCount(t, `SELECT count(*) FROM infoflow_issue_delivery WHERE id=$1 AND acked_at IS NULL`, got[0].ID) != 1 {
		t.Fatal("oversized ACK changed delivery state")
	}
	ids = ids[:infoFlowMaxDeliveryMessageIDs]
	// Production supplies both the full list and its final ID as a legacy
	// alias. The duplicate alias must not consume another batch slot.
	req := infoFlowDeliveryRequest{infoFlowScope: f.req.infoFlowScope, MessageIDs: ids, MessageID: ids[len(ids)-1]}
	for range 2 {
		r := testutil.WithURLParams(testutil.WithHeaders(testutil.JSONRequest("POST", "/ack", req), "Authorization", "Bearer "+infoFlowTestToken), "id", got[0].ID)
		testutil.Call(t, f.h.InfoFlowIssueDeliveryAck, r).Want(200)
	}
	var saved []string
	dbfx.QueryRow(t, `SELECT external_message_ids FROM infoflow_issue_delivery WHERE id=$1 AND acked_at IS NOT NULL`, got[0].ID).Scan(&saved)
	slices.Sort(saved)
	if !slices.Equal(saved, ids) {
		t.Fatalf("101 Markdown/AT IDs did not survive ACK retries: %v", saved)
	}
}

func TestInfoFlowNotifyTaskOnlyAndCommentAncestry(t *testing.T) {
	f := newInfoFlowNotifyFixture(t)
	bound := f.bind(t)
	task := dbfx.Task(t, f.req.AgentID, testutil.Cols{"issue_id": f.issue, "runtime_id": f.runtime})
	dbfx.Insert(t, "infoflow_issue_source", testutil.Cols{
		"id":         uuid.NewString(),
		"binding_id": bound.Binding.BindingID, "generation": bound.Binding.Generation,
		"message_id": uuid.NewString(), "sender": f.req.Sender, "user_id": f.owner,
		"request_hash": "task-only-source", "issue_id": f.issue, "task_id": task,
		"response": testutil.Raw("'{}'::jsonb"), "notify_users": []string{"alice"},
	})
	first := infoFlowAgentReply(t, f, f.issue, "", task, "task-only reply")
	got := infoFlowPoll(t, f, 20)
	if len(got) != 1 || got[0].CommentID != first {
		t.Fatalf("task-only source: %+v", got)
	}
	wantInfoFlowNotifyUsers(t, got[0].NotifyUsers, []string{"alice", f.req.Sender})
	infoFlowAck(t, f, got[0].ID, nil, 200)
	// Only parent_id connects this reply to the IM source, not its own task.
	second := dbfx.Comment(t, f.issue, "parent-derived reply", testutil.Cols{
		"author_type": "agent", "author_id": f.req.AgentID, "parent_id": first,
	})
	got = infoFlowPoll(t, f, 20)
	if len(got) != 1 || got[0].CommentID != second {
		t.Fatalf("comment ancestry: %+v", got)
	}
	wantInfoFlowNotifyUsers(t, got[0].NotifyUsers, []string{"alice", f.req.Sender})
}

func TestInfoFlowNotifyDeliveryMixedOriginsBlocked(t *testing.T) {
	for _, kind := range []string{"private", "group", "generation"} {
		t.Run(kind, func(t *testing.T) {
			f := newInfoFlowNotifyFixture(t)
			f.bind(t)
			_, first, task := infoFlowNotifyInput(t, f, f.req.Sender, []string{"alice"})
			infoFlowAgentReply(t, f, f.issue, first.CommentID, task, "pending group answer")
			if got := infoFlowPoll(t, f, 20); len(got) != 1 {
				t.Fatalf("initial delivery: %+v", got)
			}
			other := f
			if kind != "group" {
				other.req.Conversation = infoFlowConversation{Kind: "private", ID: f.req.Sender}
			} else {
				other.issue = dbfx.Issue(t, "Other conversation issue", testutil.Cols{"creator_id": f.owner, "assignee_type": "agent", "assignee_id": f.req.AgentID})
				other.req.IssueRef = other.issue
				other.req.Conversation.ID = fmt.Sprint(time.Now().UnixNano())
			}
			other.req.MessageID = uuid.NewString()
			other.bind(t)
			_, second, _ := infoFlowNotifyInput(t, other, f.req.Sender, []string{"secret-person"})
			if kind == "generation" {
				dbfx.Exec(t, `UPDATE infoflow_issue_source SET binding_id=$2,generation=$3 WHERE comment_id=$1`, second.CommentID, first.Binding.BindingID, uuid.NewString())
			}
			dbfx.Exec(t, `UPDATE agent_task_queue SET coalesced_comment_ids=ARRAY[$2::uuid] WHERE id=$1`, task, second.CommentID)
			infoFlowAgentReply(t, f, f.issue, first.CommentID, task, "mixed output")
			if got := infoFlowPoll(t, f, 20); len(got) != 0 {
				t.Fatalf("mixed ancestry leaked: %+v", got)
			}
		})
	}
}

func TestInfoFlowNotifyPrivateAndWebEmpty(t *testing.T) {
	for _, kind := range []string{"private", "web", "web-agent", "unattributed-agent"} {
		t.Run(kind, func(t *testing.T) {
			f := newInfoFlowNotifyFixture(t)
			if kind == "private" {
				f.req.Conversation = infoFlowConversation{Kind: "private", ID: f.req.Sender}
			}
			f.bind(t)
			var reply string
			if kind == "private" {
				r, input, task := infoFlowNotifyInput(t, f, f.req.Sender, []string{"alice", "atall"})
				var users []string
				dbfx.QueryRow(t, `SELECT notify_users FROM infoflow_issue_source WHERE comment_id=$1`, input.CommentID).Scan(&users)
				wantInfoFlowNotifyUsers(t, users, []string{})
				r.NotifyUsers = nil
				if retry := infoFlowCall(t, f.h, r, 200); retry.CommentID != input.CommentID {
					t.Fatal("ignored private list changed hash")
				}
				reply = infoFlowAgentReply(t, f, f.issue, input.CommentID, task, "private reply")
			} else if kind == "unattributed-agent" {
				reply = dbfx.Comment(t, f.issue, "unrelated output", testutil.Cols{"author_type": "agent", "author_id": f.req.AgentID})
			} else {
				reply = dbfx.Comment(t, f.issue, "native platform input", testutil.Cols{"author_id": f.owner})
				if kind == "web-agent" {
					task := dbfx.Task(t, f.req.AgentID, testutil.Cols{"issue_id": f.issue, "trigger_comment_id": reply, "runtime_id": f.runtime})
					reply = infoFlowAgentReply(t, f, f.issue, reply, task, "web-derived agent reply")
				}
			}
			found := false
			for range 3 {
				got := infoFlowPoll(t, f, 20)
				if len(got) == 0 {
					break
				}
				for _, d := range got {
					wantInfoFlowNotifyUsers(t, d.NotifyUsers, []string{})
					found = found || d.CommentID == reply
					infoFlowAck(t, f, d.ID, nil, 200)
				}
			}
			if found != (kind != "unattributed-agent") {
				t.Fatalf("unexpected delivery eligibility %s: %v", kind, found)
			}
		})
	}
}

func TestInfoFlowNotifyMigrationRoundTrip(t *testing.T) {
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	schema := "notify_migration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = tx.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SET LOCAL search_path TO `+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `CREATE TABLE infoflow_issue_source(id text,sender text,request_hash text); INSERT INTO infoflow_issue_source VALUES('old','Alice','legacy-hash'); CREATE TABLE infoflow_issue_delivery(id text); INSERT INTO infoflow_issue_delivery VALUES('old')`); err != nil {
		t.Fatal(err)
	}
	run := func(direction string) {
		t.Helper()
		data, e := os.ReadFile(filepath.Join("..", "..", "..", "..", "migrations", "521_infoflow_issue_notify_users."+direction+".sql"))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(ctx, string(data)); e != nil {
			t.Fatal(e)
		}
	}
	for range 2 {
		run("up")
	}
	var users []string
	var hash string
	if err = tx.QueryRow(ctx, `SELECT notify_users,request_hash FROM infoflow_issue_source WHERE id='old'`).Scan(&users, &hash); err != nil || hash != "legacy-hash" {
		t.Fatalf("legacy row changed: %q %v", hash, err)
	}
	wantInfoFlowNotifyUsers(t, users, []string{})
	if err = tx.QueryRow(ctx, `SELECT notify_users FROM infoflow_issue_delivery WHERE id='old'`).Scan(&users); err != nil || users != nil {
		t.Fatalf("legacy delivery must keep a NULL snapshot: %v %v", users, err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO infoflow_issue_delivery(id) VALUES('new'); INSERT INTO infoflow_issue_delivery(id,notify_users) VALUES('empty','{}')`); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT notify_users FROM infoflow_issue_delivery WHERE id='new'`).Scan(&users); err != nil || users != nil {
		t.Fatalf("absent snapshot must default to NULL: %v %v", users, err)
	}
	if err = tx.QueryRow(ctx, `SELECT notify_users FROM infoflow_issue_delivery WHERE id='empty'`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	wantInfoFlowNotifyUsers(t, users, []string{})
	if _, err = tx.Exec(ctx, `INSERT INTO infoflow_issue_source(id,sender,request_hash) VALUES('new','Bob','new-hash')`); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT notify_users FROM infoflow_issue_source WHERE id='new'`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	wantInfoFlowNotifyUsers(t, users, []string{})
	run("down")
	run("down")
	run("up")
	if _, err = tx.Exec(ctx, `DROP TABLE infoflow_issue_source; DROP TABLE infoflow_issue_delivery`); err != nil {
		t.Fatal(err)
	}
	run("up") // conditional objects may be absent
	run("down")
}
