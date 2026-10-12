package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"net/http/httptest"
	"testing"
)

func TestPersonalRuntimeTransactionRetry(t *testing.T) {
	attempts := 0
	err := retryPersonalRuntimeTx(context.Background(), func() error {
		attempts++
		if attempts == 1 {
			return fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: "40001"})
		}
		return nil
	})
	if err != nil || attempts != 2 {
		t.Fatalf("retry failed: attempts=%d error=%v", attempts, err)
	}
	attempts = 0
	uncertain := errors.New("connection reset during commit")
	err = retryPersonalRuntimeTx(context.Background(), func() error { attempts++; return uncertain })
	if !errors.Is(err, uncertain) || attempts != 1 {
		t.Fatal("must not replay an uncertain commit")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = retryPersonalRuntimeTx(ctx, func() error { return &pgconn.PgError{Code: "40001"} })
	if !errors.Is(err, context.Canceled) {
		t.Fatal("retry ignored cancellation")
	}
}

func TestPersonalRuntimeLegacyIdentityRetirement(t *testing.T) {
	ctx := context.Background()
	ws2 := dbfx.Workspace(t, "Legacy B", "legacy-"+uuid.NewString())
	dbfx.Member(t, ws2, testUserID, "member")
	oldDaemon := "legacy-" + uuid.NewString()
	newDaemon := uuid.NewString()
	oldA := dbfx.Runtime(t, "Old identity", testutil.Cols{"daemon_id": oldDaemon, "runtime_mode": "local", "provider": "codex"})
	if err := testHandler.ensurePersonalRuntimes(ctx, parseUUID(ws2), testUserID); err != nil {
		t.Fatal(err)
	}
	var oldB, canonical string
	if err := testPool.QueryRow(ctx, `SELECT id,personal_runtime_id FROM agent_runtime WHERE workspace_id=$1 AND owner_id=$2 AND daemon_id=$3`, ws2, testUserID, oldDaemon).Scan(&oldB, &canonical); err != nil {
		t.Fatal(err)
	}
	newA := dbfx.Runtime(t, "New identity", testutil.Cols{"daemon_id": newDaemon, "runtime_mode": "local", "provider": "codex"})
	aid := dbfx.Agent(t, "Preserved legacy agent", oldA)
	tid := dbfx.Task(t, aid, testutil.Cols{"runtime_id": oldA, "status": "completed", "started_at": testutil.Raw("now()-interval '1 hour'"), "completed_at": testutil.Raw("now()")})
	if err := testHandler.mergeLegacyRuntime(ctx, parseUUID(newA), parseUUID(oldA), oldDaemon, "codex"); err != nil {
		t.Fatal(err)
	}
	for _, ws := range []string{testWorkspaceID, ws2} {
		if err := testHandler.ensurePersonalRuntimes(ctx, parseUUID(ws), testUserID); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent_runtime WHERE workspace_id=$1 AND daemon_id=$2`, testWorkspaceID, oldDaemon).Scan(&count); err != nil || count != 0 {
		t.Fatalf("retired identity reappeared: %d %v", count, err)
	}
	w := httptest.NewRecorder()
	req := newRequest("GET", "/", nil)
	req.Header.Set("X-Workspace-ID", ws2)
	testHandler.ListAgentRuntimes(w, req)
	if w.Code != 200 {
		t.Fatalf("list: %s", w.Body.String())
	}
	var listed []AgentRuntimeResponse
	json.Unmarshal(w.Body.Bytes(), &listed)
	for _, row := range listed {
		if row.DaemonID != nil && *row.DaemonID == oldDaemon {
			t.Fatal("retired legacy computer still visible")
		}
	}
	var newB string
	if err := testPool.QueryRow(ctx, `SELECT id FROM agent_runtime WHERE workspace_id=$1 AND owner_id=$2 AND daemon_id=$3`, ws2, testUserID, newDaemon).Scan(&newB); err != nil {
		t.Fatal(err)
	}
	if err := testHandler.mergeLegacyRuntime(ctx, parseUUID(newB), parseUUID(oldB), oldDaemon, "codex"); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM personal_runtime WHERE id=$1`, canonical).Scan(&count); err != nil || count != 0 {
		t.Fatal("drained legacy canonical was not removed")
	}
	a, err := testHandler.Queries.GetAgent(ctx, parseUUID(aid))
	if err != nil || uuidToString(a.RuntimeID) != newA {
		t.Fatal("agent binding not preserved")
	}
	task, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(tid))
	if err != nil || uuidToString(task.RuntimeID) != newA || task.Status != "completed" {
		t.Fatal("task history lost during identity merge")
	}
}

func TestPersonalRuntimeCrossWorkspace(t *testing.T) {
	if testHandler == nil {
		t.Fatal("isolated database required")
	}
	ctx := context.Background()
	ws2 := dbfx.Workspace(t, "Personal Runtime B", "personal-"+uuid.NewString())
	dbfx.Member(t, ws2, testUserID, "member")
	p := insertRuntimeProfileFixture(t, ctx, "personal-"+uuid.NewString(), "codex", "codex")
	rid := dbfx.Runtime(t, "My computer", testutil.Cols{"daemon_id": "personal-" + uuid.NewString(), "runtime_mode": "local", "profile_id": p, "provider": "codex"})
	if err := testHandler.ensurePersonalRuntimes(ctx, parseUUID(ws2), testUserID); err != nil {
		t.Fatal(err)
	}
	var p2, r2 string
	if err := testPool.QueryRow(ctx, `SELECT r.id,r.profile_id FROM agent_runtime r JOIN agent_runtime original ON original.personal_runtime_id=r.personal_runtime_id WHERE original.id=$1 AND r.workspace_id=$2`, rid, ws2).Scan(&r2, &p2); err != nil {
		t.Fatal(err)
	}
	if rid == r2 || p == p2 {
		t.Fatal("legacy per-workspace registration identity was lost")
	}
	request := withURLParams(newRequest("PATCH", "/", map[string]any{"command_name": "codex-shared", "fixed_args": []string{"--shared"}}), "id", testWorkspaceID, "profileId", p)
	w := httptest.NewRecorder()
	testHandler.UpdateRuntimeProfile(w, request)
	if w.Code != 200 {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	for _, id := range []string{p, p2} {
		row, err := testHandler.Queries.GetRuntimeProfile(ctx, parseUUID(id))
		if err != nil || row.CommandName != "codex-shared" || string(row.FixedArgs) != "[\"--shared\"]" {
			t.Fatalf("profile not synchronized: %s %s %v", row.CommandName, row.FixedArgs, err)
		}
	}
	var definitions int
	if err := testPool.QueryRow(ctx, `SELECT count(DISTINCT personal_profile_id) FROM runtime_profile WHERE id IN ($1,$2)`, p, p2).Scan(&definitions); err != nil || definitions != 1 {
		t.Fatalf("not one canonical config: %d %v", definitions, err)
	}
	w = httptest.NewRecorder()
	request = withURLParam(newRequest("PATCH", "/", map[string]any{"custom_name": "Shared personal computer"}), "runtimeId", r2)
	testHandler.UpdateAgentRuntime(w, request)
	if w.Code != 200 {
		t.Fatalf("rename: %d %s", w.Code, w.Body.String())
	}
	for _, id := range []string{rid, r2} {
		row, err := testHandler.Queries.GetAgentRuntime(ctx, parseUUID(id))
		if err != nil || row.CustomName.String != "Shared personal computer" {
			t.Fatalf("rename not global: %+v %v", row.CustomName, err)
		}
	}
	// Owner/admin of A must not see another member's personal runtime or profile.
	other := dbfx.User(t, "other", "other-"+uuid.NewString()+"@example.test")
	dbfx.Member(t, testWorkspaceID, other, "member")
	secret := dbfx.Runtime(t, "Other private computer", testutil.Cols{"owner_id": other, "daemon_id": "other-" + uuid.NewString(), "runtime_mode": "local"})
	w = httptest.NewRecorder()
	testHandler.ListAgentRuntimes(w, newRequest("GET", "/", nil))
	if w.Code != 200 {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	var listed []AgentRuntimeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	for _, row := range listed {
		if row.ID == secret || row.OwnerID == nil || *row.OwnerID != testUserID {
			t.Fatal("another owner's machine leaked")
		}
	}
	w = httptest.NewRecorder()
	testHandler.UpdateAgentRuntime(w, withURLParam(newRequest("PATCH", "/", map[string]any{"custom_name": "stolen"}), "runtimeId", secret))
	if w.Code != 403 && w.Code != 404 {
		t.Fatalf("admin could edit another machine: %d", w.Code)
	}
	w = httptest.NewRecorder()
	testHandler.UpdateAgentRuntime(w, withURLParam(newRequest("PATCH", "/", map[string]any{"visibility": "public"}), "runtimeId", rid))
	if w.Code != 400 {
		t.Fatalf("runtime sharing still enabled: %d", w.Code)
	}
	// Membership loss removes access to the workspace, never the personal config.
	if _, err := testPool.Exec(ctx, `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, ws2, testUserID); err != nil {
		t.Fatal(err)
	}
	if err := testHandler.ensurePersonalRuntimes(ctx, parseUUID(ws2), testUserID); err == nil {
		t.Fatal("projected into unauthorized workspace")
	}
	var n int
	testPool.QueryRow(ctx, `SELECT count(*) FROM personal_runtime_profile WHERE id=(SELECT personal_profile_id FROM runtime_profile WHERE id=$1)`, p).Scan(&n)
	if n != 1 {
		t.Fatal("leaving workspace deleted personal config")
	}
}

func TestPersonalRuntimeGlobalDeletionGuards(t *testing.T) {
	ctx := context.Background()
	ws2 := dbfx.Workspace(t, "Delete B", "delete-"+uuid.NewString())
	dbfx.Member(t, ws2, testUserID, "member")
	p := insertRuntimeProfileFixture(t, ctx, "delete-"+uuid.NewString(), "codex", "codex")
	rid := dbfx.Runtime(t, "Delete machine", testutil.Cols{"daemon_id": "delete-" + uuid.NewString(), "runtime_mode": "local", "profile_id": p, "provider": "codex", "status": "offline"})
	if err := testHandler.ensurePersonalRuntimes(ctx, parseUUID(ws2), testUserID); err != nil {
		t.Fatal(err)
	}
	var r2 string
	if err := testPool.QueryRow(ctx, `SELECT r.id FROM agent_runtime r JOIN agent_runtime o ON o.personal_runtime_id=r.personal_runtime_id WHERE o.id=$1 AND r.workspace_id=$2`, rid, ws2).Scan(&r2); err != nil {
		t.Fatal(err)
	}
	aid := dbfx.Agent(t, "Bound in B", r2, testutil.Cols{"workspace_id": ws2})
	w := httptest.NewRecorder()
	testHandler.DeleteRuntimeProfile(w, withURLParams(newRequest("DELETE", "/", nil), "id", testWorkspaceID, "profileId", p))
	if w.Code != 409 {
		t.Fatalf("ignored B dependency: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	testHandler.GetPersonalMachineDeletePlan(w, withURLParam(newRequest("GET", "/", nil), "runtimeId", rid))
	var plan struct {
		IDs    []string `json:"ids"`
		Agents []struct {
			ID string `json:"id"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.IDs) != 2 || len(plan.Agents) != 1 || plan.Agents[0].ID != aid {
		t.Fatalf("incomplete global plan: %s", w.Body.String())
	}
	// A stale A-only confirmation cannot delete B's registrations.
	w = httptest.NewRecorder()
	testHandler.DeleteOfflineMachine(w, withURLParam(newRequest("POST", "/", map[string]any{"expected_runtime_ids": []string{rid}, "expected_active_agent_ids": []string{aid}}), "runtimeId", rid))
	if w.Code != 409 {
		t.Fatalf("stale plan accepted: %d", w.Code)
	}
	w = httptest.NewRecorder()
	testHandler.DeleteOfflineMachine(w, withURLParam(newRequest("POST", "/", map[string]any{"expected_runtime_ids": plan.IDs, "expected_active_agent_ids": []string{aid}}), "runtimeId", rid))
	if w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	a, err := testHandler.Queries.GetAgent(ctx, parseUUID(aid))
	if err != nil || a.RuntimeID.Valid || a.ArchivedAt.Valid {
		t.Fatal("agent/history not preserved unbound")
	}
	if err := testHandler.ensurePersonalRuntimes(ctx, parseUUID(ws2), testUserID); err != nil {
		t.Fatal(err)
	}
	var n int
	testPool.QueryRow(ctx, `SELECT count(*) FROM agent_runtime WHERE id IN ($1,$2)`, rid, r2).Scan(&n)
	if n != 0 {
		t.Fatal("deleted machine came back from workspace projection")
	}
}

func TestPersonalRuntimeAgentSharingBoundary(t *testing.T) {
	owner := parseUUID("11111111-1111-1111-1111-111111111111")
	other := parseUUID("22222222-2222-2222-2222-222222222222")
	rt := db.AgentRuntime{OwnerID: owner, Visibility: "public"}
	for _, role := range []string{"member", "admin", "owner"} {
		m := db.Member{UserID: other, Role: role}
		if canUseRuntimeForAgent(m, rt) || canEditRuntime(m, rt) {
			t.Fatal("workspace role bypasses personal ownership")
		}
		if canViewAgentSecrets(db.Agent{OwnerID: owner}, uuidToString(other), role) {
			t.Fatal("shared agent exposes owner secrets")
		}
	}
	if !canUseRuntimeForAgent(db.Member{UserID: owner}, rt) {
		t.Fatal("runtime owner lost access")
	}
}
