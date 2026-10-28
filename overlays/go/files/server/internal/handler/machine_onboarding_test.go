package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func machineWorkspaceCall(h *Handler, userID, name string) *httptest.ResponseRecorder {
	r := testutil.WithHeaders(testutil.JSONRequest("POST", "/api/tmesh/workspaces/ensure", map[string]string{"name": name}), "X-User-ID", userID)
	w := httptest.NewRecorder()
	h.EnsureMachineWorkspace(w, r)
	return w
}

func TestMachineWorkspaceEnsureCreatesAndReuses(t *testing.T) {
	name := machineTestName()
	h := machineTestConfig(t, name)
	user := dbfx.User(t, "Fixture", name+"@example.test")
	first := machineWorkspaceCall(h, user, name)
	second := machineWorkspaceCall(h, user, name)
	var a, b map[string]any
	if first.Code != 200 || second.Code != 200 || json.Unmarshal(first.Body.Bytes(), &a) != nil || json.Unmarshal(second.Body.Bytes(), &b) != nil {
		t.Fatalf("ensure failed: %d %d", first.Code, second.Code)
	}
	if a["workspace_created"] != true || b["workspace_created"] != false || a["workspace_id"] != b["workspace_id"] {
		t.Fatalf("ensure did not reuse workspace: %v %v", a, b)
	}
	var role string
	if err := testPool.QueryRow(context.Background(), "SELECT role FROM member WHERE user_id=$1 AND workspace_id=$2", user, a["workspace_id"]).Scan(&role); err != nil || role != "owner" {
		t.Fatal("new workspace did not have the user as owner")
	}
}

func TestMachineWorkspaceEnsureReusesMemberAndRejectsAmbiguity(t *testing.T) {
	name := machineTestName()
	h := machineTestConfig(t, name)
	user := dbfx.User(t, "Fixture", name+"@example.test")
	first := dbfx.Workspace(t, name, "first-"+uuid.NewString())
	dbfx.Member(t, first, user, "member")
	response := machineWorkspaceCall(h, user, name)
	var got map[string]any
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &got) != nil || got["workspace_id"] != first || got["workspace_created"] != false {
		t.Fatalf("member workspace not reused: %d %s", response.Code, response.Body.String())
	}
	second := dbfx.Workspace(t, name, "second-"+uuid.NewString())
	dbfx.Member(t, second, user, "admin")
	response = machineWorkspaceCall(h, user, name)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), first) || !strings.Contains(response.Body.String(), second) {
		t.Fatalf("ambiguous workspaces were not reported: %d %s", response.Code, response.Body.String())
	}
}

func TestMachineWorkspaceEnsureConcurrentCallsCreateOnlyOne(t *testing.T) {
	name := machineTestName()
	h := machineTestConfig(t, name)
	user := dbfx.User(t, "Fixture", name+"@example.test")
	results := make(chan *httptest.ResponseRecorder, 6)
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- machineWorkspaceCall(h, user, name) }()
	}
	wg.Wait()
	close(results)
	var workspaceID string
	created := 0
	for response := range results {
		var got map[string]any
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &got) != nil {
			t.Fatalf("concurrent ensure failed: %d %s", response.Code, response.Body.String())
		}
		id, _ := got["workspace_id"].(string)
		if workspaceID != "" && workspaceID != id {
			t.Fatal("concurrent calls created different workspaces")
		}
		workspaceID = id
		if got["workspace_created"] == true {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("expected one creation, got %d", created)
	}
}

func TestMachineWorkspaceEnsureRespectsDisabledCreation(t *testing.T) {
	name := machineTestName()
	h := machineTestConfig(t, name)
	user := dbfx.User(t, "Fixture", name+"@example.test")
	h.cfg.DisableWorkspaceCreation = true
	if response := machineWorkspaceCall(h, user, name); response.Code != http.StatusForbidden {
		t.Fatalf("disabled creation status %d", response.Code)
	}
}

func TestMachineWorkspaceSlugSupportsDisplayNames(t *testing.T) {
	for name, want := range map[string]string{"zhang_san": "zhang-san", "研发团队": "workspace", "Hello Team": "hello-team"} {
		if got := machineWorkspaceSlug(name); got != want {
			t.Fatalf("slug(%q)=%q, want %q", name, got, want)
		}
	}
}

func TestMachineAgentLockRejectsConcurrentAndReleasesOnDisconnect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(testHandler.MachineAgentLock))
	defer server.Close()
	request := func() *http.Response {
		body, _ := json.Marshal(map[string]string{"workspace_id": testWorkspaceID, "agent_name": "Fixture agent"})
		r, err := http.NewRequest("POST", server.URL, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("X-User-ID", testUserID)
		response, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	first := request()
	if first.StatusCode != 200 {
		t.Fatalf("first lock: %d", first.StatusCode)
	}
	second := request()
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("concurrent lock: %d", second.StatusCode)
	}
	second.Body.Close()
	first.Body.Close()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		response := request()
		if response.StatusCode == 200 {
			response.Body.Close()
			return
		}
		response.Body.Close()
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal(fmt.Errorf("lock was not released after disconnect"))
}

func TestMachineAgentLockRejectsAnotherOwnersPrivateAgent(t *testing.T) {
	actor := dbfx.User(t, "Fixture member", "fixture-member-"+uuid.NewString()+"@example.test")
	dbfx.Member(t, testWorkspaceID, actor, "member")
	dbfx.Agent(t, "Private fixture", "", testutil.Cols{"owner_id": testUserID, "visibility": "private"})
	req := testutil.WithHeaders(testutil.JSONRequest("POST", "/api/tmesh/agents/lock", map[string]string{
		"workspace_id": testWorkspaceID, "agent_name": "Private fixture",
	}), "X-User-ID", actor)
	response := testutil.Call(t, testHandler.MachineAgentLock, req).ResponseRecorder
	if response.Code != http.StatusConflict || strings.Contains(response.Body.String(), testUserID) {
		t.Fatalf("private conflict not safely rejected: %d %s", response.Code, response.Body.String())
	}
}
