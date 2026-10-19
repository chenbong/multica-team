package handler

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func transferSupplementRun(t *testing.T, f transferFixture, status string, capability bool) string {
	t.Helper()
	agent := dbfx.Agent(t, "Historical supplement agent", testRuntimeID)
	task := dbfx.Task(t, agent, testutil.Cols{"issue_id": uuidToString(f.issue), "runtime_id": testRuntimeID, "status": status})
	if capability {
		transferSupplementCapability(t, f, task)
	}
	return task
}

func transferSupplementCapability(t *testing.T, f transferFixture, task string) {
	t.Helper()
	dbfx.InsertNoID(t, "task_supplement_capability", testutil.Cols{
		"task_id": task, "workspace_id": uuidToString(f.source), "issue_id": uuidToString(f.issue),
		"capability": protocol.DaemonCapabilityTaskSupplementV1,
	}, "task_id=$1", task)
}

func TestIssueTransferSupplementTerminalCapabilityHistory(t *testing.T) {
	for _, status := range []string{"completed", "failed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			f := newTransferFixture(t)
			// Match the reported case: the issue may remain in progress even
			// though its execution has completed or been cancelled.
			dbfx.Exec(t, `INSERT INTO issue_status(workspace_id,key,name,category,color,position) VALUES($1,'in_progress','In Progress','started','#808080',1),($2,'in_progress','In Progress','started','#808080',1) ON CONFLICT DO NOTHING`, f.source, f.target)
			dbfx.Exec(t, `UPDATE issue SET status='in_progress' WHERE id=$1`, f.issue)
			task := transferSupplementRun(t, f, status, true)
			message := dbfx.Insert(t, "task_message", testutil.Cols{"task_id": task, "seq": 1, "type": "text", "content": "Preserved execution history"})
			var taskBefore, capabilityBefore, taskAfter, capabilityAfter string
			dbfx.QueryRow(t, `SELECT to_jsonb(t)::text FROM agent_task_queue t WHERE id=$1`, task).Scan(&taskBefore)
			dbfx.QueryRow(t, `SELECT (to_jsonb(c)-'workspace_id')::text FROM task_supplement_capability c WHERE task_id=$1`, task).Scan(&capabilityBefore)
			p, err := f.run(issueTransferRequest{}, false)
			if err != nil || len(p.Blockers) != 0 {
				t.Fatalf("terminal capability should not block: %+v %v", p, err)
			}
			if _, err = f.run(issueTransferRequest{Fingerprint: p.Fingerprint}, true); err != nil {
				t.Fatal(err)
			}
			var workspace string
			dbfx.QueryRow(t, `SELECT workspace_id FROM task_supplement_capability WHERE task_id=$1`, task).Scan(&workspace)
			if workspace != uuidToString(f.target) {
				t.Fatal("capability retained its source scope")
			}
			dbfx.QueryRow(t, `SELECT to_jsonb(t)::text FROM agent_task_queue t WHERE id=$1`, task).Scan(&taskAfter)
			dbfx.QueryRow(t, `SELECT (to_jsonb(c)-'workspace_id')::text FROM task_supplement_capability c WHERE task_id=$1`, task).Scan(&capabilityAfter)
			if taskAfter != taskBefore || capabilityAfter != capabilityBefore {
				t.Fatal("transfer rewrote execution or capability history")
			}
			if n := dbfx.Count(t, `SELECT count(*) FROM task_message WHERE id=$1 AND content='Preserved execution history'`, message); n != 1 {
				t.Fatal("execution transcript changed")
			}
			for _, scope := range []pgtype.UUID{f.source, f.target} {
				rows, err := testHandler.Queries.ListTaskSupplementMetadata(t.Context(), db.ListTaskSupplementMetadataParams{WorkspaceID: scope, TaskIds: []pgtype.UUID{parseUUID(task)}})
				if err != nil {
					t.Fatal(err)
				}
				want := 0
				if scope == f.target {
					want = 1
				}
				if len(rows) != want {
					t.Fatalf("metadata in wrong workspace: got %d want %d", len(rows), want)
				}
			}
			// An old daemon's workspace credential must not regain access to
			// the completed run after its owning issue changes workspace.
			r := withURLParam(newDaemonTokenRequest(http.MethodPost, "/claim", nil, uuidToString(f.source), "stale-transfer-fixture"), "taskId", task)
			testutil.Call(t, testHandler.ClaimTaskSupplement, r).Want(http.StatusNotFound)
		})
	}
}

func TestIssueTransferSupplementActiveAndReceiptGuards(t *testing.T) {
	for _, status := range []string{"queued", "dispatched", "waiting_local_directory", "deferred", "running"} {
		t.Run(status, func(t *testing.T) {
			f := newTransferFixture(t)
			transferSupplementRun(t, f, status, true)
			p, err := f.run(issueTransferRequest{}, false)
			if err != nil || !containsTransferBlocker(p, "active_or_linked_runs") {
				t.Fatalf("active run not blocked: %+v %v", p, err)
			}
			_, err = f.run(issueTransferRequest{Fingerprint: p.Fingerprint}, true)
			expectTransferCode(t, err, "transfer_blocked")
		})
	}
	// Actual supplement receipts keep their existing protection, including
	// late delivery reconciliation. This fix only relaxes pure capability data.
	for _, status := range []string{"pending", "delivering", "delivered", "failed"} {
		t.Run("receipt_"+status, func(t *testing.T) {
			f := newTransferFixture(t)
			task := transferSupplementRun(t, f, "cancelled", true)
			comment := dbfx.Comment(t, uuidToString(f.issue), "Existing additional input")
			dbfx.InsertNoID(t, "task_supplement", testutil.Cols{"task_id": task, "workspace_id": uuidToString(f.source), "issue_id": uuidToString(f.issue), "comment_id": comment, "author_id": testUserID, "client_request_id": uuid.NewString(), "status": status}, "task_id=$1 AND comment_id=$2", task, comment)
			p, err := f.run(issueTransferRequest{}, false)
			if err != nil || !containsTransferBlocker(p, "automation_or_sources") {
				t.Fatalf("receipt protection lost: %+v %v", p, err)
			}
			_, err = f.run(issueTransferRequest{Fingerprint: p.Fingerprint}, true)
			expectTransferCode(t, err, "transfer_blocked")
			if n := dbfx.Count(t, `SELECT count(*) FROM task_supplement WHERE task_id=$1 AND workspace_id=$2 AND status=$3`, task, f.source, status); n != 1 {
				t.Fatal("blocked transfer altered the receipt")
			}
		})
	}
}

func TestIssueTransferSupplementMalformedCapabilitiesFailClosed(t *testing.T) {
	for _, kind := range []string{"missing_task", "foreign_task_issue", "foreign_capability_issue", "foreign_workspace"} {
		t.Run(kind, func(t *testing.T) {
			f := newTransferFixture(t)
			task := transferSupplementRun(t, f, "completed", false)
			if kind == "missing_task" {
				task = uuid.NewString()
			}
			transferSupplementCapability(t, f, task)
			switch kind {
			case "foreign_task_issue":
				other := dbfx.Issue(t, "Other task owner")
				dbfx.Exec(t, `UPDATE agent_task_queue SET issue_id=$2 WHERE id=$1`, task, other)
			case "foreign_capability_issue":
				other := dbfx.Issue(t, "Other capability owner")
				dbfx.Exec(t, `UPDATE task_supplement_capability SET issue_id=$2 WHERE task_id=$1`, task, other)
			case "foreign_workspace":
				dbfx.Exec(t, `UPDATE task_supplement_capability SET workspace_id=$2 WHERE task_id=$1`, task, f.target)
			}
			p, err := f.run(issueTransferRequest{}, false)
			if err != nil || !containsTransferBlocker(p, "automation_or_sources") {
				t.Fatalf("malformed capability admitted: %+v %v", p, err)
			}
			_, err = f.run(issueTransferRequest{Fingerprint: p.Fingerprint}, true)
			expectTransferCode(t, err, "transfer_blocked")
		})
	}
}

func TestIssueTransferSupplementFingerprintIncludesCapability(t *testing.T) {
	f := newTransferFixture(t)
	task := transferSupplementRun(t, f, "cancelled", false)
	before, err := f.run(issueTransferRequest{}, false)
	if err != nil {
		t.Fatal(err)
	}
	transferSupplementCapability(t, f, task)
	after, err := f.run(issueTransferRequest{}, false)
	if err != nil || len(after.Blockers) != 0 {
		t.Fatalf("new terminal capability blocked: %+v %v", after, err)
	}
	if before.Fingerprint == after.Fingerprint {
		t.Fatal("capability change did not invalidate preview")
	}
	_, err = f.run(issueTransferRequest{Fingerprint: before.Fingerprint}, true)
	expectTransferCode(t, err, "transfer_changed")
	if n := dbfx.Count(t, `SELECT count(*) FROM issue WHERE id=$1 AND workspace_id=$2`, f.issue, f.source); n != 1 {
		t.Fatal("stale preview moved issue")
	}
}

func TestIssueTransferSupplementReceiptAliasesRemainBlocked(t *testing.T) {
	for _, kind := range []string{"task_reference", "comment_reference"} {
		t.Run(kind, func(t *testing.T) {
			f := newTransferFixture(t)
			task := transferSupplementRun(t, f, "cancelled", true)
			other := dbfx.Issue(t, "Other receipt owner")
			comment := dbfx.Comment(t, other, "Foreign receipt input")
			if kind == "comment_reference" {
				agent := dbfx.Agent(t, "Other receipt agent", testRuntimeID)
				task = dbfx.Task(t, agent, testutil.Cols{"issue_id": other, "runtime_id": testRuntimeID, "status": "cancelled"})
				comment = dbfx.Comment(t, uuidToString(f.issue), "Current issue input")
			}
			dbfx.InsertNoID(t, "task_supplement", testutil.Cols{"task_id": task, "workspace_id": uuidToString(f.source), "issue_id": other, "comment_id": comment, "author_id": testUserID, "client_request_id": uuid.NewString(), "status": "pending"}, "task_id=$1 AND comment_id=$2", task, comment)
			p, err := f.run(issueTransferRequest{}, false)
			if err != nil || !containsTransferBlocker(p, "automation_or_sources") {
				t.Fatalf("indirect receipt association bypassed protection: %+v %v", p, err)
			}
			_, err = f.run(issueTransferRequest{Fingerprint: p.Fingerprint}, true)
			expectTransferCode(t, err, "transfer_blocked")
		})
	}
}

func TestIssueTransferSupplementConcurrentWritersFailClosed(t *testing.T) {
	for _, table := range []string{"task_supplement", "task_supplement_capability"} {
		t.Run(table, func(t *testing.T) {
			f := newTransferFixture(t)
			p, err := f.run(issueTransferRequest{}, false)
			if err != nil {
				t.Fatal(err)
			}
			writer, err := testPool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Rollback(context.Background())
			if _, err = writer.Exec(t.Context(), "LOCK TABLE "+table+" IN ROW EXCLUSIVE MODE"); err != nil {
				t.Fatal(err)
			}
			_, err = f.run(issueTransferRequest{Fingerprint: p.Fingerprint}, true)
			var lockError *pgconn.PgError
			if !errors.As(err, &lockError) || lockError.Code != "55P03" {
				t.Fatalf("expected supplementary table lock fence, got %v", err)
			}
			if n := dbfx.Count(t, `SELECT count(*) FROM issue WHERE id=$1 AND workspace_id=$2`, f.issue, f.source); n != 1 {
				t.Fatal("issue moved despite contention")
			}
		})
	}
}

func TestIssueTransferSupplementCapabilityRollback(t *testing.T) {
	f := newTransferFixture(t)
	task := transferSupplementRun(t, f, "completed", true)
	p, err := f.run(issueTransferRequest{}, false)
	if err != nil || len(p.Blockers) != 0 {
		t.Fatalf("preview blocked: %+v %v", p, err)
	}
	h := *testHandler
	h.TxStarter = transferFailStarter{testPool}
	_, err = h.runIssueTransfer(t.Context(), f.issue, f.source, f.target, f.actor, issueTransferRequest{Fingerprint: p.Fingerprint}, true)
	if err == nil {
		t.Fatal("expected commit failure")
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM task_supplement_capability WHERE task_id=$1 AND workspace_id=$2`, task, f.source); n != 1 {
		t.Fatal("capability scope escaped rollback")
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM issue WHERE id=$1 AND workspace_id=$2`, f.issue, f.source); n != 1 {
		t.Fatal("issue scope escaped rollback")
	}
}
