package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func personalSecurityMember(t *testing.T, role string) string {
	t.Helper()
	if testHandler == nil || testPool == nil {
		t.Fatal("personal runtime security tests require an isolated database")
	}
	id := dbfx.User(t, "Runtime security "+role, uuid.NewString()+"@runtime-security.test")
	dbfx.Member(t, testWorkspaceID, id, role)
	// Canonical definitions intentionally outlive workspace projections. Remove
	// only these fixture users' unreferenced definitions before user cleanup.
	t.Cleanup(func() {
		for _, owner := range []string{id, testUserID} {
			if _, err := testPool.Exec(context.Background(), `DELETE FROM personal_runtime p WHERE owner_id=$1 AND NOT EXISTS(SELECT 1 FROM agent_runtime r WHERE r.personal_runtime_id=p.id)`, owner); err != nil {
				t.Errorf("clean personal runtime fixtures: %v", err)
			}
			if _, err := testPool.Exec(context.Background(), `DELETE FROM personal_runtime_profile p WHERE owner_id=$1 AND NOT EXISTS(SELECT 1 FROM runtime_profile r WHERE r.personal_profile_id=p.id) AND NOT EXISTS(SELECT 1 FROM personal_runtime r WHERE r.personal_profile_id=p.id)`, owner); err != nil {
				t.Errorf("clean personal profile fixtures: %v", err)
			}
		}
	})
	return id
}

func personalSecurityRuntime(t *testing.T, owner, machine string) string {
	t.Helper()
	return dbfx.Runtime(t, "Personal security runtime", testutil.Cols{
		"owner_id": owner, "daemon_id": machine, "runtime_mode": "local", "provider": "codex",
	})
}

func personalSecurityRegisterBody(machine string) map[string]any {
	return map[string]any{
		"workspace_id": testWorkspaceID, "daemon_id": machine, "device_name": "Security fixture",
		"runtimes": []map[string]any{{"type": "codex", "name": "Reconnected runtime"}},
	}
}

func TestPersonalRuntimeSecurityAgentManagement(t *testing.T) {
	owner := personalSecurityMember(t, "member")
	runtimeID := personalSecurityRuntime(t, owner, uuid.NewString())
	agentID := dbfx.Agent(t, "Shared owned agent", runtimeID, testutil.Cols{
		"owner_id": owner, "visibility": "workspace", "permission_mode": "public_to",
		"mcp_config": testutil.Raw(`'{"servers":[{"token":"private-mcp-value"}]}'::jsonb`),
	})
	dbfx.Insert(t, "agent_invocation_target", testutil.Cols{"agent_id": agentID, "target_type": "workspace", "target_id": testWorkspaceID})
	for _, userID := range []string{testUserID, personalSecurityMember(t, "admin"), personalSecurityMember(t, "member")} {
		t.Run(userID, func(t *testing.T) {
			var response AgentResponse
			testutil.Call(t, testHandler.GetAgent, withURLParam(newRequestAs(userID, "GET", "/", nil), "id", agentID)).Want(200).JSON(&response)
			if (len(response.McpConfig) != 0 && strings.TrimSpace(string(response.McpConfig)) != "null") || !response.McpConfigRedacted {
				t.Fatal("shared agent exposed owner MCP secrets")
			}
			for _, call := range []struct {
				handler http.HandlerFunc
				body    any
			}{
				{testHandler.UpdateAgent, map[string]any{"name": "stolen", "mcp_config": map[string]any{"servers": []string{"evil"}}}},
				{testHandler.ArchiveAgent, nil},
				{testHandler.RestoreAgent, nil},
				{testHandler.CancelAgentTasks, nil},
			} {
				testutil.Call(t, call.handler, withURLParam(newRequestAs(userID, "POST", "/", call.body), "id", agentID)).Want(403)
			}
		})
	}
	testutil.Call(t, testHandler.UpdateAgent, withURLParam(newRequestAs(owner, "PATCH", "/", map[string]any{"name": "Owner edit"}), "id", agentID)).Want(200)
	if canViewAgentSecrets(db.Agent{}, "", "owner") {
		t.Fatal("missing owner and user identities must not compare equal")
	}
}

func TestPersonalRuntimeSecurityAgentEnvOwnerOnly(t *testing.T) {
	owner := personalSecurityMember(t, "member")
	runtimeID := personalSecurityRuntime(t, owner, uuid.NewString())
	agentID := dbfx.Agent(t, "Owner secret agent", runtimeID, testutil.Cols{
		"owner_id": owner, "visibility": "workspace", "permission_mode": "public_to",
		"custom_env": testutil.Raw(`'{"TOKEN":"owner-secret"}'::jsonb`),
	})
	for _, userID := range []string{testUserID, personalSecurityMember(t, "admin")} {
		testutil.Call(t, testHandler.GetAgentEnv, withURLParam(newRequestAs(userID, "GET", "/", nil), "id", agentID)).Want(403)
		testutil.Call(t, testHandler.UpdateAgentEnv, withURLParam(newRequestAs(userID, "PUT", "/", map[string]any{"custom_env": map[string]string{"TOKEN": "changed"}}), "id", agentID)).Want(403)
	}
	testutil.Call(t, testHandler.GetAgentEnv, withURLParam(newRequestAs(owner, "GET", "/", nil), "id", agentID)).Want(200)
}

func TestPersonalRuntimeSecurityMachineCredentialsCannotManageAgents(t *testing.T) {
	personalSecurityMember(t, "member")
	runtimeID := personalSecurityRuntime(t, testUserID, uuid.NewString())
	agentID := dbfx.Agent(t, "Owner configuration", runtimeID, testutil.Cols{
		"visibility": "workspace", "permission_mode": "public_to", "mcp_config": testutil.Raw(`'{"token":"private-config"}'::jsonb`),
	})
	for _, source := range []string{"task_token", "cloud_pat"} {
		t.Run(source, func(t *testing.T) {
			request := newRequest("POST", "/", map[string]any{"name": "Delegated agent", "runtime_id": runtimeID})
			request.Header.Set("X-Actor-Source", source)
			testutil.Call(t, testHandler.CreateAgent, request).Want(403)
			request = withURLParam(newRequest("PATCH", "/", map[string]any{"name": "Changed by shared invocation"}), "id", agentID)
			request.Header.Set("X-Actor-Source", source)
			testutil.Call(t, testHandler.UpdateAgent, request).Want(403)
			request = withURLParam(newRequest("POST", "/", nil), "id", agentID)
			request.Header.Set("X-Actor-Source", source)
			testutil.Call(t, testHandler.ArchiveAgent, request).Want(403)
			request = withURLParam(newRequest("GET", "/", nil), "id", agentID)
			request.Header.Set("X-Actor-Source", source)
			var response AgentResponse
			testutil.Call(t, testHandler.GetAgent, request).Want(200).JSON(&response)
			if !response.McpConfigRedacted || strings.Contains(string(response.McpConfig), "private-config") {
				t.Fatal("machine credential exposed owner MCP configuration")
			}
		})
	}
}

func TestPersonalRuntimeSecurityRejectsForeignPublicRuntimeBinding(t *testing.T) {
	other := personalSecurityMember(t, "member")
	// A legacy public cloud row is not normalized by the personal projection
	// trigger, so this exercises the application guard independently of SQL.
	foreign := dbfx.Runtime(t, "Legacy public runtime", testutil.Cols{"owner_id": other, "visibility": "public"})
	testutil.Call(t, testHandler.CreateAgent, newRequest("POST", "/", map[string]any{"name": "Foreign binding", "runtime_id": foreign})).Want(403)
	agentID := dbfx.Agent(t, "Own agent", testRuntimeID)
	testutil.Call(t, testHandler.UpdateAgent, withURLParam(newRequest("PATCH", "/", map[string]any{"runtime_id": foreign}), "id", agentID)).Want(403)
	var bound string
	dbfx.QueryRow(t, `SELECT runtime_id FROM agent WHERE id=$1`, agentID).Scan(&bound)
	if bound != testRuntimeID {
		t.Fatal("denied rebind changed the stored runtime")
	}
}

func TestPersonalRuntimeSecurityRegistration(t *testing.T) {
	owner := personalSecurityMember(t, "member")
	machine := uuid.NewString()
	runtimeID := personalSecurityRuntime(t, owner, machine)
	body := personalSecurityRegisterBody(machine)
	testutil.Call(t, testHandler.DaemonRegister, newRequest("POST", "/", body)).Want(403)
	testutil.Call(t, testHandler.DaemonRegister, newDaemonTokenRequest("POST", "/", body, testWorkspaceID, uuid.NewString())).Want(403)
	var name, storedOwner string
	dbfx.QueryRow(t, `SELECT name,owner_id FROM agent_runtime WHERE id=$1`, runtimeID).Scan(&name, &storedOwner)
	if name != "Personal security runtime" || storedOwner != owner {
		t.Fatal("denied registration changed the existing runtime")
	}
	for _, request := range []*http.Request{
		newRequestAs(owner, "POST", "/", body),
		newDaemonTokenRequest("POST", "/", body, testWorkspaceID, machine),
	} {
		var result struct {
			Runtimes []AgentRuntimeResponse `json:"runtimes"`
		}
		testutil.Call(t, testHandler.DaemonRegister, request).Want(200).JSON(&result)
		if len(result.Runtimes) != 1 || result.Runtimes[0].ID != runtimeID || result.Runtimes[0].OwnerID == nil || *result.Runtimes[0].OwnerID != owner {
			t.Fatal("legitimate reconnect lost the original runtime ID or owner")
		}
	}
	// An exact MDT can also reconnect its owner's custom profile. It has no
	// human header, so the machine's locked owner must supply this authority.
	profile := insertRuntimeProfileFixture(t, context.Background(), "Security profile "+uuid.NewString(), "codex", "codex")
	ownMachine := uuid.NewString()
	custom := dbfx.Runtime(t, "Custom runtime", testutil.Cols{"daemon_id": ownMachine, "runtime_mode": "local", "profile_id": profile, "provider": "codex"})
	customBody := personalSecurityRegisterBody(ownMachine)
	customBody["runtimes"] = []map[string]any{{"type": "codex", "profile_id": profile}}
	var customResult struct {
		Runtimes []AgentRuntimeResponse `json:"runtimes"`
	}
	testutil.Call(t, testHandler.DaemonRegister, newDaemonTokenRequest("POST", "/", customBody, testWorkspaceID, ownMachine)).Want(200).JSON(&customResult)
	if len(customResult.Runtimes) != 1 || customResult.Runtimes[0].ID != custom {
		t.Fatal("custom MDT reconnect did not preserve runtime identity")
	}
	customBody["daemon_id"] = machine
	testutil.Call(t, testHandler.DaemonRegister, newDaemonTokenRequest("POST", "/", customBody, testWorkspaceID, machine)).Want(400)
	// A public/ownerless registration is not a claimable identity for a PAT.
	unownedMachine := uuid.NewString()
	dbfx.Runtime(t, "Legacy MDT runtime", testutil.Cols{"daemon_id": unownedMachine, "runtime_mode": "local", "provider": "codex", "owner_id": nil})
	testutil.Call(t, testHandler.DaemonRegister, newRequest("POST", "/", personalSecurityRegisterBody(unownedMachine))).Want(403)
	testutil.Call(t, testHandler.DaemonRegister, newDaemonTokenRequest("POST", "/", personalSecurityRegisterBody(unownedMachine), testWorkspaceID, unownedMachine)).Want(200)
}

func TestPersonalRuntimeSecurityConcurrentRegistration(t *testing.T) {
	other := personalSecurityMember(t, "member")
	machine := uuid.NewString()
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE workspace_id=$1 AND daemon_id=$2`, testWorkspaceID, machine)
	})
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, owner := range []string{testUserID, other} {
		go func(owner string) {
			<-start
			_, err := testHandler.upsertDaemonRuntime(context.Background(), db.UpsertAgentRuntimeParams{
				WorkspaceID: parseUUID(testWorkspaceID), DaemonID: strToText(machine), OwnerID: parseUUID(owner),
				Name: "Concurrent registration", RuntimeMode: "local", Provider: "codex", Status: "online", Metadata: []byte("{}"),
			})
			results <- err
		}(owner)
	}
	close(start)
	winners, denied := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			winners++
		} else if errors.Is(err, errDaemonRegistrationDenied) {
			denied++
		} else {
			t.Fatalf("unexpected registration result: %v", err)
		}
	}
	if winners != 1 || denied != 1 {
		t.Fatalf("concurrent registration: %d accepted, %d denied; want one each", winners, denied)
	}
}

func TestPersonalRuntimeSecurityLegacyMerge(t *testing.T) {
	other := personalSecurityMember(t, "member")
	oldMachine, newMachine := uuid.NewString(), uuid.NewString()
	oldRuntime := personalSecurityRuntime(t, other, oldMachine)
	newRuntime := personalSecurityRuntime(t, testUserID, newMachine)
	agentID := dbfx.Agent(t, "Foreign legacy agent", oldRuntime, testutil.Cols{"owner_id": other})
	taskID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": oldRuntime})
	body := personalSecurityRegisterBody(newMachine)
	body["legacy_daemon_ids"] = []string{oldMachine}
	testutil.Call(t, testHandler.DaemonRegister, newRequest("POST", "/", body)).Want(200)
	// Exercise the under-lock guard directly too; preflight alone is insufficient.
	err := testHandler.mergeLegacyRuntime(context.Background(), parseUUID(newRuntime), parseUUID(oldRuntime), oldMachine, "codex")
	if !errors.Is(err, errDaemonRegistrationDenied) {
		t.Fatalf("under-lock owner mismatch must fail, got %v", err)
	}
	var agentRuntime, taskRuntime string
	dbfx.QueryRow(t, `SELECT runtime_id FROM agent WHERE id=$1`, agentID).Scan(&agentRuntime)
	dbfx.QueryRow(t, `SELECT runtime_id FROM agent_task_queue WHERE id=$1`, taskID).Scan(&taskRuntime)
	if agentRuntime != oldRuntime || taskRuntime != oldRuntime {
		t.Fatal("foreign legacy registration moved an agent or task")
	}
	// Same-owner migration remains available to PAT daemons and keeps history.
	sameMachine := uuid.NewString()
	sameRuntime := personalSecurityRuntime(t, testUserID, sameMachine)
	sameAgent := dbfx.Agent(t, "Own legacy agent", sameRuntime)
	sameTask := dbfx.Task(t, sameAgent, testutil.Cols{"runtime_id": sameRuntime})
	body["legacy_daemon_ids"] = []string{sameMachine}
	testutil.Call(t, testHandler.DaemonRegister, newRequest("POST", "/", body)).Want(200)
	dbfx.QueryRow(t, `SELECT runtime_id FROM agent WHERE id=$1`, sameAgent).Scan(&agentRuntime)
	dbfx.QueryRow(t, `SELECT runtime_id FROM agent_task_queue WHERE id=$1`, sameTask).Scan(&taskRuntime)
	if agentRuntime != newRuntime || taskRuntime != newRuntime {
		t.Fatal("same-owner migration failed to retain agents and task history")
	}
}

func TestPersonalRuntimeSecurityDaemonIsolation(t *testing.T) {
	other := personalSecurityMember(t, "member")
	machine := uuid.NewString()
	runtimeID := personalSecurityRuntime(t, other, machine)
	agentID := dbfx.Agent(t, "Foreign task agent", runtimeID, testutil.Cols{"owner_id": other})
	issueID := dbfx.Issue(t, "Foreign issue")
	taskID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID})
	for _, request := range []*http.Request{
		newRequest("POST", "/", map[string]any{"runtime_id": runtimeID}),
		newDaemonTokenRequest("POST", "/", map[string]any{"runtime_id": runtimeID}, testWorkspaceID, uuid.NewString()),
	} {
		testutil.Call(t, testHandler.DaemonHeartbeat, request).Want(404)
	}
	testutil.Call(t, testHandler.ListPendingTasksByRuntime, withURLParam(newRequest("GET", "/", nil), "runtimeId", runtimeID)).Want(404)
	testutil.Call(t, testHandler.GetTaskStatus, withURLParam(newRequest("GET", "/", nil), "taskId", taskID)).Want(404)
	var batch batchClaimResponse
	testutil.Call(t, testHandler.ClaimTasksByRuntime, newRequest("POST", "/", map[string]any{"daemon_id": machine, "runtime_ids": []string{runtimeID}, "max_tasks": 1})).Want(200).JSON(&batch)
	if len(batch.Tasks) != 0 {
		t.Fatal("foreign task escaped in batch claim")
	}
	own := personalSecurityRuntime(t, testUserID, uuid.NewString())
	testutil.Call(t, testHandler.DaemonDeregister, newRequest("POST", "/", map[string]any{"runtime_ids": []string{own, runtimeID}})).Want(200)
	var status string
	dbfx.QueryRow(t, `SELECT status FROM agent_runtime WHERE id=$1`, runtimeID).Scan(&status)
	if status != "online" {
		t.Fatal("mixed deregistration changed a foreign runtime")
	}
	dbfx.QueryRow(t, `SELECT status FROM agent_runtime WHERE id=$1`, own).Scan(&status)
	if status != "offline" {
		t.Fatal("mixed deregistration did not process the authorized runtime")
	}
	dbfx.QueryRow(t, `SELECT status FROM agent_task_queue WHERE id=$1`, taskID).Scan(&status)
	if status != "queued" {
		t.Fatal("denied daemon operation modified the task")
	}
	// Orphaned/unbound tasks are not workspace-wide daemon capabilities.
	dbfx.Exec(t, `UPDATE agent_task_queue SET runtime_id=NULL,status='completed',completed_at=now() WHERE id=$1`, taskID)
	testutil.Call(t, testHandler.GetTaskStatus, withURLParam(newRequest("GET", "/", nil), "taskId", taskID)).Want(404)
}

func TestPersonalRuntimeSecurityWebSocketScopes(t *testing.T) {
	other := personalSecurityMember(t, "member")
	machine := uuid.NewString()
	own := personalSecurityRuntime(t, testUserID, machine)
	foreign := personalSecurityRuntime(t, other, uuid.NewString())
	legacyCloud := dbfx.Runtime(t, "Legacy cloud")
	unboundLocal := dbfx.Runtime(t, "Local without machine", testutil.Cols{"runtime_mode": "local"})
	for _, tc := range []struct {
		name    string
		ids     []string
		request *http.Request
		allowed bool
	}{
		{"owner PAT", []string{own}, newRequest("GET", "/", nil), true},
		{"admin PAT foreign", []string{foreign}, newRequest("GET", "/", nil), false},
		{"mixed PAT set", []string{own, foreign}, newRequest("GET", "/", nil), false},
		{"exact MDT", []string{own}, newDaemonTokenRequest("GET", "/", nil, testWorkspaceID, machine), true},
		{"foreign MDT", []string{own}, newDaemonTokenRequest("GET", "/", nil, testWorkspaceID, uuid.NewString()), false},
		{"cross workspace MDT", []string{own}, newDaemonTokenRequest("GET", "/", nil, uuid.NewString(), machine), false},
		{"legacy cloud MDT", []string{legacyCloud}, newDaemonTokenRequest("GET", "/", nil, testWorkspaceID, machine), true},
		{"unbound local MDT", []string{unboundLocal}, newDaemonTokenRequest("GET", "/", nil, testWorkspaceID, machine), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			identity, ok := testHandler.buildDaemonWebSocketIdentity(w, tc.request, tc.ids, requestUserID(tc.request))
			if ok != tc.allowed {
				t.Fatalf("authorization=%v want %v: %d %s", ok, tc.allowed, w.Code, w.Body.String())
			}
			if !ok && (w.Code != 404 || len(identity.RuntimeLeases) != 0) {
				t.Fatal("denied websocket exposed an identity or leases")
			}
		})
	}
	request := newDaemonTokenRequest("GET", "/", nil, testWorkspaceID, machine)
	request.Header.Set("X-User-ID", other)
	w := httptest.NewRecorder()
	identity, ok := testHandler.buildDaemonWebSocketIdentity(w, request, []string{own}, other)
	if !ok || identity.UserID != "" {
		t.Fatalf("MDT must retain machine scope despite supplied user header: %d %s", w.Code, w.Body.String())
	}
	if _, err := testHandler.HandleDaemonWSHeartbeat(context.Background(), identity, foreign, false); err == nil {
		t.Fatal("websocket heartbeat accepted a runtime outside its leases")
	}
	// RPC requests still re-authorize the supplied runtime set, even if a
	// caller bypasses the UI and sends another machine's runtime IDs.
	claim, _ := json.Marshal(map[string]any{"daemon_id": machine, "runtime_ids": []string{foreign}, "max_tasks": 1})
	status, body, err := testHandler.DaemonRPCHandler(context.Background(), identity, "tasks.claim", claim)
	if err != nil || status != 200 {
		t.Fatalf("RPC denial: status %d error %v", status, err)
	}
	var response batchClaimResponse
	if err := json.Unmarshal(body, &response); err != nil || len(response.Tasks) != 0 {
		t.Fatal("websocket RPC leaked foreign work")
	}
}

func TestPersonalRuntimeSecurityMDTCannotSpoofUser(t *testing.T) {
	other := personalSecurityMember(t, "member")
	foreign := personalSecurityRuntime(t, other, uuid.NewString())
	request := newDaemonTokenRequest("POST", "/", map[string]any{"runtime_id": foreign}, testWorkspaceID, uuid.NewString())
	request.Header.Set("X-User-ID", other)
	testutil.Call(t, testHandler.DaemonHeartbeat, request).Want(404)
}

func TestPersonalRuntimeSecuritySharedInvocation(t *testing.T) {
	colleague := personalSecurityMember(t, "member")
	machine := uuid.NewString()
	runtimeID := personalSecurityRuntime(t, testUserID, machine)
	agentID := dbfx.Agent(t, "Shared personal runtime agent", runtimeID, testutil.Cols{
		"visibility": "workspace", "permission_mode": "public_to", "runtime_mode": "local",
		"custom_env": testutil.Raw(`'{"OWNER_TOKEN":"fixture-owner-secret"}'::jsonb`),
	})
	dbfx.Insert(t, "agent_invocation_target", testutil.Cols{"agent_id": agentID, "target_type": "member", "target_id": colleague})
	member, err := testHandler.Queries.GetMemberByUserAndWorkspace(context.Background(), db.GetMemberByUserAndWorkspaceParams{UserID: parseUUID(colleague), WorkspaceID: parseUUID(testWorkspaceID)})
	if err != nil {
		t.Fatal(err)
	}
	request := newRequestAs(colleague, "POST", "/", map[string]any{"agent_id": agentID, "title": "Shared invocation"})
	request = request.WithContext(middleware.SetMemberContext(request.Context(), testWorkspaceID, member))
	var session ChatSessionResponse
	testutil.Call(t, testHandler.CreateChatSession, request).Want(201).JSON(&session)
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM chat_session WHERE id=$1`, session.ID) })
	request = withURLParam(newRequestAs(colleague, "POST", "/", map[string]any{"content": "Run the shared agent"}), "sessionId", session.ID)
	request = request.WithContext(middleware.SetMemberContext(request.Context(), testWorkspaceID, member))
	var sent SendChatMessageResponse
	testutil.Call(t, testHandler.SendChatMessage, request).Want(201).JSON(&sent)
	if sent.TaskID == "" {
		t.Fatal("shared invocation did not enqueue a task")
	}
	// The colleague may invoke, but cannot impersonate the execution daemon.
	testutil.Call(t, testHandler.ClaimTaskByRuntime, withURLParam(newRequestAs(colleague, "POST", "/", nil), "runtimeId", runtimeID)).Want(404)
	claim := newDaemonTokenRequest("POST", "/", map[string]any{"daemon_id": machine, "runtime_ids": []string{runtimeID}, "max_tasks": 1}, testWorkspaceID, machine)
	w := testutil.Call(t, testHandler.ClaimTasksByRuntime, claim).Want(200)
	var response batchClaimResponse
	w.JSON(&response)
	if len(response.Tasks) != 1 || response.Tasks[0].ID != sent.TaskID || response.Tasks[0].AuthToken == "" {
		t.Fatalf("owner daemon failed to receive shared invocation: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "fixture-owner-secret") {
		t.Fatal("authorized owner daemon lost required execution secrets")
	}
	// The remote MCP broker's same-machine capability can read this task even
	// though the user who initiated it is not the runtime owner.
	read := newDaemonTokenRequest("GET", "/", nil, testWorkspaceID, machine)
	read = withURLParam(read, "taskId", sent.TaskID)
	testutil.Call(t, testHandler.GetTaskStatus, read).Want(200)
}

func TestPersonalRuntimeSecurityPublicLegacyClaimOwnerMismatch(t *testing.T) {
	other := personalSecurityMember(t, "member")
	runtimeID := dbfx.Runtime(t, "Legacy public claim", testutil.Cols{"visibility": "public"})
	agentID := dbfx.Agent(t, "Mismatched legacy agent", runtimeID, testutil.Cols{"owner_id": other})
	issueID := dbfx.Issue(t, "Mismatched legacy issue")
	taskID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID})
	w := testutil.Call(t, testHandler.ClaimTaskByRuntime, withURLParam(newRequest("POST", "/", nil), "runtimeId", runtimeID)).Want(200)
	if strings.TrimSpace(w.Body.String()) != `{"task":null}` {
		t.Fatalf("legacy public runtime dispatched another owner's agent: %s", w.Body.String())
	}
	var status pgtype.Text
	dbfx.QueryRow(t, `SELECT status FROM agent_task_queue WHERE id=$1`, taskID).Scan(&status)
	if status.String != "failed" {
		t.Fatalf("mismatched task status=%s, want failed", status.String)
	}
}

func TestPersonalRuntimeSecurityPublicOwnerIsImmutableBeforeDelivery(t *testing.T) {
	other := personalSecurityMember(t, "member")
	ctx := context.Background()
	runtimeID := dbfx.Runtime(t, "Immutable legacy public runtime", testutil.Cols{"visibility": "public"})
	agentID := dbfx.Agent(t, "Legacy delivery agent", runtimeID)
	issueID := dbfx.Issue(t, "Legacy delivery issue")
	taskID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID})
	runtimeSnapshot, err := testHandler.Queries.GetAgentRuntime(ctx, parseUUID(runtimeID))
	if err != nil {
		t.Fatal(err)
	}
	task, err := testHandler.TaskService.ClaimTaskForRuntime(ctx, parseUUID(runtimeID))
	if err != nil || task == nil || uuidToString(task.ID) != taskID {
		t.Fatalf("claim fixture: %v", err)
	}
	// Even legacy cloud rows without a canonical identity cannot change owner
	// between claim and delivery. Assert the database fence, then prove the
	// rejected hijack does not disrupt delivery to the original owner.
	_, err = testPool.Exec(ctx, `UPDATE agent_runtime SET owner_id=$1 WHERE id=$2`, other, runtimeID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("runtime owner change must fail with 23514, got %v", err)
	}
	storedRuntime, err := testHandler.Queries.GetAgentRuntime(ctx, parseUUID(runtimeID))
	if err != nil || storedRuntime.OwnerID != runtimeSnapshot.OwnerID {
		t.Fatalf("rejected owner change altered the runtime: %v", err)
	}
	request := newDaemonTokenRequest("POST", "/", nil, testWorkspaceID, "legacy-cloud")
	response, _, _, _, failure, err := testHandler.finalizeClaimDeliveryForTestWithRuntime(request, task, runtimeSnapshot, runtimeID, testWorkspaceID)
	if err != nil || failure != nil || response.AuthToken == "" {
		t.Fatalf("immutable owner must retain authorized delivery: failure=%+v error=%v", failure, err)
	}
	var tokenOwner string
	dbfx.QueryRow(t, `SELECT user_id::text FROM task_token WHERE task_id=$1`, taskID).Scan(&tokenOwner)
	if tokenOwner != uuidToString(runtimeSnapshot.OwnerID) {
		t.Fatal("delivery minted a task token for a different owner")
	}
}

func TestPersonalRuntimeSecurityMachineCredentialsCannotManageRuntimes(t *testing.T) {
	personalSecurityMember(t, "member")
	runtimeID := dbfx.Runtime(t, "Human-owned offline computer", testutil.Cols{
		"daemon_id": uuid.NewString(), "runtime_mode": "local", "provider": "codex", "status": "offline",
	})
	agentID := dbfx.Agent(t, "Human-owned bound agent", runtimeID)
	for _, source := range []string{"task_token", "cloud_pat"} {
		t.Run(source, func(t *testing.T) {
			for _, tc := range []struct {
				name, method string
				handler      http.HandlerFunc
				body         any
			}{
				{"list", "GET", testHandler.ListAgentRuntimes, nil},
				{"rename", "PATCH", testHandler.UpdateAgentRuntime, map[string]any{"custom_name": "Machine credential rename"}},
				{"delete", "DELETE", testHandler.DeleteAgentRuntime, nil},
				{"unbind_and_delete", "POST", testHandler.UnbindAgentsAndDeleteRuntime, map[string]any{"expected_active_agent_ids": []string{agentID}}},
				{"machine_delete_plan", "GET", testHandler.GetPersonalMachineDeletePlan, nil},
				{"delete_machine", "POST", testHandler.DeleteOfflineMachine, map[string]any{
					"expected_runtime_ids": []string{runtimeID}, "expected_active_agent_ids": []string{agentID},
				}},
				{"usage", "GET", testHandler.GetRuntimeUsage, nil},
				{"task_activity", "GET", testHandler.GetRuntimeTaskActivity, nil},
				{"usage_by_agent", "GET", testHandler.GetRuntimeUsageByAgent, nil},
				{"usage_by_hour", "GET", testHandler.GetRuntimeUsageByHour, nil},
			} {
				t.Run(tc.name, func(t *testing.T) {
					// Authentication stamps the real owner's ID for both machine
					// credentials. Ownership alone must not grant personal access.
					request := withURLParam(newRequest(tc.method, "/api/runtimes/"+runtimeID, tc.body), "runtimeId", runtimeID)
					request.Header.Set("X-Actor-Source", source)
					testutil.Call(t, tc.handler, request).Want(http.StatusForbidden)
				})
			}
		})
	}
	runtime, err := testHandler.Queries.GetAgentRuntime(context.Background(), parseUUID(runtimeID))
	if err != nil || runtime.Name != "Human-owned offline computer" || runtime.CustomName.Valid || runtime.Status != "offline" {
		t.Fatalf("denied machine requests changed runtime state: %v", err)
	}
	agent, err := testHandler.Queries.GetAgent(context.Background(), parseUUID(agentID))
	if err != nil || uuidToString(agent.RuntimeID) != runtimeID {
		t.Fatalf("denied deletion changed the agent binding: %v", err)
	}
	// The same member using a human session/PAT retains the intended access.
	testutil.Call(t, testHandler.GetRuntimeTaskActivity, withURLParam(newRequest("GET", "/", nil), "runtimeId", runtimeID)).Want(http.StatusOK)
	testutil.Call(t, testHandler.UpdateAgentRuntime, withURLParam(newRequest("PATCH", "/", map[string]any{"custom_name": "Human rename"}), "runtimeId", runtimeID)).Want(http.StatusOK)
}

func TestPersonalRuntimeSecurityMachineCredentialsCannotManageProfiles(t *testing.T) {
	personalSecurityMember(t, "member")
	ctx := context.Background()
	profileID := insertRuntimeProfileFixture(t, ctx, "Human-owned profile "+uuid.NewString(), "codex", "codex")
	blockedName := "Machine-owned profile " + uuid.NewString()
	for _, source := range []string{"task_token", "cloud_pat"} {
		t.Run(source, func(t *testing.T) {
			for _, tc := range []struct {
				name, method string
				handler      http.HandlerFunc
				body         any
			}{
				{"list", "GET", testHandler.ListRuntimeProfiles, nil},
				{"get", "GET", testHandler.GetRuntimeProfile, nil},
				{"create", "POST", testHandler.CreateRuntimeProfile, map[string]any{
					"display_name": blockedName, "protocol_family": "codex", "runtime_type": "codex", "command_name": "codex",
				}},
				{"update", "PATCH", testHandler.UpdateRuntimeProfile, map[string]any{"command_name": "machine-command"}},
				{"delete", "DELETE", testHandler.DeleteRuntimeProfile, nil},
			} {
				t.Run(tc.name, func(t *testing.T) {
					request := withURLParams(newRequest(tc.method, "/api/workspaces/"+testWorkspaceID+"/runtime-profiles/"+profileID, tc.body), "id", testWorkspaceID, "profileId", profileID)
					request.Header.Set("X-Actor-Source", source)
					testutil.Call(t, tc.handler, request).Want(http.StatusForbidden)
				})
			}
		})
	}
	profile, err := testHandler.Queries.GetRuntimeProfile(ctx, parseUUID(profileID))
	if err != nil || profile.CommandName != "codex" || uuidToString(profile.CreatedBy) != testUserID {
		t.Fatalf("denied machine requests changed the profile: %v", err)
	}
	var created int
	dbfx.QueryRow(t, `SELECT count(*) FROM personal_runtime_profile WHERE owner_id=$1 AND display_name=$2`, testUserID, blockedName).Scan(&created)
	if created != 0 {
		t.Fatal("denied profile creation left a canonical definition behind")
	}
	testutil.Call(t, testHandler.GetRuntimeProfile, withURLParams(newRequest("GET", "/", nil), "id", testWorkspaceID, "profileId", profileID)).Want(http.StatusOK)
	testutil.Call(t, testHandler.UpdateRuntimeProfile, withURLParams(newRequest("PATCH", "/", map[string]any{"command_name": "human-command"}), "id", testWorkspaceID, "profileId", profileID)).Want(http.StatusOK)
}
