package handler

import (
	"context"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/testutil"
	"net/http"
	"strings"
	"testing"
)

func managedReadRequest(issue, section, user, agent, task string) *http.Request {
	req := newRequest(http.MethodGet, "/api/agent/managed-issues/"+issue+"/"+section, nil)
	req.Header.Set("X-User-ID", user)
	req.Header.Set("Authorization", "Bearer mat_managed_"+task)
	req.Header.Set("X-Agent-ID", agent)
	req.Header.Set("X-Task-ID", task)
	req.Header.Set("X-Actor-Source", "task_token")
	route := chi.NewRouteContext()
	route.URLParams.Add("managedIssueId", issue)
	route.URLParams.Add("*", section)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, route))
}
func TestOwnerManagedIssueReadsStayReadOnlyAndOwnerScoped(t *testing.T) {
	alice := managerTestUser(t, "Shared agent owner")
	bob := managerTestUser(t, "Other task owner")
	runtime := dbfx.Runtime(t, "Shared execution machine", testutil.Cols{"owner_id": testUserID, "visibility": "public"})
	agent := dbfx.Agent(t, "Shared reading agent", runtime, testutil.Cols{"owner_id": alice})
	task := dbfx.Task(t, agent, testutil.Cols{"status": "running", "runtime_id": runtime, "originator_user_id": bob, "accountable_user_id": bob, "originator_source": "direct_human"})
	tokenHash := auth.HashToken("mat_managed_" + task)
	dbfx.Insert(t, "task_token", testutil.Cols{"token_hash": tokenHash, "task_id": task, "agent_id": agent, "workspace_id": testWorkspaceID, "user_id": testUserID, "expires_at": testutil.Raw("now()+interval '1 hour'")})
	dbfx.InsertNoID(t, "agent_token_read_scope", testutil.Cols{"token_hash": tokenHash, "task_id": task, "agent_id": agent, "owner_id": alice, "workspace_id": testWorkspaceID}, "token_hash=$1", tokenHash)
	machineTask := dbfx.Issue(t, "Machine owner's task")
	initializeManagers(t, machineTask)
	target := dbfx.Workspace(t, "Other workspace", "managed-"+uuid.NewString(), testutil.Cols{"issue_prefix": "READ"})
	dbfx.Member(t, target, alice, "owner")
	dbfx.Member(t, target, bob, "member")
	own := dbfx.Issue(t, "Owner-managed confidential task", testutil.Cols{"workspace_id": target, "creator_id": alice, "description": "retained task context"})
	initializeManagers(t, own)
	other := dbfx.Issue(t, "Unmanaged confidential task", testutil.Cols{"workspace_id": target, "creator_id": bob})
	initializeManagers(t, other)
	dbfx.Comment(t, own, "Managed task discussion", testutil.Cols{"workspace_id": target})
	run := dbfx.Task(t, agent, testutil.Cols{"issue_id": own, "status": "completed", "runtime_id": runtime})
	dbfx.Insert(t, "task_message", testutil.Cols{"task_id": run, "seq": 1, "type": "text", "content": "managed execution record"})
	for _, section := range []string{"", "comments", "timeline", "runs", "panels", "properties", "administrators", "runs/" + run + "/messages"} {
		got := testutil.Call(t, testHandler.OwnerManagedIssues, managedReadRequest(own, section, testUserID, agent, task)).Want(200)
		if section == "" && !strings.Contains(got.Body.String(), "retained task context") {
			t.Fatal("task detail omitted owner-managed context")
		}
	}
	testutil.Call(t, testHandler.OwnerManagedIssues, managedReadRequest(other, "", testUserID, agent, task)).Want(200)
	listing := testutil.Call(t, testHandler.OwnerManagedIssues, managedReadRequest("", "", testUserID, agent, task)).Want(200)
	if !strings.Contains(listing.Body.String(), own) || !strings.Contains(listing.Body.String(), other) || !strings.Contains(listing.Body.String(), machineTask) {
		t.Fatal("list omitted tasks in workspaces visible to the agent owner")
	}
	wrongRun := dbfx.Task(t, agent, testutil.Cols{"issue_id": other, "status": "completed", "runtime_id": runtime})
	testutil.Call(t, testHandler.OwnerManagedIssues, managedReadRequest(own, "runs/"+wrongRun+"/messages", testUserID, agent, task)).Want(404)
	family := managedReadRequest(own, "runs", testUserID, agent, task)
	family.URL.RawQuery = "scope=family"
	testutil.Call(t, testHandler.OwnerManagedIssues, family).Want(400)
	write := managedReadRequest(own, "", testUserID, agent, task)
	write.Method = http.MethodPut
	testutil.Call(t, testHandler.OwnerManagedIssues, write).Want(403)
	human := managedReadRequest(own, "", testUserID, agent, task)
	human.Header.Del("X-Actor-Source")
	testutil.Call(t, testHandler.OwnerManagedIssues, human).Want(403)
	// A transfer to the runtime's owner cannot upgrade an old token into the
	// new agent owner's read authority, even though its native user ID matches.
	dbfx.Exec(t, `UPDATE agent SET owner_id=$2 WHERE id=$1`, agent, testUserID)
	testutil.Call(t, testHandler.OwnerManagedIssues, managedReadRequest(own, "", testUserID, agent, task)).Want(403)
	dbfx.Exec(t, `UPDATE agent SET owner_id=$2 WHERE id=$1`, agent, alice)
	// Removal is effective immediately for the same still-running shared agent.
	req := withURLParam(newRequest(http.MethodPut, "/administrators", map[string]any{"user_ids": []string{bob}}), "id", own)
	req.Header.Set("X-User-ID", alice)
	req.Header.Set("X-Workspace-ID", target)
	testutil.Call(t, testHandler.IssueManagers, req).Want(200)
	testutil.Call(t, testHandler.OwnerManagedIssues, managedReadRequest(own, "", testUserID, agent, task)).Want(200)
	dbfx.Exec(t, `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, target, alice)
	testutil.Call(t, testHandler.OwnerManagedIssues, managedReadRequest(own, "", testUserID, agent, task)).Want(404)
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed' WHERE id=$1`, task)
	testutil.Call(t, testHandler.OwnerManagedIssues, managedReadRequest(own, "", testUserID, agent, task)).Want(403)
}

func TestOwnerManagedScopeIsMintedForAgentOwnerOnSharedMachine(t *testing.T) {
	if testHandler == nil {
		t.Fatal("isolated database required")
	}
	ctx := context.Background()
	owner := managerTestUser(t, "Agent owner on shared runtime")
	runtime := createClaimReclaimRuntime(t, ctx, "Owner grant shared runtime")
	agent, issue := createClaimReclaimAgentAndIssue(t, ctx, runtime, "Owner grant shared agent")
	dbfx.Exec(t, `UPDATE agent_runtime SET visibility='public' WHERE id=$1`, runtime)
	dbfx.Exec(t, `UPDATE agent SET owner_id=$2,permission_mode='public_to',visibility='workspace' WHERE id=$1`, agent, owner)
	dbfx.Exec(t, `INSERT INTO agent_invocation_target(agent_id,target_type,target_id) VALUES($1,'workspace',$2) ON CONFLICT DO NOTHING`, agent, testWorkspaceID)
	task := createDispatchedClaimFixtureTask(t, ctx, agent, runtime, issue, "120 seconds", false)
	claimed, body := claimTaskByRuntimeForTest(t, runtime)
	if claimed == nil || claimed.ID != task {
		t.Fatalf("fixture was not claimed: %s", body)
	}
	var agentOwner, machineOwner string
	dbfx.QueryRow(t, `SELECT cap.owner_id,tok.user_id FROM agent_token_read_scope cap JOIN task_token tok ON tok.token_hash=cap.token_hash WHERE cap.task_id=$1`, task).Scan(&agentOwner, &machineOwner)
	if agentOwner != owner || machineOwner != testUserID {
		t.Fatal("claim conflated agent ownership and machine credential identity")
	}
	dbfx.Cleanup(t, `DELETE FROM agent_token_read_scope WHERE task_id=$1`, task)
}
