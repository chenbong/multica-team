package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type transferFixture struct{ issue, source, target, actor pgtype.UUID }

func newTransferFixture(t *testing.T) transferFixture {
	t.Helper()
	if testHandler == nil {
		t.Fatal("isolated database required")
	}
	target := dbfx.Workspace(t, "Transfer target", "transfer-"+uuid.NewString(), testutil.Cols{"issue_prefix": "DST"})
	dbfx.Member(t, target, testUserID, "member")
	// Fixture helpers insert workspaces directly, bypassing catalog setup in
	// the application workspace-creation flow.
	dbfx.Exec(t, `INSERT INTO issue_status(workspace_id,key,name,category,color,position) VALUES($1,'cancelled','Cancelled','closed','#808080',0),($2,'cancelled','Cancelled','closed','#808080',0) ON CONFLICT DO NOTHING`, testWorkspaceID, target)
	id := dbfx.Issue(t, "Transfer fixture", testutil.Cols{"status": "cancelled", "description": "Keep this description"})
	return transferFixture{parseUUID(id), parseUUID(testWorkspaceID), parseUUID(target), parseUUID(testUserID)}
}
func (f transferFixture) run(req issueTransferRequest, apply bool) (*issueTransferPlan, error) {
	return testHandler.runIssueTransfer(context.Background(), f.issue, f.source, f.target, f.actor, req, apply)
}
func expectTransferCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *issueTransferFailure
	if !errors.As(err, &e) || e.code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}

func TestIssueTransferPreservesHistory(t *testing.T) {
	f := newTransferFixture(t)
	id := uuidToString(f.issue)
	comment := dbfx.Comment(t, id, "Preserved comment")
	agent := dbfx.Agent(t, "Historical transfer agent", testRuntimeID)
	task := dbfx.Task(t, agent, testutil.Cols{"issue_id": id, "runtime_id": testRuntimeID, "status": "completed", "completed_at": testutil.Raw("now()"), "result": `{"text":"historical result"}`})
	dbfx.Insert(t, "task_token", testutil.Cols{"task_id": task, "agent_id": agent, "workspace_id": testWorkspaceID, "user_id": testUserID, "token_hash": uuid.NewString(), "expires_at": testutil.Raw("now()+interval '1 hour'")})
	dbfx.Insert(t, "task_message", testutil.Cols{"task_id": task, "seq": 1, "type": "text", "content": "Preserved transcript"})
	dbfx.Insert(t, "comment_reaction", testutil.Cols{"comment_id": comment, "workspace_id": testWorkspaceID, "actor_type": "member", "actor_id": testUserID, "emoji": "ok"})
	p, err := f.run(issueTransferRequest{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Blockers) > 0 {
		t.Fatalf("blockers: %v", p.Blockers)
	}
	if p.Comments != 1 || p.Runs != 1 || p.Messages != 1 {
		t.Fatalf("bad preview: %+v", p)
	}
	var taskBefore, messagesBefore string
	testPool.QueryRow(context.Background(), `SELECT md5(to_jsonb(t)::text) FROM agent_task_queue t WHERE id=$1`, task).Scan(&taskBefore)
	testPool.QueryRow(context.Background(), `SELECT md5(jsonb_agg(to_jsonb(t))::text) FROM task_message t WHERE task_id=$1`, task).Scan(&messagesBefore)
	result, err := f.run(issueTransferRequest{Fingerprint: p.Fingerprint}, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Identifier != "DST-1" || !strings.HasSuffix(result.Path, "/issues/DST-1") {
		t.Fatalf("bad result %+v", result)
	}
	i, err := testHandler.Queries.GetIssue(context.Background(), f.issue)
	if err != nil || i.WorkspaceID != f.target || i.Status != "cancelled" || i.Description.String != "Keep this description" {
		t.Fatalf("issue changed unexpectedly %+v %v", i, err)
	}
	var body, workspace, after string
	testPool.QueryRow(context.Background(), `SELECT content,workspace_id FROM comment WHERE id=$1`, comment).Scan(&body, &workspace)
	if body != "Preserved comment" || workspace != uuidToString(f.target) {
		t.Fatal("comment was not preserved")
	}
	testPool.QueryRow(context.Background(), `SELECT md5(to_jsonb(t)::text) FROM agent_task_queue t WHERE id=$1`, task).Scan(&after)
	if after != taskBefore {
		t.Fatal("task changed")
	}
	testPool.QueryRow(context.Background(), `SELECT md5(jsonb_agg(to_jsonb(t))::text) FROM task_message t WHERE task_id=$1`, task).Scan(&after)
	if after != messagesBefore {
		t.Fatal("transcript changed")
	}
	var count int
	testPool.QueryRow(context.Background(), `SELECT count(*) FROM activity_log WHERE issue_id=$1 AND action='workspace_changed'`, f.issue).Scan(&count)
	if count != 1 {
		t.Fatal("missing audit event")
	}
	testPool.QueryRow(context.Background(), `SELECT count(*) FROM task_token WHERE task_id=$1`, task).Scan(&count)
	if count != 0 {
		t.Fatal("old host credential retained")
	}
	_, err = f.run(issueTransferRequest{Fingerprint: p.Fingerprint}, true)
	expectTransferCode(t, err, "transfer_changed")
}

func TestIssueTransferAuthorization(t *testing.T) {
	for _, role := range []string{"owner", "admin", "member"} {
		t.Run(role, func(t *testing.T) {
			f := newTransferFixture(t)
			dbfx.Exec(t, `UPDATE member SET role=$1 WHERE workspace_id=$2 AND user_id=$3`, role, f.source, f.actor)
			t.Cleanup(func() {
				testPool.Exec(context.Background(), `UPDATE member SET role='owner' WHERE workspace_id=$1 AND user_id=$2`, f.source, f.actor)
			})
			_, err := f.run(issueTransferRequest{}, false)
			if role == "member" {
				expectTransferCode(t, err, "transfer_forbidden")
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("target membership revoked", func(t *testing.T) {
		f := newTransferFixture(t)
		p, err := f.run(issueTransferRequest{}, false)
		if err != nil {
			t.Fatal(err)
		}
		dbfx.Exec(t, `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, f.target, f.actor)
		_, err = f.run(issueTransferRequest{Fingerprint: p.Fingerprint}, true)
		expectTransferCode(t, err, "transfer_forbidden")
	})
	for _, source := range []string{"task_token", "cloud_pat"} {
		t.Run(source, func(t *testing.T) {
			f := newTransferFixture(t)
			r := withURLParam(newRequest("POST", "/", map[string]any{"target_workspace_id": uuidToString(f.target)}), "id", uuidToString(f.issue))
			r.Header.Set("X-Actor-Source", source)
			w := httptest.NewRecorder()
			testHandler.PreviewIssueTransfer(w, r)
			if w.Code != 403 {
				t.Fatalf("machine got %d", w.Code)
			}
		})
	}
}

func TestIssueTransferBlocksUnsupportedData(t *testing.T) {
	for _, status := range []string{"queued", "running", "deferred", "waiting_local_directory"} {
		t.Run(status, func(t *testing.T) {
			f := newTransferFixture(t)
			a := dbfx.Agent(t, "Busy transfer agent", testRuntimeID)
			dbfx.Task(t, a, testutil.Cols{"issue_id": uuidToString(f.issue), "runtime_id": testRuntimeID, "status": status})
			p, err := f.run(issueTransferRequest{}, false)
			if err != nil {
				t.Fatal(err)
			}
			if !containsTransferBlocker(p, "active_or_linked_runs") {
				t.Fatal("active run not blocked")
			}
			_, err = f.run(issueTransferRequest{Fingerprint: p.Fingerprint}, true)
			expectTransferCode(t, err, "transfer_blocked")
		})
	}
	for _, test := range []struct {
		name, code string
		setup      func(*testing.T, transferFixture)
	}{
		{"child", "children_or_dependencies", func(t *testing.T, f transferFixture) {
			dbfx.Issue(t, "Child", testutil.Cols{"parent_issue_id": uuidToString(f.issue)})
		}},
		{"metadata", "custom_context", func(t *testing.T, f transferFixture) {
			dbfx.Exec(t, `UPDATE issue SET metadata='{"custom":"data"}' WHERE id=$1`, f.issue)
		}},
		{"pin", "labels_pins_or_prs", func(t *testing.T, f transferFixture) {
			dbfx.Insert(t, "pinned_item", testutil.Cols{"workspace_id": testWorkspaceID, "user_id": testUserID, "item_type": "issue", "item_id": uuidToString(f.issue), "position": 0})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newTransferFixture(t)
			test.setup(t, f)
			p, err := f.run(issueTransferRequest{}, false)
			if err != nil {
				t.Fatal(err)
			}
			if !containsTransferBlocker(p, test.code) {
				t.Fatalf("missing %s: %v", test.code, p.Blockers)
			}
		})
	}
}
func containsTransferBlocker(p *issueTransferPlan, code string) bool {
	for _, b := range p.Blockers {
		if b == code {
			return true
		}
	}
	return false
}

func TestIssueTransferRequiresFreshPreviewAndUnassign(t *testing.T) {
	f := newTransferFixture(t)
	a := dbfx.Agent(t, "Assigned transfer agent", testRuntimeID)
	dbfx.Exec(t, `UPDATE issue SET assignee_type='agent',assignee_id=$2 WHERE id=$1`, f.issue, a)
	p, err := f.run(issueTransferRequest{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Unassign {
		t.Fatal("missing unassign warning")
	}
	_, err = f.run(issueTransferRequest{Fingerprint: p.Fingerprint}, true)
	expectTransferCode(t, err, "transfer_unassign_required")
	dbfx.Comment(t, uuidToString(f.issue), "New comment after preview")
	_, err = f.run(issueTransferRequest{Fingerprint: p.Fingerprint, ConfirmUnassign: true}, true)
	expectTransferCode(t, err, "transfer_changed")
	p, err = f.run(issueTransferRequest{}, false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.run(issueTransferRequest{Fingerprint: p.Fingerprint, ConfirmUnassign: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	i, _ := testHandler.Queries.GetIssue(context.Background(), f.issue)
	if i.AssigneeID.Valid || i.AssigneeType.Valid {
		t.Fatal("cross-workspace assignee retained")
	}
}

func TestIssueTransferStatusMappingAndNumberAllocation(t *testing.T) {
	f := newTransferFixture(t)
	dbfx.Exec(t, `UPDATE issue_status SET archived_at=now() WHERE workspace_id=$1 AND key='cancelled'`, f.target)
	dbfx.Insert(t, "issue_status", testutil.Cols{"workspace_id": uuidToString(f.target), "key": "abandoned", "name": "Abandoned", "category": "closed", "color": "#808080", "position": 99})
	p, err := f.run(issueTransferRequest{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !containsTransferBlocker(p, "status_mapping_required") {
		t.Fatal("missing state warning")
	}
	p, err = f.run(issueTransferRequest{TargetStatus: "abandoned"}, false)
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Issue(t, "Existing destination issue", testutil.Cols{"workspace_id": uuidToString(f.target), "number": 8})
	result, err := f.run(issueTransferRequest{TargetStatus: "abandoned", Fingerprint: p.Fingerprint}, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Identifier != "DST-9" {
		t.Fatalf("number collision %s", result.Identifier)
	}
}

func TestIssueTransferEndpointPreview(t *testing.T) {
	f := newTransferFixture(t)
	w := httptest.NewRecorder()
	r := withURLParam(newRequest("POST", "/", map[string]any{"target_workspace_id": uuidToString(f.target)}), "id", uuidToString(f.issue))
	testHandler.PreviewIssueTransfer(w, r)
	if w.Code != 200 {
		t.Fatalf("preview: %d %s", w.Code, w.Body.String())
	}
	var p issueTransferPlan
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil || p.Fingerprint == "" {
		t.Fatal("invalid preview")
	}
}

type transferFailCommit struct{ pgx.Tx }

func (t transferFailCommit) Commit(context.Context) error {
	return errors.New("injected commit failure")
}

type transferFailStarter struct{ pool *pgxpool.Pool }

func (s transferFailStarter) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return transferFailCommit{tx}, nil
}

func TestIssueTransferRollbackAndNoDispatchEvents(t *testing.T) {
	f := newTransferFixture(t)
	dbfx.Comment(t, uuidToString(f.issue), "unchanged")
	p, err := f.run(issueTransferRequest{}, false)
	if err != nil {
		t.Fatal(err)
	}
	h := *testHandler
	h.TxStarter = transferFailStarter{testPool}
	_, err = h.runIssueTransfer(context.Background(), f.issue, f.source, f.target, f.actor, issueTransferRequest{Fingerprint: p.Fingerprint}, true)
	if err == nil {
		t.Fatal("expected injected failure")
	}
	i, _ := h.Queries.GetIssue(context.Background(), f.issue)
	if i.WorkspaceID != f.source || i.Revision != p.Revision {
		t.Fatal("partial issue update escaped rollback")
	}
	var counter, count int
	testPool.QueryRow(context.Background(), `SELECT issue_counter FROM workspace WHERE id=$1`, f.target).Scan(&counter)
	if counter != 0 {
		t.Fatal("number allocation escaped rollback")
	}
	testPool.QueryRow(context.Background(), `SELECT count(*) FROM comment WHERE issue_id=$1 AND workspace_id=$2`, f.issue, f.source).Scan(&count)
	if count != 1 {
		t.Fatal("comment escaped rollback")
	}
	h.TxStarter = testHandler.TxStarter
	h.Bus = events.New()
	var notices, dispatch int
	h.Bus.Subscribe("issue:workspace_changed", func(e events.Event) {
		notices++
		if e.WorkspaceID != uuidToString(f.source) && e.WorkspaceID != uuidToString(f.target) {
			t.Error("wrong audience")
		}
	})
	for _, event := range []string{"issue:created", "issue:updated", "issue:deleted", "task:created"} {
		h.Bus.Subscribe(event, func(events.Event) { dispatch++ })
	}
	w := httptest.NewRecorder()
	r := withURLParam(newRequest("POST", "/", map[string]any{"target_workspace_id": uuidToString(f.target), "fingerprint": p.Fingerprint}), "id", uuidToString(f.issue))
	h.TransferIssue(w, r)
	if w.Code != 200 || notices != 2 || dispatch != 0 {
		t.Fatalf("status=%d notices=%d dispatch=%d response=%s", w.Code, notices, dispatch, w.Body.String())
	}
}

func TestIssueTransferConcurrentWriterFailsClosed(t *testing.T) {
	f := newTransferFixture(t)
	p, err := f.run(issueTransferRequest{}, false)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := testPool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback(context.Background())
	if _, err = writer.Exec(context.Background(), `LOCK TABLE attachment IN ROW EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	_, err = f.run(issueTransferRequest{Fingerprint: p.Fingerprint}, true)
	if err == nil {
		t.Fatal("concurrent relationship writer was not fenced")
	}
	i, _ := testHandler.Queries.GetIssue(context.Background(), f.issue)
	if i.WorkspaceID != f.source {
		t.Fatal("issue moved despite contention")
	}
}

func TestIssueTransferLateEnqueueDenied(t *testing.T) {
	f := newTransferFixture(t)
	agent := dbfx.Agent(t, "Old host", testRuntimeID)
	p, err := f.run(issueTransferRequest{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.run(issueTransferRequest{Fingerprint: p.Fingerprint}, true); err != nil {
		t.Fatal(err)
	}
	params := db.CreateAgentTaskParams{ID: parseUUID(uuid.NewString()), IssueID: f.issue, AgentID: parseUUID(agent), RuntimeID: parseUUID(testRuntimeID)}
	_, err = testHandler.Queries.CreateAgentTask(context.Background(), params)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("stale source host enqueue not refused: %v", err)
	}
	// Normal issue-less tasks retain the prior contract.
	params.IssueID = pgtype.UUID{}
	created, err := testHandler.Queries.CreateAgentTask(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Cleanup(t, `DELETE FROM agent_task_queue WHERE id=$1`, created.ID)
	// A late source-workspace subscription cannot expose future destination
	// activity to the old agent after the issue has moved.
	count, err := testHandler.Queries.AddIssueSubscriber(context.Background(), db.AddIssueSubscriberParams{IssueID: f.issue, UserType: "agent", UserID: parseUUID(agent), Reason: "manual"})
	if err != nil || count != 0 {
		t.Fatalf("late source subscription accepted: %d %v", count, err)
	}
}

func requireInfoFlowTransferSchema(t *testing.T) {
	t.Helper()
	var present bool
	dbfx.QueryRow(t, `SELECT to_regclass('infoflow_issue_binding') IS NOT NULL`).Scan(&present)
	if !present {
		t.Skip("InfoFlow overlay migrations are not installed")
	}
}

func infoFlowTransferBinding(t *testing.T, f transferFixture, kind string) (string, string, string) {
	t.Helper()
	agent := dbfx.Agent(t, "Transfer-bound agent", testRuntimeID)
	generation := uuid.NewString()
	binding := dbfx.Insert(t, "infoflow_issue_binding", testutil.Cols{
		"id": uuid.NewString(), "robot_id": "transfer-" + uuid.NewString(),
		"workspace_id": uuidToString(f.source), "agent_id": agent,
		"conversation_kind": kind, "conversation_id": "transfer-conversation-" + uuid.NewString(),
		"issue_id": uuidToString(f.issue), "binder_id": uuidToString(f.actor), "generation": generation,
	})
	dbfx.Cleanup(t, `DELETE FROM infoflow_issue_source WHERE binding_id=$1`, binding)
	dbfx.Cleanup(t, `DELETE FROM infoflow_issue_delivery WHERE binding_id=$1`, binding)
	return binding, generation, agent
}

func TestIssueTransferBlocksActiveInfoFlowBinding(t *testing.T) {
	requireInfoFlowTransferSchema(t)
	for _, kind := range []string{"private", "group"} {
		t.Run(kind, func(t *testing.T) {
			f := newTransferFixture(t)
			before, err := f.run(issueTransferRequest{}, false)
			if err != nil || len(before.Blockers) != 0 {
				t.Fatalf("initial preview: %+v %v", before, err)
			}
			binding, _, _ := infoFlowTransferBinding(t, f, kind)
			preview, err := f.run(issueTransferRequest{}, false)
			if err != nil || !containsTransferBlocker(preview, "infoflow_binding") {
				t.Fatalf("active %s binding absent from blockers: %+v %v", kind, preview, err)
			}
			// Apply must recheck even when the binding appeared after confirmation.
			_, err = f.run(issueTransferRequest{Fingerprint: before.Fingerprint}, true)
			expectTransferCode(t, err, "transfer_blocked")
			var issueWorkspace, bindingWorkspace, boundIssue string
			dbfx.QueryRow(t, `SELECT i.workspace_id::text,b.workspace_id::text,b.issue_id::text FROM issue i JOIN infoflow_issue_binding b ON b.id=$2 WHERE i.id=$1`, f.issue, binding).Scan(&issueWorkspace, &bindingWorkspace, &boundIssue)
			if issueWorkspace != uuidToString(f.source) || bindingWorkspace != issueWorkspace || boundIssue != uuidToString(f.issue) {
				t.Fatal("blocked transfer changed the issue or its binding")
			}
		})
	}
}

func TestIssueTransferPreservesInactiveInfoFlowHistory(t *testing.T) {
	requireInfoFlowTransferSchema(t)
	f := newTransferFixture(t)
	binding, generation, agent := infoFlowTransferBinding(t, f, "group")
	comment := dbfx.Comment(t, uuidToString(f.issue), "Old routed answer", testutil.Cols{"author_type": "agent", "author_id": agent})
	source := dbfx.Insert(t, "infoflow_issue_source", testutil.Cols{
		"id": uuid.NewString(), "binding_id": binding, "generation": generation,
		"message_id": "historical-message", "sender": "historical-sender", "user_id": uuidToString(f.actor),
		"request_hash": "fixture", "issue_id": uuidToString(f.issue), "response": testutil.Raw("'{}'::jsonb"),
	})
	dbfx.Insert(t, "infoflow_issue_delivery", testutil.Cols{
		"id": uuid.NewString(), "binding_id": binding, "generation": generation,
		"source_id": source, "comment_id": comment, "content": "Old routed answer",
	})
	dbfx.Exec(t, `UPDATE infoflow_issue_binding SET issue_id=NULL,binder_id=NULL,generation=$2 WHERE id=$1`, binding, uuid.NewString())
	const historySQL = `SELECT md5(jsonb_build_array(
		(SELECT to_jsonb(b) FROM infoflow_issue_binding b WHERE id=$1),
		(SELECT jsonb_agg(to_jsonb(s) ORDER BY id) FROM infoflow_issue_source s WHERE binding_id=$1),
		(SELECT jsonb_agg(to_jsonb(d) ORDER BY id) FROM infoflow_issue_delivery d WHERE binding_id=$1))::text)`
	var before, after string
	dbfx.QueryRow(t, historySQL, binding).Scan(&before)
	p, err := f.run(issueTransferRequest{}, false)
	if err != nil || len(p.Blockers) != 0 {
		t.Fatalf("inactive history should not block transfer: %+v %v", p, err)
	}
	if _, err = f.run(issueTransferRequest{Fingerprint: p.Fingerprint}, true); err != nil {
		t.Fatal(err)
	}
	dbfx.QueryRow(t, historySQL, binding).Scan(&after)
	if before != after {
		t.Fatal("transfer rewrote immutable InfoFlow binding/source/delivery history")
	}
}

func TestIssueTransferFencesConcurrentInfoFlowBindingWriter(t *testing.T) {
	requireInfoFlowTransferSchema(t)
	f := newTransferFixture(t)
	p, err := f.run(issueTransferRequest{}, false)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := testPool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback(context.Background())
	if _, err = writer.Exec(context.Background(), `LOCK TABLE infoflow_issue_binding IN ROW EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	if _, err = f.run(issueTransferRequest{Fingerprint: p.Fingerprint}, true); err == nil {
		t.Fatal("transfer ignored the in-flight InfoFlow binding writer")
	}
	i, err := testHandler.Queries.GetIssue(context.Background(), f.issue)
	if err != nil || i.WorkspaceID != f.source {
		t.Fatalf("issue moved despite binding contention: %+v %v", i, err)
	}
}

func TestIssueTransferInfoFlowRejectsStaleSourceWrites(t *testing.T) {
	requireInfoFlowTransferSchema(t)
	f := newTransferFixture(t)
	p, err := f.run(issueTransferRequest{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.run(issueTransferRequest{Fingerprint: p.Fingerprint}, true); err != nil {
		t.Fatal(err)
	}
	// Simulate a stale mapping left by an older deployment. Admission must
	// recheck the issue's current workspace, even if this fixture is reachable.
	binding, _, agent := infoFlowTransferBinding(t, f, "group")
	var robot, conversation string
	dbfx.QueryRow(t, `SELECT robot_id,conversation_id FROM infoflow_issue_binding WHERE id=$1`, binding).Scan(&robot, &conversation)
	sender := "transfer-sender-" + uuid.NewString()
	user := dbfx.User(t, "Stale source sender", sender+"@example.test")
	dbfx.Member(t, uuidToString(f.source), user, "admin")
	token := "transfer-fixture-" + uuid.NewString()
	t.Setenv("INFOFLOW_ISSUE_RELAY_TOKEN", token)
	t.Setenv("INFOFLOW_ISSUE_IDENTITY_DOMAIN", "example.test")
	for _, operation := range []string{"bind", "message"} {
		r := newRequest("POST", "/api/integrations/infoflow/issue-relay", map[string]any{
			"robot_id": robot, "workspace_id": uuidToString(f.source), "agent_id": agent,
			"conversation": map[string]string{"kind": "group", "id": conversation},
			"sender":       sender, "message_id": "stale-" + operation, "operation": operation,
			"issue_ref": uuidToString(f.issue), "content": "Must not reach the moved issue",
		})
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		testHandler.InfoFlowIssueRelay(w, r)
		var result infoFlowRelayResponse
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 || !result.Handled || result.Silent || result.Notice == "" || result.CommentID != "" || result.Binding != nil {
			t.Fatalf("stale %s exposed a binding or comment: status=%d body=%s error=%v", operation, w.Code, w.Body.String(), err)
		}
		if got := dbfx.Count(t, `SELECT count(*) FROM infoflow_issue_source WHERE binding_id=$1`, binding); got != 0 {
			t.Fatalf("stale %s wrote %d source receipts", operation, got)
		}
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM comment WHERE issue_id=$1`, f.issue); got != 0 {
		t.Fatalf("stale source wrote %d comments in the moved issue", got)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1`, f.issue); got != 0 {
		t.Fatalf("stale source queued %d tasks", got)
	}
}

type transferPauseIssueLoadStarter struct {
	pool            *pgxpool.Pool
	loaded, release chan struct{}
}

func (s transferPauseIssueLoadStarter) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &transferPauseIssueLoadTx{Tx: tx, loaded: s.loaded, release: s.release}, nil
}

type transferPauseIssueLoadTx struct {
	pgx.Tx
	loaded, release chan struct{}
	paused          bool
}

func (tx *transferPauseIssueLoadTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	row := tx.Tx.QueryRow(ctx, sql, args...)
	if !tx.paused && strings.HasPrefix(sql, "-- name: GetIssueInWorkspace :one") {
		tx.paused = true
		return transferPauseIssueLoadRow{Row: row, ctx: ctx, loaded: tx.loaded, release: tx.release}
	}
	return row
}

type transferPauseIssueLoadRow struct {
	pgx.Row
	ctx             context.Context
	loaded, release chan struct{}
}

func (row transferPauseIssueLoadRow) Scan(out ...any) error {
	if err := row.Row.Scan(out...); err != nil {
		return err
	}
	close(row.loaded)
	select {
	case <-row.release:
		return nil
	case <-row.ctx.Done():
		return row.ctx.Err()
	}
}

func TestIssueTransferInfoFlowRebindRace(t *testing.T) {
	requireInfoFlowTransferSchema(t)
	f := newTransferFixture(t)
	binding, _, agent := infoFlowTransferBinding(t, f, "group")
	dbfx.Exec(t, `UPDATE infoflow_issue_binding SET issue_id=NULL,binder_id=NULL WHERE id=$1`, binding)
	var robot, conversation string
	dbfx.QueryRow(t, `SELECT robot_id,conversation_id FROM infoflow_issue_binding WHERE id=$1`, binding).Scan(&robot, &conversation)
	sender := "rebind-sender-" + uuid.NewString()
	user := dbfx.User(t, "Rebind sender", sender+"@example.test")
	dbfx.Member(t, uuidToString(f.source), user, "admin")
	token := "rebind-fixture-" + uuid.NewString()
	t.Setenv("INFOFLOW_ISSUE_RELAY_TOKEN", token)
	t.Setenv("INFOFLOW_ISSUE_IDENTITY_DOMAIN", "example.test")
	p, err := f.run(issueTransferRequest{}, false)
	if err != nil || len(p.Blockers) != 0 {
		t.Fatalf("unbound preview: %+v %v", p, err)
	}
	loaded, release := make(chan struct{}), make(chan struct{})
	h := *testHandler
	h.TxStarter = transferPauseIssueLoadStarter{testPool, loaded, release}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r := newRequest("POST", "/api/integrations/infoflow/issue-relay", map[string]any{
		"robot_id": robot, "workspace_id": uuidToString(f.source), "agent_id": agent,
		"conversation": map[string]string{"kind": "group", "id": conversation},
		"sender":       sender, "message_id": "racing-bind", "operation": "bind", "issue_ref": uuidToString(f.issue),
	}).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); h.InfoFlowIssueRelay(w, r) }()
	defer func() {
		cancel()
		<-done
	}()
	select {
	case <-loaded:
	case <-done:
		t.Fatalf("bind returned before the issue-read barrier: %d %s", w.Code, w.Body.String())
	case <-ctx.Done():
		t.Fatal("bind did not reach the issue-read barrier")
	}
	_, moveErr := f.run(issueTransferRequest{Fingerprint: p.Fingerprint}, true)
	close(release)
	<-done
	if moveErr != nil {
		// Pinning the source issue before reading it is also safe: transfer's
		// NOWAIT fence refuses the move and the bind stays in its workspace.
		var lockErr *pgconn.PgError
		if !errors.As(moveErr, &lockErr) || lockErr.Code != "55P03" {
			t.Fatalf("unexpected transfer failure: %v", moveErr)
		}
		return
	}
	var result infoFlowRelayResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 || !result.Handled || result.Binding != nil {
		t.Fatalf("stale rebind response: %d %s %v", w.Code, w.Body.String(), err)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM infoflow_issue_binding WHERE id=$1 AND issue_id IS NOT NULL`, binding); got != 0 {
		t.Fatal("bind reactivated the old-workspace route after transfer committed")
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM infoflow_issue_source WHERE binding_id=$1`, binding); got != 0 {
		t.Fatalf("bind wrote %d stale source receipts after transfer committed", got)
	}
}
