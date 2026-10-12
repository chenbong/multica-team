package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
