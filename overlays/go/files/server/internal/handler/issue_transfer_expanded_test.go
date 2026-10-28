package handler

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
	"net/http/httptest"
	"testing"
)

func TestIssueTransferTreePreservesContentAndLegacyManagers(t *testing.T) {
	f := newTransferFixture(t)
	ctx := context.Background()
	alice := managerTestUser(t, "Source-only task creator")
	targetOwner := dbfx.User(t, "Destination owner", "destination-"+uuid.NewString()+"@example.test")
	dbfx.Member(t, uuidToString(f.target), targetOwner, "owner")
	dbfx.Exec(t, `INSERT INTO issue_status(workspace_id,key,name,category,color,position) VALUES($1,'todo','Todo','unstarted','#808080',1),($2,'todo','Todo','unstarted','#808080',1) ON CONFLICT DO NOTHING`, f.source, f.target)
	outside := dbfx.Issue(t, "External parent remains")
	child := dbfx.Issue(t, "Child content remains", testutil.Cols{"parent_issue_id": uuidToString(f.issue), "creator_id": alice})
	grandchild := dbfx.Issue(t, "Grandchild content remains", testutil.Cols{"parent_issue_id": child, "creator_id": alice})
	initializeManagers(t, child)
	initializeManagers(t, grandchild)
	agent := dbfx.Agent(t, "Source transfer worker", testRuntimeID)
	property := dbfx.Insert(t, "issue_property", testutil.Cols{"workspace_id": testWorkspaceID, "name": "Old property", "type": "text", "config": "{}"})
	dbfx.Exec(t, `UPDATE issue SET status='todo',assignee_type='agent',assignee_id=$2,parent_issue_id=$3,metadata='{"old":"configuration"}',context_refs='["legacy"]',properties=$4::jsonb WHERE id=$1`, f.issue, agent, outside, fmt.Sprintf(`{"%s":"old value"}`, property))
	dbfx.Insert(t, "issue_dependency", testutil.Cols{"issue_id": child, "depends_on_issue_id": grandchild, "type": "related"})
	dbfx.Insert(t, "issue_dependency", testutil.Cols{"issue_id": child, "depends_on_issue_id": outside, "type": "related"})
	comment := dbfx.Comment(t, child, "Preserve child conversation")
	attachment := dbfx.Insert(t, "attachment", testutil.Cols{"workspace_id": testWorkspaceID, "issue_id": child, "uploader_type": "member", "uploader_id": testUserID, "filename": "retained.txt", "url": "/uploads/workspaces/fixture/retained.txt", "content_type": "text/plain", "size_bytes": 5})
	task := dbfx.Task(t, agent, testutil.Cols{"issue_id": uuidToString(f.issue), "runtime_id": testRuntimeID, "status": "running"})
	dbfx.Insert(t, "task_message", testutil.Cols{"task_id": task, "seq": 1, "type": "text", "content": "Preserved run transcript"})
	dbfx.Insert(t, "task_token", testutil.Cols{"task_id": task, "agent_id": agent, "workspace_id": testWorkspaceID, "user_id": testUserID, "token_hash": uuid.NewString(), "expires_at": testutil.Raw("now()+interval '1 hour'")})
	wake := withURLParam(newRequest("POST", "/wakeups", map[string]any{"agent_id": agent, "kind": "at", "after_seconds": 600, "instruction": "Old workspace wakeup"}), "id", uuidToString(f.issue))
	testutil.Call(t, testHandler.CreateIssueWakeup, wake).Want(201)
	snapshot, err := testHandler.Queries.GetIssue(ctx, f.issue)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := f.run(issueTransferRequest{IncludeSubissues: true}, false)
	if err != nil || len(plan.Blockers) > 0 || plan.AffectedIssues != 3 {
		t.Fatalf("tree preview %+v %v", plan, err)
	}
	if _, err = f.run(issueTransferRequest{IncludeSubissues: true, Fingerprint: plan.Fingerprint, ConfirmUnassign: true}, true); err != nil {
		t.Fatal(err)
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM issue WHERE id=ANY($1::uuid[]) AND workspace_id=$2`, []string{uuidToString(f.issue), child, grandchild}, f.target); n != 3 {
		t.Fatal("subtree did not move atomically")
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM issue WHERE id=$1 AND parent_issue_id IS NULL AND assignee_id IS NULL AND metadata='{}'::jsonb AND properties='{}'::jsonb AND context_refs='[]'::jsonb`, f.issue); n != 1 {
		t.Fatal("root kept incompatible workspace configuration")
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM issue WHERE id=$1 AND parent_issue_id=$2`, child, f.issue); n != 1 {
		t.Fatal("internal parent relation detached")
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM issue_dependency WHERE issue_id=$1 AND depends_on_issue_id=$2`, child, grandchild); n != 1 {
		t.Fatal("internal dependency detached")
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM issue_dependency WHERE issue_id=$1 AND depends_on_issue_id=$2`, child, outside); n != 0 {
		t.Fatal("external dependency retained")
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM comment WHERE id=$1 AND workspace_id=$2 AND content='Preserve child conversation'`, comment, f.target); n != 1 {
		t.Fatal("child conversation changed")
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM attachment WHERE id=$1 AND workspace_id=$2`, attachment, f.target); n != 1 {
		t.Fatal("attachment access scope did not move")
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE id=$1 AND status='cancelled'`, task); n != 1 {
		t.Fatal("running source execution survived")
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM task_token WHERE task_id=$1`, task); n != 0 {
		t.Fatal("old run retained writeback credentials")
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM issue_wakeup WHERE issue_id=$1`, f.issue); n != 0 {
		t.Fatal("source wakeup still active")
	}
	requireManagerIDs(t, uuidToString(f.issue), testUserID)
	requireManagerIDs(t, child, alice)
	requireManagerIDs(t, grandchild, alice)
	// Simulate an already-admitted old DELETE continuing after the move. It must
	// not cancel a newly started run in the destination workspace.
	runtime := dbfx.Runtime(t, "Destination runtime", testutil.Cols{"workspace_id": uuidToString(f.target), "owner_id": targetOwner})
	nextAgent := dbfx.Agent(t, "Destination worker", runtime, testutil.Cols{"workspace_id": uuidToString(f.target), "owner_id": targetOwner})
	next := dbfx.Task(t, nextAgent, testutil.Cols{"issue_id": uuidToString(f.issue), "runtime_id": runtime, "status": "running"})
	recorder := httptest.NewRecorder()
	if testHandler.cancelIssueForHumanDeletion(recorder, newRequest("DELETE", "/old-source", nil), snapshot) {
		t.Fatal("stale source deletion was admitted")
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE id=$1 AND status='running'`, next); n != 1 {
		t.Fatal("stale delete cancelled destination work")
	}
}
func TestIssueTransferSingleDetachesChildrenAndRequiresHumanConfirmation(t *testing.T) {
	f := newTransferFixture(t)
	child := dbfx.Issue(t, "Leave child in source", testutil.Cols{"parent_issue_id": uuidToString(f.issue)})
	plan, err := f.run(issueTransferRequest{}, false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = testHandler.runIssueTransfer(context.Background(), f.issue, f.source, f.target, f.actor, issueTransferRequest{Fingerprint: plan.Fingerprint}, true)
	expectTransferCode(t, err, "transfer_cleanup_required")
	if _, err = f.run(issueTransferRequest{Fingerprint: plan.Fingerprint}, true); err != nil {
		t.Fatal(err)
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM issue WHERE id=$1 AND workspace_id=$2 AND parent_issue_id IS NULL`, child, f.source); n != 1 {
		t.Fatal("single-task move moved or retained the external child edge")
	}
}
func TestIssueTransferTreeChecksEveryCreator(t *testing.T) {
	f := newTransferFixture(t)
	alice := managerTestUser(t, "Root-only administrator")
	dbfx.Member(t, uuidToString(f.target), alice, "member")
	dbfx.Exec(t, `UPDATE issue SET creator_id=$2 WHERE id=$1`, f.issue, alice)
	child := dbfx.Issue(t, "Child owned by someone else", testutil.Cols{"status": "cancelled", "parent_issue_id": uuidToString(f.issue)})
	initializeManagers(t, child)
	_, err := testHandler.runIssueTransfer(context.Background(), f.issue, f.source, f.target, parseUUID(alice), issueTransferRequest{IncludeSubissues: true}, false)
	expectTransferCode(t, err, "transfer_forbidden")
	if _, err = testHandler.runIssueTransfer(context.Background(), f.issue, f.source, f.target, parseUUID(alice), issueTransferRequest{}, false); err != nil {
		t.Fatal("creator cannot move their own task:", err)
	}
}

func TestIssueTransferRetainsCreatorWhoIsNotDestinationMember(t *testing.T) {
	f := newTransferFixture(t)
	creator := managerTestUser(t, "Source-only creator")
	dbfx.Exec(t, `UPDATE issue SET creator_id=$2 WHERE id=$1`, f.issue, creator)
	plan, err := f.run(issueTransferRequest{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.run(issueTransferRequest{Fingerprint: plan.Fingerprint}, true); err != nil {
		t.Fatal(err)
	}
	var got string
	dbfx.QueryRow(t, `SELECT creator_id::text FROM issue WHERE id=$1`, f.issue).Scan(&got)
	if got != creator {
		t.Fatal("transfer changed creator")
	}
	allowed, err := testHandler.Queries.CanManageIssueByCreator(context.Background(), f.issue, f.target, parseUUID(creator))
	if err != nil || allowed {
		t.Fatalf("nonmember creator should not have deletion permission: %v %v", allowed, err)
	}
}

func TestIssueTransferUsesCurrentAgentOwner(t *testing.T) {
	f := newTransferFixture(t)
	owner := managerTestUser(t, "Agent creator owner")
	other := managerTestUser(t, "Other user")
	dbfx.Member(t, uuidToString(f.target), owner, "member")
	agent := dbfx.Agent(t, "Archived creator", testRuntimeID, testutil.Cols{"owner_id": owner})
	dbfx.Exec(t, `UPDATE agent SET archived_at=now() WHERE id=$1`, agent)
	dbfx.Exec(t, `UPDATE issue SET creator_type='agent',creator_id=$2 WHERE id=$1`, f.issue, agent)
	plan, err := testHandler.runIssueTransfer(context.Background(), f.issue, f.source, f.target, parseUUID(owner), issueTransferRequest{}, false)
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, `UPDATE agent SET owner_id=$2 WHERE id=$1`, agent, other)
	_, err = testHandler.runIssueTransfer(context.Background(), f.issue, f.source, f.target, parseUUID(owner), issueTransferRequest{Fingerprint: plan.Fingerprint, ConfirmCleanup: true}, true)
	expectTransferCode(t, err, "transfer_forbidden")
}
