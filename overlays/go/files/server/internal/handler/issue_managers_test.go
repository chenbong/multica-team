package handler

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
)

func managerTestUser(t *testing.T, label string) string {
	t.Helper()
	if testHandler == nil {
		t.Fatal("isolated database required")
	}
	user := dbfx.User(t, label, "manager-"+uuid.NewString()+"@example.test")
	dbfx.Member(t, testWorkspaceID, user, "member")
	return user
}
func initializeManagers(t *testing.T, issue string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `SELECT team_mesh_ensure_issue_managers($1)`, issue); err != nil {
		t.Fatal(err)
	}
}
func managerIDs(t *testing.T, issue string) []string {
	t.Helper()
	rows, err := testPool.Query(context.Background(), `SELECT user_id::text FROM issue_manager WHERE issue_id=$1 ORDER BY user_id`, issue)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	return out
}
func requireManagerIDs(t *testing.T, issue string, want ...string) {
	t.Helper()
	sort.Strings(want)
	got := managerIDs(t, issue)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("administrator identities got %v want %v", got, want)
	}
}
func managerRequest(t *testing.T, issue, user string, body any, want int) {
	t.Helper()
	req := withURLParam(newRequest(http.MethodPut, "/api/issues/"+issue+"/administrators", body), "id", issue)
	req.Header.Set("X-User-ID", user)
	testutil.Call(t, testHandler.IssueManagers, req).Want(want)
}
func TestIssueManagersPermissionsAndDefaults(t *testing.T) {
	alice := managerTestUser(t, "Alice")
	bob := managerTestUser(t, "Bob")
	charlie := managerTestUser(t, "Charlie")
	issue := dbfx.Issue(t, "Human administrator", testutil.Cols{"creator_id": alice})
	initializeManagers(t, issue)
	requireManagerIDs(t, issue, alice)
	req := withURLParam(newRequest(http.MethodGet, "/administrators", nil), "id", issue)
	var view struct {
		CanManage bool `json:"can_manage"`
		CanDelete bool `json:"can_delete"`
	}
	testutil.Call(t, testHandler.IssueManagers, req).Want(200).JSON(&view)
	if !view.CanManage || view.CanDelete {
		t.Fatal("workspace owner implicitly became a task administrator")
	}
	remove := withURLParam(newRequest(http.MethodDelete, "/api/issues/"+issue, nil), "id", issue)
	testutil.Call(t, testHandler.DeleteIssue, remove).Want(403)
	managerRequest(t, issue, charlie, map[string]any{"user_ids": []string{charlie}}, 403)
	managerRequest(t, issue, alice, map[string]any{"user_ids": []string{}}, 400)
	outsider := dbfx.User(t, "Outsider", "outsider-"+uuid.NewString()+"@example.test")
	managerRequest(t, issue, alice, map[string]any{"user_ids": []string{outsider}}, 400)
	managerRequest(t, issue, alice, map[string]any{"user_ids": []string{alice, bob}}, 200)
	requireManagerIDs(t, issue, alice, bob)
	machine := withURLParam(newRequest(http.MethodPut, "/administrators", map[string]any{"user_ids": []string{alice}}), "id", issue)
	machine.Header.Set("X-User-ID", alice)
	machine.Header.Set("X-Actor-Source", "task_token")
	testutil.Call(t, testHandler.IssueManagers, machine).Want(403)
	agentDelete := remove.Clone(remove.Context())
	agentDelete.Header = remove.Header.Clone()
	agentDelete.Header.Set("X-User-ID", alice)
	agentDelete.Header.Set("X-Actor-Source", "task_token")
	testutil.Call(t, testHandler.DeleteIssue, agentDelete).Want(403)
	// A workspace owner may explicitly add themselves, then delete as a listed administrator.
	managerRequest(t, issue, testUserID, map[string]any{"user_ids": []string{testUserID, alice}}, 200)
	testutil.Call(t, testHandler.DeleteIssue, remove).Want(204)
	if len(managerIDs(t, issue)) != 0 {
		t.Fatal("deleted task left administrator metadata")
	}
}
func TestIssueManagersAgentCreationAndIndependentInheritance(t *testing.T) {
	alice := managerTestUser(t, "Agent owner")
	bob := managerTestUser(t, "Actual requester")
	agent := dbfx.Agent(t, "Manager provenance agent", testRuntimeID, testutil.Cols{"owner_id": alice})
	parent := dbfx.Issue(t, "Parent managed by Bob", testutil.Cols{"creator_id": bob})
	initializeManagers(t, parent)
	child := dbfx.Issue(t, "Agent child", testutil.Cols{"creator_type": "agent", "creator_id": agent, "parent_issue_id": parent})
	initializeManagers(t, child)
	requireManagerIDs(t, child, bob)
	managerRequest(t, parent, bob, map[string]any{"user_ids": []string{alice}}, 200)
	requireManagerIDs(t, child, bob)
	task := dbfx.Task(t, agent, testutil.Cols{"issue_id": parent, "runtime_id": testRuntimeID, "status": "completed", "originator_user_id": bob, "accountable_user_id": bob, "originator_source": "direct_human"})
	independent := dbfx.Issue(t, "Requested independent", testutil.Cols{"creator_type": "agent", "creator_id": agent, "origin_type": "agent_create", "origin_id": task})
	initializeManagers(t, independent)
	requireManagerIDs(t, independent, bob)
	automatic := dbfx.Task(t, agent, testutil.Cols{"issue_id": parent, "runtime_id": testRuntimeID, "status": "completed", "originator_user_id": nil, "originator_source": "unattributed"})
	derived := dbfx.Issue(t, "Autonomous independent", testutil.Cols{"creator_type": "agent", "creator_id": agent, "origin_type": "agent_create", "origin_id": automatic})
	initializeManagers(t, derived)
	requireManagerIDs(t, derived, alice)
	noSource := dbfx.Issue(t, "Autonomous without source", testutil.Cols{"creator_type": "agent", "creator_id": agent})
	initializeManagers(t, noSource)
	requireManagerIDs(t, noSource, alice)
}
func TestIssueManagersDepartureAddsWorkspaceOwner(t *testing.T) {
	alice := managerTestUser(t, "Departing administrator")
	issue := dbfx.Issue(t, "Managed before departure", testutil.Cols{"creator_id": alice})
	initializeManagers(t, issue)
	var member string
	if err := testPool.QueryRow(context.Background(), `SELECT id FROM member WHERE workspace_id=$1 AND user_id=$2`, testWorkspaceID, alice).Scan(&member); err != nil {
		t.Fatal(err)
	}
	if _, err := testHandler.revokeAndRemoveMember(context.Background(), parseUUID(testWorkspaceID), parseUUID(alice), parseUUID(member), parseUUID(testUserID)); err != nil {
		t.Fatal(err)
	}
	requireManagerIDs(t, issue, testUserID)
	// A returning member does not regain authority merely by rejoining.
	dbfx.Member(t, testWorkspaceID, alice, "member")
	requireManagerIDs(t, issue, testUserID)
}
func TestIssueManagersConcurrentReplacementPreservesAuthority(t *testing.T) {
	alice := managerTestUser(t, "First administrator")
	bob := managerTestUser(t, "Second administrator")
	issue := dbfx.Issue(t, "Concurrent administrators", testutil.Cols{"creator_id": alice})
	initializeManagers(t, issue)
	managerRequest(t, issue, alice, map[string]any{"user_ids": []string{alice, bob}}, 200)
	start := make(chan struct{})
	results := make(chan int, 2)
	for _, user := range []string{alice, bob} {
		go func(user string) {
			<-start
			req := withURLParam(newRequest(http.MethodPut, "/administrators", map[string]any{"user_ids": []string{user}}), "id", issue)
			req.Header.Set("X-User-ID", user)
			rec := httptest.NewRecorder()
			testHandler.IssueManagers(rec, req)
			results <- rec.Code
		}(user)
	}
	close(start)
	codes := []int{<-results, <-results}
	sort.Ints(codes)
	if fmt.Sprint(codes) != "[200 403]" {
		t.Fatalf("concurrent permission checks were not serialized: %v", codes)
	}
	if len(managerIDs(t, issue)) != 1 {
		t.Fatal("concurrent replacement left an invalid manager list")
	}
}
