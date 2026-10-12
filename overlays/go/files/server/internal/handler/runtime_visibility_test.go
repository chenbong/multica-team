package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestCanUseRuntimeForAgent_Pure exercises the pure predicate behind the
// CreateAgent / UpdateAgent runtime gate. The truth table is the single
// product contract: runtimes are personal, including legacy public rows.
// Workspace roles and visibility never substitute for ownership.
// An ownerless runtime is usable by nobody,
// public included: it cannot run tasks either (task claim requires a runtime
// owner to mint the agent token, MUL-3292), so the gate refuses it up front
// instead of letting the binding succeed and every task on it fail.
func TestCanUseRuntimeForAgent_Pure(t *testing.T) {
	ownerUserID := "11111111-1111-1111-1111-111111111111"
	otherUserID := "22222222-2222-2222-2222-222222222222"

	privateRT := db.AgentRuntime{
		OwnerID:    util.MustParseUUID(ownerUserID),
		Visibility: "private",
	}
	publicRT := db.AgentRuntime{
		OwnerID:    util.MustParseUUID(ownerUserID),
		Visibility: "public",
	}
	ownerlessPrivateRT := db.AgentRuntime{Visibility: "private"}
	ownerlessPublicRT := db.AgentRuntime{Visibility: "public"}

	cases := []struct {
		name   string
		userID string
		role   string
		rt     db.AgentRuntime
		want   bool
	}{
		// no workspace owner / admin override: role never widens access
		{"workspace owner on private runtime owned by another", otherUserID, "owner", privateRT, false},
		{"workspace admin on private runtime owned by another", otherUserID, "admin", privateRT, false},
		{"workspace admin on someone else's public runtime", otherUserID, "admin", publicRT, false},
		// runtime owner
		{"runtime owner on own private runtime", ownerUserID, "member", privateRT, true},
		{"runtime owner on own public runtime", ownerUserID, "member", publicRT, true},
		{"runtime owner who is also a workspace admin", ownerUserID, "admin", privateRT, true},
		// legacy public rows still require ownership
		{"plain member on someone else's public runtime", otherUserID, "member", publicRT, false},
		// private runtime owned by someone else is denied to everyone
		{"plain member on someone else's private runtime", otherUserID, "member", privateRT, false},
		{"plain member with empty role on private runtime", otherUserID, "", privateRT, false},
		// ownerless runtimes are refused whatever their visibility — nobody
		// can be issued a task token for them (MUL-3292)
		{"workspace owner on an ownerless private runtime", otherUserID, "owner", ownerlessPrivateRT, false},
		{"plain member on an ownerless public runtime", otherUserID, "member", ownerlessPublicRT, false},
		{"workspace admin on an ownerless public runtime", otherUserID, "admin", ownerlessPublicRT, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			member := db.Member{
				UserID: util.MustParseUUID(tc.userID),
				Role:   tc.role,
			}
			got := canUseRuntimeForAgent(member, tc.rt)
			if got != tc.want {
				t.Fatalf("canUseRuntimeForAgent(role=%s, visibility=%s, owner=%s, caller=%s) = %v; want %v",
					tc.role, tc.rt.Visibility, ownerUserID, tc.userID, got, tc.want)
			}
		})
	}
}

// runtimeVisibilityFixture builds the three-actor world the gate needs to
// exercise: a private runtime owned by a non-admin member, a separate plain
// member in the same workspace, and the workspace owner (testUserID). The
// runtime is registered through agent_runtime directly so the test doesn't
// depend on the daemon-registration code path. Returns runtime id, runtime
// owner user id, and the plain member's user id.
func runtimeVisibilityFixture(t *testing.T) (runtimeID, runtimeOwnerID, plainMemberID string) {
	t.Helper()
	ctx := context.Background()

	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ('Runtime Owner', 'runtime-owner@multica.test')
		RETURNING id
	`).Scan(&runtimeOwnerID); err != nil {
		t.Fatalf("create runtime owner user: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM "user" WHERE email = 'runtime-owner@multica.test'`)
	})

	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'member')
	`, testWorkspaceID, runtimeOwnerID); err != nil {
		t.Fatalf("add runtime owner as member: %v", err)
	}

	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ('Plain Runtime Member', 'plain-runtime-member@multica.test')
		RETURNING id
	`).Scan(&plainMemberID); err != nil {
		t.Fatalf("create plain member user: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM "user" WHERE email = 'plain-runtime-member@multica.test'`)
	})

	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'member')
	`, testWorkspaceID, plainMemberID); err != nil {
		t.Fatalf("add plain member: %v", err)
	}

	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status,
			device_info, metadata, owner_id, visibility, last_seen_at
		)
		VALUES ($1, NULL, 'Visibility Test Runtime', 'cloud', 'visibility_test_provider', 'online', 'visibility test', '{}'::jsonb, $2, 'private', now())
		RETURNING id
	`, testWorkspaceID, runtimeOwnerID).Scan(&runtimeID); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})

	return runtimeID, runtimeOwnerID, plainMemberID
}

// TestCreateAgent_RejectsPrivateRuntimeForNonOwner walks the gate end-to-end:
// the runtime is private and owned by a plain member, so only that owner can
// create agents on it. Neither a plain workspace member nor the workspace
// owner may — the admin override is gone (MUL-6126), which is what keeps the
// API/CLI answer identical to the disabled runtime row the web UI renders.
func TestCreateAgent_RejectsPrivateRuntimeForNonOwner(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeID, runtimeOwnerID, plainMemberID := runtimeVisibilityFixture(t)

	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM agent WHERE workspace_id = $1 AND name LIKE 'runtime-visibility-test-%'`,
			testWorkspaceID)
	})

	body := func(name string) map[string]any {
		return map[string]any{
			"name":                 name,
			"description":          "",
			"runtime_id":           runtimeID,
			"visibility":           "private",
			"max_concurrent_tasks": 1,
		}
	}

	// Workspace owner (testUserID): refused. This is the API/CLI half of
	// personal ownership — workspace administrators cannot publish the machine
	// or bind their own agents to it.
	w := httptest.NewRecorder()
	testHandler.CreateAgent(w, newRequest(http.MethodPost, "/api/agents", body("runtime-visibility-test-admin")))
	if w.Code != http.StatusForbidden {
		t.Fatalf("CreateAgent as workspace owner on another member's private runtime: expected 403, got %d: %s", w.Code, w.Body.String())
	}

	// Runtime owner: allowed because they own the runtime.
	w = httptest.NewRecorder()
	testHandler.CreateAgent(w, newRequestAs(runtimeOwnerID, http.MethodPost, "/api/agents", body("runtime-visibility-test-runtime-owner")))
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateAgent as runtime owner: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// Plain member: this is the hole MUL-2062 closes — must be 403.
	w = httptest.NewRecorder()
	testHandler.CreateAgent(w, newRequestAs(plainMemberID, http.MethodPost, "/api/agents", body("runtime-visibility-test-plain-member")))
	if w.Code != http.StatusForbidden {
		t.Fatalf("CreateAgent as plain member on private runtime: expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

// A legacy public row is still usable only by its owner.
func TestCreateAgent_RejectsLegacyPublicRuntimeForPlainMember(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeID, runtimeOwnerID, plainMemberID := runtimeVisibilityFixture(t)
	ctx := context.Background()
	if _, err := testPool.Exec(ctx,
		`UPDATE agent_runtime SET visibility = 'public' WHERE id = $1`, runtimeID,
	); err != nil {
		t.Fatalf("flip runtime to public: %v", err)
	}

	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM agent WHERE workspace_id = $1 AND name = 'runtime-visibility-test-public-runtime'`,
			testWorkspaceID)
	})

	body := map[string]any{
		"name":                 "runtime-visibility-test-public-runtime",
		"description":          "",
		"runtime_id":           runtimeID,
		"visibility":           "private",
		"max_concurrent_tasks": 1,
	}
	w := httptest.NewRecorder()
	testHandler.CreateAgent(w, newRequestAs(plainMemberID, http.MethodPost, "/api/agents", body))
	if w.Code != http.StatusForbidden {
		t.Fatalf("CreateAgent as plain member on legacy public runtime: expected 403, got %d: %s", w.Code, w.Body.String())
	}
	testutil.Call(t, testHandler.CreateAgent, newRequestAs(runtimeOwnerID, http.MethodPost, "/api/agents", body)).Want(http.StatusCreated)
}

func TestListAgentRuntimes_HidesOtherMembersPrivateRuntimes(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	privateRuntimeID, _, plainMemberID := runtimeVisibilityFixture(t)
	ownedRuntimeID := dbfx.Runtime(t, "Plain member computer", testutil.Cols{"owner_id": plainMemberID})

	if _, err := testPool.Exec(context.Background(),
		`UPDATE agent_runtime SET visibility = 'public' WHERE id = $1`, testRuntimeID,
	); err != nil {
		t.Fatalf("make fixture runtime public: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`UPDATE agent_runtime SET visibility = 'private' WHERE id = $1`, testRuntimeID)
	})

	w := testutil.Call(t, testHandler.ListAgentRuntimes,
		newRequestAs(plainMemberID, http.MethodGet, "/api/runtimes", nil),
	).Want(http.StatusOK)
	var runtimes []AgentRuntimeResponse
	w.JSON(&runtimes)

	for _, runtime := range runtimes {
		if runtime.ID == privateRuntimeID {
			t.Fatalf("private runtime %s owned by another member was returned", privateRuntimeID)
		}
	}
	if len(runtimes) != 1 || runtimes[0].ID != ownedRuntimeID {
		t.Fatalf("visible runtimes = %#v; want only owned runtime %s", runtimes, ownedRuntimeID)
	}
}

func TestListAgentRuntimes_AdminSeesOnlyOwnedRuntimes(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	privateRuntimeID, _, adminID := runtimeVisibilityFixture(t)
	promoteRuntimeTestMemberToAdmin(t, adminID)
	ownedRuntimeID := dbfx.Runtime(t, "Admin's own computer", testutil.Cols{"owner_id": adminID})
	var runtimes []AgentRuntimeResponse
	testutil.Call(t, testHandler.ListAgentRuntimes, newRequestAs(adminID, http.MethodGet, "/api/runtimes", nil)).Want(http.StatusOK).JSON(&runtimes)
	for _, runtime := range runtimes {
		if runtime.ID == privateRuntimeID || runtime.OwnerID == nil || *runtime.OwnerID != adminID {
			t.Fatal("admin list exposed a foreign runtime")
		}
	}
	if len(runtimes) != 1 || runtimes[0].ID != ownedRuntimeID {
		t.Fatalf("admin runtimes = %#v; want owned runtime only", runtimes)
	}
}

func TestPrivateRuntimeReadEndpointsHideKnownRuntimeFromNonOwners(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeID, runtimeOwnerID, plainMemberID := runtimeVisibilityFixture(t)

	modelRequest, err := testHandler.ModelListStore.Create(context.Background(), runtimeID)
	if err != nil {
		t.Fatalf("create model list request: %v", err)
	}
	localSkillRequest, err := testHandler.LocalSkillListStore.Create(context.Background(), runtimeID)
	if err != nil {
		t.Fatalf("create local skill list request: %v", err)
	}

	tests := []struct {
		name   string
		method string
		path   string
		handle func(http.ResponseWriter, *http.Request)
		params []string
	}{
		{"usage", http.MethodGet, "/api/runtimes/" + runtimeID + "/usage", testHandler.GetRuntimeUsage, []string{"runtimeId", runtimeID}},
		{"activity", http.MethodGet, "/api/runtimes/" + runtimeID + "/activity", testHandler.GetRuntimeTaskActivity, []string{"runtimeId", runtimeID}},
		{"usage by agent", http.MethodGet, "/api/runtimes/" + runtimeID + "/usage/by-agent", testHandler.GetRuntimeUsageByAgent, []string{"runtimeId", runtimeID}},
		{"usage by hour", http.MethodGet, "/api/runtimes/" + runtimeID + "/usage/by-hour", testHandler.GetRuntimeUsageByHour, []string{"runtimeId", runtimeID}},
		{"model discovery", http.MethodPost, "/api/runtimes/" + runtimeID + "/models", testHandler.InitiateListModels, []string{"runtimeId", runtimeID}},
		{"model discovery poll", http.MethodGet, "/api/runtimes/" + runtimeID + "/models/" + modelRequest.ID, testHandler.GetModelListRequest, []string{"runtimeId", runtimeID, "requestId", modelRequest.ID}},
		{"local skill discovery", http.MethodPost, "/api/runtimes/" + runtimeID + "/local-skills", testHandler.InitiateListLocalSkills, []string{"runtimeId", runtimeID}},
		{"local skill discovery poll", http.MethodGet, "/api/runtimes/" + runtimeID + "/local-skills/" + localSkillRequest.ID, testHandler.GetLocalSkillListRequest, []string{"runtimeId", runtimeID, "requestId", localSkillRequest.ID}},
	}

	for _, tc := range tests {
		t.Run("plain member/"+tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := withURLParams(newRequestAs(plainMemberID, tc.method, tc.path, nil), tc.params...)
			tc.handle(w, req)
			if w.Code != http.StatusNotFound {
				t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
			}
		})
	}

	promoteRuntimeTestMemberToAdmin(t, plainMemberID)
	for _, tc := range tests {
		t.Run("workspace admin/"+tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := withURLParams(newRequestAs(plainMemberID, tc.method, tc.path, nil), tc.params...)
			tc.handle(w, req)
			if w.Code != http.StatusNotFound {
				t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
			}
		})
	}

	// The owner remains allowed to start discovery on their own private machine.
	w := httptest.NewRecorder()
	req := withURLParam(newRequestAs(runtimeOwnerID, http.MethodPost, "/api/runtimes/"+runtimeID+"/models", nil), "runtimeId", runtimeID)
	testHandler.InitiateListModels(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("owner model discovery: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if _, err := testPool.Exec(context.Background(), `UPDATE agent_runtime SET visibility = 'public' WHERE id = $1`, runtimeID); err != nil {
		t.Fatalf("make runtime public: %v", err)
	}
	w = httptest.NewRecorder()
	req = withURLParam(newRequestAs(plainMemberID, http.MethodPost, "/api/runtimes/"+runtimeID+"/models", nil), "runtimeId", runtimeID)
	testHandler.InitiateListModels(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("legacy public runtime discovery by non-owner: expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestUpdateAgent_RejectsRebindToPrivateRuntime is the regression for the
// "update can bypass create" backdoor — without this gate a plain member
// could create an agent on a public runtime, then re-bind it onto someone
// else's private runtime via UpdateAgent.
func TestUpdateAgent_RejectsRebindToPrivateRuntime(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	privateRuntimeID, _, plainMemberID := runtimeVisibilityFixture(t)

	ctx := context.Background()
	// Create a public runtime that the plain member can legitimately own
	// an agent on, then we try to move the agent onto the private runtime.
	var publicRuntimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status,
			device_info, metadata, owner_id, visibility, last_seen_at
		)
		VALUES ($1, NULL, 'Public Runtime', 'cloud', 'visibility_test_public_provider', 'online', 'public', '{}'::jsonb, $2, 'public', now())
		RETURNING id
	`, testWorkspaceID, plainMemberID).Scan(&publicRuntimeID); err != nil {
		t.Fatalf("create public runtime: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, publicRuntimeID)
	})

	var agentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id,
			instructions, custom_env, custom_args
		)
		VALUES ($1, 'rebind-test-agent', '', 'cloud', '{}'::jsonb,
		        $2, 'private', 1, $3, '', '{}'::jsonb, '[]'::jsonb)
		RETURNING id
	`, testWorkspaceID, publicRuntimeID, plainMemberID).Scan(&agentID); err != nil {
		t.Fatalf("create agent on public runtime: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
	})

	body := map[string]any{
		"runtime_id": privateRuntimeID,
	}
	w := httptest.NewRecorder()
	req := newRequestAs(plainMemberID, http.MethodPut, "/api/agents/"+agentID, body)
	req = withURLParam(req, "id", agentID)
	testHandler.UpdateAgent(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("UpdateAgent rebinding to private runtime: expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

// TestUpdateAgent_WorkspaceAdminCannotRebindToMemberPrivateRuntime walks the
// personal runtime contract: an admin cannot bind to another member's machine,
// and even its owner cannot publish it to grant cross-owner binding.
func TestUpdateAgent_WorkspaceAdminCannotRebindToMemberPrivateRuntime(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	memberPrivateRuntimeID, runtimeOwnerID, _ := runtimeVisibilityFixture(t)

	ctx := context.Background()
	// The admin's own agent, on the fixture runtime they own — so the only
	// thing this update can trip over is the runtime gate.
	var agentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id,
			instructions, custom_env, custom_args
		)
		VALUES ($1, 'admin-rebind-test-agent', '', 'cloud', '{}'::jsonb,
		        $2, 'private', 1, $3, '', '{}'::jsonb, '[]'::jsonb)
		RETURNING id
	`, testWorkspaceID, testRuntimeID, testUserID).Scan(&agentID); err != nil {
		t.Fatalf("create agent on the workspace owner's runtime: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
	})

	rebind := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := newRequest(http.MethodPut, "/api/agents/"+agentID, map[string]any{
			"runtime_id": memberPrivateRuntimeID,
		})
		testHandler.UpdateAgent(w, withURLParam(req, "id", agentID))
		return w
	}

	setVisibility := func(actorID, visibility string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := newRequestAs(actorID, http.MethodPatch, "/api/runtimes/"+memberPrivateRuntimeID, map[string]any{
			"visibility": visibility,
		})
		testHandler.UpdateAgentRuntime(w, withURLParam(req, "runtimeId", memberPrivateRuntimeID))
		return w
	}

	if w := rebind(); w.Code != http.StatusForbidden {
		t.Fatalf("UpdateAgent as workspace owner onto a member's private runtime: expected 403, got %d: %s",
			w.Code, w.Body.String())
	}

	// …and cannot route around it by publishing the member's machine itself.
	if w := setVisibility(testUserID, "public"); w.Code != http.StatusForbidden {
		t.Fatalf("UpdateAgentRuntime as workspace owner on a member's runtime: expected 403, got %d: %s",
			w.Code, w.Body.String())
	}

	// Sharing is no longer a runtime operation, even for its owner.
	if w := setVisibility(runtimeOwnerID, "public"); w.Code != http.StatusBadRequest {
		t.Fatalf("UpdateAgentRuntime as runtime owner → public: expected 400, got %d: %s",
			w.Code, w.Body.String())
	}

	if w := rebind(); w.Code != http.StatusForbidden {
		t.Fatalf("UpdateAgent after rejected sharing: expected 403, got %d: %s",
			w.Code, w.Body.String())
	}
}

// TestCanSetRuntimeVisibility_Pure pins the narrower gate: only the runtime
// owner may normalize a legacy row to private. The public API separately
// refuses sharing; admins also have no rename or delete override.
func TestCanSetRuntimeVisibility_Pure(t *testing.T) {
	ownerUserID := "11111111-1111-1111-1111-111111111111"
	otherUserID := "22222222-2222-2222-2222-222222222222"

	ownedRT := db.AgentRuntime{
		OwnerID:    util.MustParseUUID(ownerUserID),
		Visibility: "private",
	}
	ownerlessRT := db.AgentRuntime{Visibility: "private"}

	cases := []struct {
		name   string
		userID string
		role   string
		rt     db.AgentRuntime
		want   bool
	}{
		{"runtime owner", ownerUserID, "member", ownedRT, true},
		{"workspace owner who does not own the runtime", otherUserID, "owner", ownedRT, false},
		{"workspace admin who does not own the runtime", otherUserID, "admin", ownedRT, false},
		{"plain member", otherUserID, "member", ownedRT, false},
		{"workspace owner on an ownerless runtime", otherUserID, "owner", ownerlessRT, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			member := db.Member{
				UserID: util.MustParseUUID(tc.userID),
				Role:   tc.role,
			}
			if got := canSetRuntimeVisibility(member, tc.rt); got != tc.want {
				t.Fatalf("canSetRuntimeVisibility(role=%s, caller=%s) = %v; want %v",
					tc.role, tc.userID, got, tc.want)
			}
		})
	}
}

// Publishing a personal runtime must fail without changing its stored visibility.
func TestUpdateAgentRuntime_RejectsPublicVisibility(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID, runtimeOwnerID, _ := runtimeVisibilityFixture(t)
	req := withURLParam(newRequestAs(runtimeOwnerID, http.MethodPatch, "/", map[string]any{"visibility": "public"}), "runtimeId", runtimeID)
	testutil.Call(t, testHandler.UpdateAgentRuntime, req).Want(http.StatusBadRequest)
	runtime, err := testHandler.Queries.GetAgentRuntime(context.Background(), util.MustParseUUID(runtimeID))
	if err != nil || runtime.Visibility != "private" {
		t.Fatalf("rejected sharing changed visibility: %v", err)
	}
}

// TestUpdateAgentRuntime_IgnoresTimezoneField guards the RFC migration that
// dropped `timezone` from UpdateAgentRuntimeRequest: a PATCH body still
// carrying `timezone` must not error, must not echo a `timezone` key back,
// and must still apply the recognised rename field. Timezone is now a
// user-level preference, not a per-runtime one.
func TestUpdateAgentRuntime_IgnoresTimezoneField(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeID, runtimeOwnerID, _ := runtimeVisibilityFixture(t)

	w := httptest.NewRecorder()
	// A legacy timezone field must not interfere with an authorized rename.
	req := newRequestAs(runtimeOwnerID, http.MethodPatch, "/api/runtimes/"+runtimeID, map[string]any{
		"timezone":    "Asia/Tokyo",
		"visibility":  "private",
		"custom_name": "Timezone compatibility",
	})
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.UpdateAgentRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH with stray timezone: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// The response must carry no `timezone` key — runtimes have no such field.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, present := raw["timezone"]; present {
		t.Errorf("response unexpectedly contains a timezone key: %s", w.Body.String())
	}

	// The rename was applied without changing personal visibility.
	var resp AgentRuntimeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Visibility != "private" || resp.CustomName == nil || *resp.CustomName != "Timezone compatibility" {
		t.Errorf("authorized rename was not applied: %+v", resp)
	}
}

// TestUpdateAgentRuntime_InvalidVisibilityReturns400 verifies that an invalid
// visibility value is rejected with 400 before any mutation runs.
func TestUpdateAgentRuntime_InvalidVisibilityReturns400(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeID, runtimeOwnerID, _ := runtimeVisibilityFixture(t)

	w := httptest.NewRecorder()
	req := newRequestAs(runtimeOwnerID, http.MethodPatch, "/api/runtimes/"+runtimeID, map[string]any{
		"visibility": "everyone",
	})
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.UpdateAgentRuntime(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("PATCH with invalid visibility: expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// Runtime owners may echo private visibility, but nobody may publish a runtime.
func TestUpdateAgentRuntime_SharingRejectedForEveryActor(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID, runtimeOwnerID, plainMemberID := runtimeVisibilityFixture(t)
	for _, tc := range []struct {
		actor, visibility string
		status            int
	}{
		{runtimeOwnerID, "public", http.StatusBadRequest},
		{runtimeOwnerID, "private", http.StatusOK},
		{runtimeOwnerID, "everyone", http.StatusBadRequest},
		{testUserID, "private", http.StatusForbidden},
		{plainMemberID, "public", http.StatusForbidden},
	} {
		request := withURLParam(newRequestAs(tc.actor, http.MethodPatch, "/", map[string]any{"visibility": tc.visibility}), "runtimeId", runtimeID)
		testutil.Call(t, testHandler.UpdateAgentRuntime, request).Want(tc.status)
	}
	runtime, err := testHandler.Queries.GetAgentRuntime(context.Background(), util.MustParseUUID(runtimeID))
	if err != nil || runtime.Visibility != "private" {
		t.Fatalf("runtime visibility changed: %v", err)
	}
}

// Echoing private visibility does not grant admins permission to rename a peer machine.
func TestUpdateAgentRuntime_AdminCannotRenameWithUnchangedVisibility(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID, _, _ := runtimeVisibilityFixture(t)
	req := withURLParam(newRequest(http.MethodPatch, "/", map[string]any{"visibility": "private", "custom_name": "Renamed By Admin"}), "runtimeId", runtimeID)
	testutil.Call(t, testHandler.UpdateAgentRuntime, req).Want(http.StatusForbidden)
	runtime, err := testHandler.Queries.GetAgentRuntime(context.Background(), util.MustParseUUID(runtimeID))
	if err != nil || runtime.CustomName.Valid || runtime.Visibility != "private" {
		t.Fatalf("denied admin update changed runtime: %v", err)
	}
}
