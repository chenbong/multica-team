package handler

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type machineNameFixture struct{ owner, other, ws, daemon, a, sibling, b, different string }

func newMachineNameFixture(t *testing.T) machineNameFixture {
	t.Helper()
	if testHandler == nil {
		t.Fatal("isolated database required")
	}
	f := machineNameFixture{daemon: "personal-" + uuid.NewString()}
	f.owner = dbfx.User(t, "Owner", uuid.NewString()+"@example.test")
	f.other = dbfx.User(t, "Other", uuid.NewString()+"@example.test")
	f.ws = dbfx.Workspace(t, "Other workspace", "personal-"+uuid.NewString())
	dbfx.Member(t, testWorkspaceID, f.owner, "member")
	dbfx.Member(t, f.ws, f.owner, "member")
	dbfx.Member(t, testWorkspaceID, f.other, "member")
	f.a = f.runtime(t, testWorkspaceID, f.daemon, "claude")
	f.sibling = f.runtime(t, testWorkspaceID, f.daemon, "codex")
	f.b = f.runtime(t, f.ws, f.daemon, "claude")
	f.different = f.runtime(t, testWorkspaceID, f.daemon+"-other", "claude")
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM user_machine_name WHERE machine_key LIKE $1", "daemon:local:"+f.daemon+"%")
	})
	return f
}
func (f machineNameFixture) runtime(t *testing.T, ws, daemon, provider string) string {
	return dbfx.Runtime(t, "Original hostname", testutil.Cols{"workspace_id": ws, "owner_id": f.owner, "daemon_id": daemon, "runtime_mode": "local", "provider": provider, "visibility": "private"})
}
func machineNamePatch(h *Handler, actor, id string, body map[string]any) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.UpdateAgentRuntime(w, withURLParam(newRequestAs(actor, http.MethodPatch, "/api/runtimes/"+id, body), "runtimeId", id))
	return w
}
func personalView(t *testing.T, actor, id string) AgentRuntimeResponse {
	t.Helper()
	rt, err := testHandler.Queries.GetAgentRuntime(context.Background(), parseUUID(id))
	if err != nil {
		t.Fatal(err)
	}
	result, err := testHandler.personalRuntimeResponses(context.Background(), parseUUID(actor), []db.AgentRuntime{rt})
	if err != nil {
		t.Fatal(err)
	}
	return result[0]
}
func assertPersonalName(t *testing.T, actor, id, want string) {
	t.Helper()
	r := personalView(t, actor, id)
	if want == "" {
		if r.PersonalName != nil {
			t.Fatalf("unexpected note: %v", r.PersonalName)
		}
		return
	}
	if r.PersonalName == nil || *r.PersonalName != want || r.CustomName == nil || *r.CustomName != want {
		t.Fatalf("wrong personal name: %+v", r)
	}
}
func TestMachineNamePersonalAcrossWorkspaces(t *testing.T) {
	f := newMachineNameFixture(t)
	for _, id := range []string{f.a, f.sibling, f.b} {
		if _, err := testPool.Exec(context.Background(), "UPDATE agent_runtime SET custom_name='Shared default' WHERE id=$1", id); err != nil {
			t.Fatal(err)
		}
	}
	h := *testHandler
	h.Bus = events.New()
	var seen []events.Event
	h.Bus.SubscribeAll(func(e events.Event) { seen = append(seen, e) })
	w := machineNamePatch(&h, f.owner, f.a, map[string]any{"custom_name": "  我的机器  ", "apply_to_machine": true, "user_id": f.other})
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	for _, id := range []string{f.a, f.sibling, f.b} {
		assertPersonalName(t, f.owner, id, "我的机器")
		assertPersonalName(t, f.other, id, "")
		rt, err := testHandler.Queries.GetAgentRuntime(context.Background(), parseUUID(id))
		if err != nil {
			t.Fatal(err)
		}
		raw := runtimeToResponse(rt)
		if raw.PersonalName != nil || raw.CustomName == nil || *raw.CustomName != "Shared default" || raw.Name != "Original hostname" {
			t.Fatal("shared or daemon metadata changed")
		}
	}
	assertPersonalName(t, f.owner, f.different, "")
	if len(seen) != 1 || seen[0].Type != personalMachineNameEvent || seen[0].ActorID != f.owner || seen[0].WorkspaceID != "" {
		t.Fatalf("wrong event routing: %+v", seen)
	}
	payload, _ := json.Marshal(seen[0].Payload)
	if string(payload) != `{"action":"update"}` {
		t.Fatalf("private data leaked: %s", payload)
	}
	late := f.runtime(t, f.ws, f.daemon, "new-provider")
	assertPersonalName(t, f.owner, late, "我的机器")
	w = machineNamePatch(&h, f.owner, f.b, map[string]any{"custom_name": " "})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for _, id := range []string{f.a, f.sibling, f.b, late} {
		assertPersonalName(t, f.owner, id, "")
	}
	if r := personalView(t, f.owner, f.a); r.CustomName == nil || *r.CustomName != "Shared default" {
		t.Fatal("default not restored")
	}
}
func TestMachineNamePermissionsAndAtomicity(t *testing.T) {
	f := newMachineNameFixture(t)
	if w := machineNamePatch(testHandler, f.other, f.a, map[string]any{"custom_name": "hidden"}); w.Code != 403 {
		t.Fatalf("private: %d", w.Code)
	}
	if w := machineNamePatch(testHandler, testUserID, f.a, map[string]any{"custom_name": "Admin note"}); w.Code != 200 {
		t.Fatalf("admin: %d %s", w.Code, w.Body.String())
	}
	assertPersonalName(t, testUserID, f.a, "Admin note")
	assertPersonalName(t, f.owner, f.a, "")
	if w := machineNamePatch(testHandler, testUserID, f.a, map[string]any{"custom_name": "Must rollback", "visibility": "public"}); w.Code != 403 {
		t.Fatalf("visibility: %d", w.Code)
	}
	assertPersonalName(t, testUserID, f.a, "Admin note")
	if _, err := testPool.Exec(context.Background(), "UPDATE agent_runtime SET visibility='public' WHERE id=$1", f.a); err != nil {
		t.Fatal(err)
	}
	if w := machineNamePatch(testHandler, f.other, f.a, map[string]any{"custom_name": "Viewer note", "apply_to_machine": false}); w.Code != 200 {
		t.Fatalf("public viewer: %d %s", w.Code, w.Body.String())
	}
	assertPersonalName(t, f.other, f.a, "Viewer note")
	assertPersonalName(t, f.owner, f.a, "")
	outsider := dbfx.User(t, "Outsider", uuid.NewString()+"@example.test")
	if w := machineNamePatch(testHandler, outsider, f.a, map[string]any{"custom_name": "Denied"}); w.Code != 404 {
		t.Fatalf("outsider: %d", w.Code)
	}
	if w := machineNamePatch(testHandler, f.owner, f.a, map[string]any{"custom_name": strings.Repeat("中", 101)}); w.Code != 400 {
		t.Fatalf("length: %d", w.Code)
	}
}
func TestMachineNameConcurrentPrivateUpdates(t *testing.T) {
	f := newMachineNameFixture(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := "Remark"
			if i%2 == 0 {
				name = ""
			}
			w := machineNamePatch(testHandler, f.owner, f.a, map[string]any{"custom_name": name})
			if w.Code != 200 {
				t.Errorf("%d %s", w.Code, w.Body.String())
			}
		}(i)
	}
	wg.Wait()
	var count int
	if err := testPool.QueryRow(context.Background(), "SELECT count(*) FROM user_machine_name WHERE user_id=$1 AND machine_key=$2", f.owner, "daemon:local:"+f.daemon).Scan(&count); err != nil || count > 1 {
		t.Fatalf("duplicates: %d %v", count, err)
	}
	assertPersonalName(t, f.other, f.a, "")
}
