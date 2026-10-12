package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestCanManageAgentEnv_Pure exercises the predicate behind the env
// endpoints without a database. Ownership must count for a plain member
// regardless of workspace role; admins have no override, and a NULL
// agent.owner_id must never match anyone: the column is nullable and
// uuidToString renders a NULL UUID as "".
func TestCanManageAgentEnv_Pure(t *testing.T) {
	ownerUserID := "11111111-1111-1111-1111-111111111111"
	otherUserID := "22222222-2222-2222-2222-222222222222"

	owned := db.Agent{OwnerID: util.MustParseUUID(ownerUserID)}
	orphaned := db.Agent{} // owner_id IS NULL

	cases := []struct {
		name   string
		agent  db.Agent
		userID string
		role   string
		want   bool
	}{
		{"workspace owner, not agent owner", owned, otherUserID, "owner", false},
		{"workspace admin, not agent owner", owned, otherUserID, "admin", false},
		{"agent owner with member role", owned, ownerUserID, "member", true},
		{"agent owner with admin role", owned, ownerUserID, "admin", true},
		{"plain member, not agent owner", owned, otherUserID, "member", false},
		{"plain member with no role string", owned, otherUserID, "", false},
		{"null owner_id, plain member", orphaned, otherUserID, "member", false},
		{"null owner_id, workspace admin", orphaned, otherUserID, "admin", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			member := db.Member{UserID: util.MustParseUUID(tc.userID), Role: tc.role}
			if got := canManageAgentEnv(tc.agent, member); got != tc.want {
				t.Fatalf("canManageAgentEnv(owner=%q, user=%s, role=%s) = %v; want %v",
					uuidToString(tc.agent.OwnerID), tc.userID, tc.role, got, tc.want)
			}
		})
	}
}

// agentEnvOwnerFixture creates a plain (role=member) workspace member and
// an agent owned by that member, seeded with a known custom_env map. This
// is the shape a member creating their own agent produces: POST
// /api/agents stores the caller as owner_id and accepts custom_env from
// any member.
func agentEnvOwnerFixture(t *testing.T, agentName, email string) (agentID, ownerUserID string) {
	t.Helper()
	ownerUserID = createPermissionTestMember(t, email)
	runtimeID := dbfx.Runtime(t, agentName+" runtime", testutil.Cols{"owner_id": ownerUserID})
	agentID = dbfx.Agent(t, agentName, runtimeID, testutil.Cols{
		"owner_id": ownerUserID, "visibility": "workspace", "permission_mode": "public_to",
		"custom_env": testutil.Raw(`'{"API_KEY":"secret-value"}'::jsonb`),
	})
	dbfx.Insert(t, "agent_invocation_target", testutil.Cols{"agent_id": agentID, "target_type": "workspace", "target_id": testWorkspaceID})
	return agentID, ownerUserID
}

// TestAgentEnv_AgentOwnerMemberCanRevealAndUpdate is the MUL-5438 fix:
// the human who owns an agent can reveal and rotate its env even with a
// plain `member` workspace role. The audit assertions come along because
// the reveal/update trail must keep naming the real human operator and
// must still never carry values.
func TestAgentEnv_AgentOwnerMemberCanRevealAndUpdate(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	agentID, ownerUserID := agentEnvOwnerFixture(t, "env-owner-member-agent", "env-owner-member@multica.test")

	req := withURLParam(newRequestAs(ownerUserID, http.MethodGet, "/api/agents/"+agentID+"/env", nil), "id", agentID)
	w := httptest.NewRecorder()
	testHandler.GetAgentEnv(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetAgentEnv as agent owner: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp AgentEnvResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode reveal response: %v", err)
	}
	if resp.CustomEnv["API_KEY"] != "secret-value" {
		t.Fatalf("expected plaintext API_KEY, got %v", resp.CustomEnv)
	}

	body := map[string]any{"custom_env": map[string]string{"API_KEY": "rotated-value"}}
	req = withURLParam(newRequestAs(ownerUserID, http.MethodPut, "/api/agents/"+agentID+"/env", body), "id", agentID)
	w = httptest.NewRecorder()
	testHandler.UpdateAgentEnv(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateAgentEnv as agent owner: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var stored string
	if err := testPool.QueryRow(ctx, `SELECT custom_env::text FROM agent WHERE id = $1`, agentID).Scan(&stored); err != nil {
		t.Fatalf("read back custom_env: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(stored), &got); err != nil {
		t.Fatalf("decode stored custom_env: %v", err)
	}
	if got["API_KEY"] != "rotated-value" {
		t.Errorf("expected the rotated value on disk, got %v", got)
	}

	// Audit must attribute the acting human — the member, not the
	// workspace owner — and must never carry the value itself.
	var actorID, details string
	if err := testPool.QueryRow(ctx, `
		SELECT actor_id::text, details::text FROM activity_log
		WHERE workspace_id = $1 AND action = 'agent_env_updated' AND details->>'agent_id' = $2
		ORDER BY created_at DESC LIMIT 1
	`, testWorkspaceID, agentID).Scan(&actorID, &details); err != nil {
		t.Fatalf("expected agent_env_updated activity row: %v", err)
	}
	if actorID != ownerUserID {
		t.Errorf("audit actor_id = %s; want the acting agent owner %s", actorID, ownerUserID)
	}
	if strings.Contains(details, "secret-value") || strings.Contains(details, "rotated-value") {
		t.Errorf("activity details must NOT contain env values, got: %s", details)
	}
}

// TestAgentEnv_UnrelatedMemberForbidden keeps the blast radius of
// MUL-5438 at exactly one agent: ownership, not membership, is what
// opens the endpoint. A member who owns some other agent still gets 403
// on this one.
func TestAgentEnv_UnrelatedMemberForbidden(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID, _ := agentEnvOwnerFixture(t, "env-unrelated-target-agent", "env-target-owner@multica.test")
	strangerID := createPermissionTestMember(t, "env-stranger@multica.test")

	cases := []struct {
		name string
		fn   func(http.ResponseWriter, *http.Request)
		body any
	}{
		{"reveal", testHandler.GetAgentEnv, nil},
		{"update", testHandler.UpdateAgentEnv, map[string]any{"custom_env": map[string]string{"API_KEY": "stolen"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			method := http.MethodGet
			if tc.body != nil {
				method = http.MethodPut
			}
			req := withURLParam(newRequestAs(strangerID, method, "/api/agents/"+agentID+"/env", tc.body), "id", agentID)
			w := httptest.NewRecorder()
			tc.fn(w, req)
			if w.Code != http.StatusForbidden {
				t.Fatalf("expected 403 for an unrelated member, got %d: %s", w.Code, w.Body.String())
			}
		})
	}

	// The rejected PUT must not have touched the stored map.
	var stored string
	if err := testPool.QueryRow(context.Background(), `SELECT custom_env::text FROM agent WHERE id = $1`, agentID).Scan(&stored); err != nil {
		t.Fatalf("read back custom_env: %v", err)
	}
	if !strings.Contains(stored, "secret-value") {
		t.Errorf("forbidden update must leave custom_env untouched, got: %s", stored)
	}
}

// Workspace governance does not grant access to another owner\'s execution secrets.
func TestAgentEnv_WorkspaceRolesCannotManageAnotherOwnersSecrets(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, _ := agentEnvOwnerFixture(t, "env-role-check-agent", "env-role-check-owner@multica.test")
	adminID := createPermissionTestAdmin(t, "env-admin@multica.test")
	for _, actorID := range []string{testUserID, adminID} {
		testutil.Call(t, testHandler.GetAgentEnv, withURLParam(newRequestAs(actorID, http.MethodGet, "/", nil), "id", agentID)).Want(http.StatusForbidden)
		testutil.Call(t, testHandler.UpdateAgentEnv, withURLParam(newRequestAs(actorID, http.MethodPut, "/", map[string]any{"custom_env": map[string]string{"API_KEY": "changed"}}), "id", agentID)).Want(http.StatusForbidden)
	}
	var stored string
	dbfx.QueryRow(t, "SELECT custom_env::text FROM agent WHERE id=$1", agentID).Scan(&stored)
	if !strings.Contains(stored, "secret-value") || strings.Contains(stored, "changed") {
		t.Fatal("denied governance update changed the secret")
	}
}

// TestAgentEnv_AgentActorRejectedForOwnedAgent is the boundary MUL-5438
// must not widen. TestAgentEnv_AgentActorRejected already covers an agent
// backed by a workspace owner; this covers the new shape the fix creates —
// an agent whose backing human is the target agent's own owner. The actor
// check runs before the ownership check precisely so this stays a 403.
func TestAgentEnv_AgentActorRejectedForOwnedAgent(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	targetID, ownerUserID := agentEnvOwnerFixture(t, "env-actor-target-agent", "env-actor-owner@multica.test")

	// The calling agent runs on behalf of the same human who owns the
	// target agent, so the owner check alone would let it through.
	hostAgentID := createHandlerTestAgent(t, "env-actor-host-agent", nil)
	if _, err := testPool.Exec(ctx, `UPDATE agent SET owner_id = $1 WHERE id = $2`, ownerUserID, hostAgentID); err != nil {
		t.Fatalf("assign host agent owner: %v", err)
	}
	hostTaskID := createHandlerTestTaskForAgent(t, hostAgentID)

	cases := []struct {
		name string
		fn   func(http.ResponseWriter, *http.Request)
		body any
	}{
		{"reveal", testHandler.GetAgentEnv, nil},
		{"update", testHandler.UpdateAgentEnv, map[string]any{"custom_env": map[string]string{"API_KEY": "exfiltrated"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			method := http.MethodGet
			if tc.body != nil {
				method = http.MethodPut
			}
			req := withURLParam(newRequestAs(ownerUserID, method, "/api/agents/"+targetID+"/env", tc.body), "id", targetID)
			req.Header.Set("X-Agent-ID", hostAgentID)
			req.Header.Set("X-Task-ID", hostTaskID)
			w := httptest.NewRecorder()
			tc.fn(w, req)
			if w.Code != http.StatusForbidden {
				t.Fatalf("expected 403 from an agent actor, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}
