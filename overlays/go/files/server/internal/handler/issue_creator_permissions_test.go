package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func permissionsRequest(issue, user, source string) *http.Request {
	req := withURLParam(newRequest("GET", "/permissions", nil), "id", issue)
	req.Header.Set("X-User-ID", user)
	if source != "" {
		req.Header.Set("X-Actor-Source", source)
	}
	return req
}
func deleteRequest(issue, user, source string) *http.Request {
	req := withURLParam(newRequest("DELETE", "/issues/"+issue, nil), "id", issue)
	req.Header.Set("X-User-ID", user)
	if source != "" {
		req.Header.Set("X-Actor-Source", source)
	}
	return req
}
func wantDeletionPermission(t *testing.T, issue, user, source string, want bool) {
	t.Helper()
	var result struct {
		CanDelete bool `json:"can_delete"`
	}
	testutil.Call(t, testHandler.IssuePermissions, permissionsRequest(issue, user, source)).Want(200).JSON(&result)
	if result.CanDelete != want {
		t.Fatalf("user %s source %s got permission %v want %v", user, source, result.CanDelete, want)
	}
}
func TestIssueCreatorDeletionPermissions(t *testing.T) {
	creator := managerTestUser(t, "Human creator")
	other := managerTestUser(t, "Former task administrator")
	admin := managerTestUser(t, "Workspace administrator")
	dbfx.Exec(t, `UPDATE member SET role='admin' WHERE workspace_id=$1 AND user_id=$2`, testWorkspaceID, admin)
	human := dbfx.Issue(t, "Creator issue", testutil.Cols{"creator_id": creator})
	dbfx.Exec(t, `INSERT INTO issue_manager(issue_id,user_id) VALUES($1,$2)`, human, other)
	wantDeletionPermission(t, human, creator, "", true)
	wantDeletionPermission(t, human, other, "", false)
	wantDeletionPermission(t, human, admin, "", true)
	wantDeletionPermission(t, human, testUserID, "", true)
	wantDeletionPermission(t, human, creator, "task_token", false)
	wantDeletionPermission(t, human, creator, "cloud_pat", false)
	testutil.Call(t, testHandler.DeleteIssue, deleteRequest(human, other, "")).Want(403)
	testutil.Call(t, testHandler.DeleteIssue, deleteRequest(human, creator, "task_token")).Want(403)
	testutil.Call(t, testHandler.DeleteIssue, deleteRequest(human, creator, "")).Want(204)

	for _, user := range []string{admin, testUserID} {
		issue := dbfx.Issue(t, "Admin issue", testutil.Cols{"creator_id": creator})
		testutil.Call(t, testHandler.DeleteIssue, deleteRequest(issue, user, "")).Want(204)
	}
}
func TestIssueAgentCreatorOwnershipAndMembership(t *testing.T) {
	owner := managerTestUser(t, "Original agent owner")
	successor := managerTestUser(t, "Current agent owner")
	agent := dbfx.Agent(t, "Creator agent", testRuntimeID, testutil.Cols{"owner_id": owner})
	issue := dbfx.Issue(t, "Agent issue", testutil.Cols{"creator_type": "agent", "creator_id": agent})
	dbfx.Exec(t, `UPDATE agent SET archived_at=now() WHERE id=$1`, agent)
	wantDeletionPermission(t, issue, owner, "", true)
	dbfx.Exec(t, `UPDATE agent SET owner_id=$2 WHERE id=$1`, agent, successor)
	wantDeletionPermission(t, issue, owner, "", false)
	wantDeletionPermission(t, issue, successor, "", true)
	// The authorization lock must reject a previously authorized stale owner.
	row, err := testHandler.Queries.GetIssue(context.Background(), parseUUID(issue))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := testPool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = validateIssueDeleteActor(withIssueDeleteActor(context.Background(), owner), tx, row); err == nil {
		t.Fatal("stale owner accepted")
	}
	tx.Rollback(context.Background())
	dbfx.Exec(t, `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, testWorkspaceID, successor)
	wantDeletionPermission(t, issue, successor, "", false)
	dbfx.Member(t, testWorkspaceID, successor, "member")
	wantDeletionPermission(t, issue, successor, "", true)
	dbfx.Exec(t, `DELETE FROM agent WHERE id=$1`, agent)
	wantDeletionPermission(t, issue, successor, "", false)
	wantDeletionPermission(t, issue, testUserID, "", true)
	testutil.Call(t, testHandler.DeleteIssue, deleteRequest(issue, successor, "")).Want(403)
	testutil.Call(t, testHandler.DeleteIssue, deleteRequest(issue, testUserID, "")).Want(204)
}
func TestIssueBatchDeletionAllOrNothingAndParentDetach(t *testing.T) {
	alice := managerTestUser(t, "Batch creator")
	bob := managerTestUser(t, "Other creator")
	own := dbfx.Issue(t, "Own parent", testutil.Cols{"creator_id": alice})
	child := dbfx.Issue(t, "Other person's child", testutil.Cols{"creator_id": bob, "parent_issue_id": own})
	batch := newRequest("POST", "/batch-delete", map[string]any{"issue_ids": []string{own, child}})
	batch.Header.Set("X-User-ID", alice)
	testutil.Call(t, testHandler.BatchDeleteIssues, batch).Want(403)
	if n := dbfx.Count(t, `SELECT count(*) FROM issue WHERE id=ANY($1::uuid[])`, []string{own, child}); n != 2 {
		t.Fatal("unauthorized batch deleted issues")
	}
	bad := newRequest("POST", "/batch-delete", map[string]any{"issue_ids": []string{own, uuid.NewString()}})
	bad.Header.Set("X-User-ID", alice)
	testutil.Call(t, testHandler.BatchDeleteIssues, bad).Want(404)
	if n := dbfx.Count(t, `SELECT count(*) FROM issue WHERE id=$1`, own); n != 1 {
		t.Fatal("partly deleted missing-id batch")
	}
	testutil.Call(t, testHandler.DeleteIssue, deleteRequest(own, alice, "")).Want(204)
	if n := dbfx.Count(t, `SELECT count(*) FROM issue WHERE id=$1 AND parent_issue_id IS NULL`, child); n != 1 {
		t.Fatal("child was deleted or not detached")
	}
}

func TestIssueDeletionTransactionRejectsChangedPermissionBeforeCancellation(t *testing.T) {
	alice := managerTestUser(t, "Transaction actor")
	bob := managerTestUser(t, "Another creator")
	own := dbfx.Issue(t, "Authorized task", testutil.Cols{"creator_id": alice})
	other := dbfx.Issue(t, "Unauthorized task", testutil.Cols{"creator_id": bob})
	agent := dbfx.Agent(t, "Executing agent", testRuntimeID)
	run := dbfx.Task(t, agent, testutil.Cols{"issue_id": own, "runtime_id": testRuntimeID, "status": "running"})
	a, err := testHandler.Queries.GetIssue(context.Background(), parseUUID(own))
	if err != nil {
		t.Fatal(err)
	}
	b, err := testHandler.Queries.GetIssue(context.Background(), parseUUID(other))
	if err != nil {
		t.Fatal(err)
	}
	ctx := withIssueDeleteActor(context.Background(), alice)
	if _, err := testHandler.deleteIssuesAndCollectAttachmentURLs(ctx, []db.Issue{a, b}, nil); err == nil {
		t.Fatal("transaction accepted a batch containing an unauthorized issue")
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM issue WHERE id=ANY($1::uuid[])`, []string{own, other}); n != 2 {
		t.Fatal("partial deletion")
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE id=$1 AND status='running'`, run); n != 1 {
		t.Fatal("unauthorized batch cancelled an active run")
	}
}
func TestIssueLegacyAdministratorsRetired(t *testing.T) {
	alice := managerTestUser(t, "Former admin")
	issue := dbfx.Issue(t, "Legacy administrator issue")
	dbfx.Exec(t, `INSERT INTO issue_manager(issue_id,user_id) VALUES($1,$2)`, issue, alice)
	req := withURLParam(newRequest("PUT", "/administrators", map[string]any{"user_ids": []string{alice}}), "id", issue)
	testutil.Call(t, testHandler.IssueManagers, req).Want(410)
	req = withURLParam(newRequest("GET", "/administrators", nil), "id", issue)
	var result struct {
		Administrators []any `json:"administrators"`
		CanManage      bool  `json:"can_manage"`
		CanDelete      bool  `json:"can_delete"`
	}
	testutil.Call(t, testHandler.IssueManagers, req).Want(200).JSON(&result)
	if len(result.Administrators) != 0 || result.CanManage || !result.CanDelete {
		t.Fatalf("unexpected compatibility response: %+v", result)
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM issue_manager WHERE issue_id=$1 AND user_id=$2`, issue, alice); n != 1 {
		t.Fatal("legacy records changed")
	}
}
