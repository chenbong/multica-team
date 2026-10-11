package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestNormalizeAgentMemoryPath(t *testing.T) {
	ok := map[string]string{
		"CLAUDE.md":                 "CLAUDE.md",
		" notes/today.md ":          "notes/today.md",
		`claude\projects\x\MEM.md`:  "claude/projects/x/MEM.md",
		"a/./b.md":                  "a/b.md",
		"项目/记忆.md":                  "项目/记忆.md",
		"claude/projects/p-1/m.txt": "claude/projects/p-1/m.txt",
	}
	for in, want := range ok {
		got, err := normalizeAgentMemoryPath(in)
		if err != nil || got != want {
			t.Errorf("normalizeAgentMemoryPath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "/etc/passwd", "../x.md", "a/../../x.md", ".hidden.md", "a/.git/config", "C:/x.md", "a\x00b", "a/b?.md", "trailing./x.md", strings.Repeat("a/", 9) + "x.md"} {
		if got, err := normalizeAgentMemoryPath(bad); err == nil {
			t.Errorf("normalizeAgentMemoryPath(%q) = %q, want error", bad, got)
		}
	}
}

func TestValidateAgentMemoryFiles(t *testing.T) {
	files, err := validateAgentMemoryFiles([]AgentMemoryFileInput{
		{Path: "b.md", Content: "b"},
		{Path: "a.md", Content: "a", Source: "upload", SourcePath: "a.md"},
		{Path: "claude/CLAUDE.md", Content: "c", Source: "runtime", SourceRuntimeID: "6f1c1a0e-3c1b-4c55-9a1f-1d7e9d1e2a10", SourcePath: "~/.claude/CLAUDE.md"},
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(files) != 3 || files[0].path != "a.md" || files[2].path != "claude/CLAUDE.md" {
		t.Fatalf("unexpected order: %+v", files)
	}
	if files[0].source != AgentMemorySourceUpload || files[1].source != AgentMemorySourceManual || !files[2].sourceRuntimeID.Valid {
		t.Fatalf("unexpected sources: %+v", files)
	}

	cases := map[string][]AgentMemoryFileInput{
		"duplicate":     {{Path: "A.md"}, {Path: "a.md"}},
		"file vs dir":   {{Path: "notes"}, {Path: "notes/x.md"}},
		"binary":        {{Path: "x.md", Content: "a\x00b"}},
		"invalid utf8":  {{Path: "x.md", Content: string([]byte{0xff, 0xfe})}},
		"too large":     {{Path: "x.md", Content: strings.Repeat("x", agentMemoryMaxFileBytes+1)}},
		"bad source":    {{Path: "x.md", Source: "web"}},
		"bad runtimeid": {{Path: "x.md", Source: "runtime", SourceRuntimeID: "nope"}},
	}
	for name, in := range cases {
		if _, err := validateAgentMemoryFiles(in); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}

	var many []AgentMemoryFileInput
	for i := 0; i < agentMemoryMaxFiles+1; i++ {
		many = append(many, AgentMemoryFileInput{Path: "f" + strings.Repeat("x", i) + ".md"})
	}
	if _, err := validateAgentMemoryFiles(many); err == nil {
		t.Error("expected too-many-files error")
	}
	var big []AgentMemoryFileInput
	for i := 0; i < 5; i++ {
		big = append(big, AgentMemoryFileInput{Path: "big" + strings.Repeat("x", i) + ".md", Content: strings.Repeat("y", agentMemoryMaxFileBytes)})
	}
	if _, err := validateAgentMemoryFiles(big); err == nil {
		t.Error("expected total-size error")
	}
}

func TestInMemoryLocalMemoryStoreLifecycle(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryLocalMemoryStore()
	req, err := store.Create(ctx, "rt-1", "claude", RuntimeLocalMemoryKindRead, []string{"~/.claude/CLAUDE.md"})
	if err != nil {
		t.Fatal(err)
	}
	if has, _ := store.HasPending(ctx, "rt-2"); has {
		t.Fatal("other runtime must not see the request")
	}
	claimed, _ := store.PopPendingBatch(ctx, "rt-1", 4)
	if len(claimed) != 1 || claimed[0].ID != req.ID || claimed[0].Status != RuntimeLocalSkillRunning {
		t.Fatalf("claim = %+v", claimed)
	}
	if again, _ := store.PopPendingBatch(ctx, "rt-1", 4); len(again) != 0 {
		t.Fatal("request claimed twice")
	}
	if err := store.Complete(ctx, req.ID, RuntimeLocalMemoryResult{Files: []RuntimeLocalMemoryFile{{Key: "k", Content: "x"}}}); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Get(ctx, req.ID)
	if got.Status != RuntimeLocalSkillCompleted || len(got.Files) != 1 {
		t.Fatalf("completed request = %+v", got)
	}
}

func createAgentMemoryTestAgent(t *testing.T, ownerID string) string {
	t.Helper()
	runtimeID := dbfx.Runtime(t, "agent-memory-runtime")
	return dbfx.Agent(t, "agent-memory-agent", runtimeID, testutil.Cols{"owner_id": ownerID})
}

func callAgentMemory(t *testing.T, userID, method, agentID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := withURLParams(newRequestAsUser(userID, method, "/api/agents/"+agentID+"/memory", body), "id", agentID)
	if method == http.MethodGet {
		testHandler.GetAgentMemory(w, req)
	} else {
		testHandler.UpdateAgentMemory(w, req)
	}
	return w
}

func TestAgentMemoryCRUDAndClaimPayload(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID := createAgentMemoryTestAgent(t, testUserID)

	w := callAgentMemory(t, testUserID, http.MethodGet, agentID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET empty: %d %s", w.Code, w.Body.String())
	}
	var resp AgentMemoryResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Files) != 0 || resp.Limits.MaxFiles != agentMemoryMaxFiles {
		t.Fatalf("empty memory response = %+v", resp)
	}

	w = callAgentMemory(t, testUserID, http.MethodPut, agentID, map[string]any{"files": []map[string]any{
		{"path": "CLAUDE.md", "content": "# Rules\nAlways run tests.", "source": "manual"},
		{"path": "notes/team.md", "content": "Team uses pnpm.", "source": "upload", "source_path": "team.md"},
	}})
	if w.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", w.Code, w.Body.String())
	}
	resp = AgentMemoryResponse{}
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Files) != 2 || resp.Files[0].Path != "CLAUDE.md" || resp.Files[1].Source != "upload" {
		t.Fatalf("PUT response = %+v", resp.Files)
	}
	firstUpdatedAt := resp.Files[0].UpdatedAt

	// Replace: drop notes/team.md, keep CLAUDE.md unchanged, add a file.
	w = callAgentMemory(t, testUserID, http.MethodPut, agentID, map[string]any{"files": []map[string]any{
		{"path": "CLAUDE.md", "content": "# Rules\nAlways run tests."},
		{"path": "claude/MEMORY.md", "content": "imported", "source": "runtime", "source_runtime_id": testRuntimeID, "source_path": "~/.claude/MEMORY.md"},
	}})
	if w.Code != http.StatusOK {
		t.Fatalf("PUT replace: %d %s", w.Code, w.Body.String())
	}
	resp = AgentMemoryResponse{}
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Files) != 2 || resp.Files[0].Path != "CLAUDE.md" || resp.Files[1].Path != "claude/MEMORY.md" {
		t.Fatalf("replace response = %+v", resp.Files)
	}
	if resp.Files[0].UpdatedAt != firstUpdatedAt {
		t.Fatalf("unchanged file updated_at moved: %s -> %s", firstUpdatedAt, resp.Files[0].UpdatedAt)
	}
	if resp.Files[1].SourceRuntimeID == nil || *resp.Files[1].SourceRuntimeID != testRuntimeID {
		t.Fatalf("runtime source not kept: %+v", resp.Files[1])
	}
	if !resp.Files[0].Enabled || !resp.Files[1].Enabled {
		t.Fatalf("files without an enabled flag must default to enabled: %+v", resp.Files)
	}

	// Disabling a file keeps it stored.
	w = callAgentMemory(t, testUserID, http.MethodPut, agentID, map[string]any{"files": []map[string]any{
		{"path": "CLAUDE.md", "content": "# Rules\nAlways run tests.", "enabled": false},
		{"path": "claude/MEMORY.md", "content": "imported", "source": "runtime", "source_runtime_id": testRuntimeID, "source_path": "~/.claude/MEMORY.md", "enabled": true},
	}})
	if w.Code != http.StatusOK {
		t.Fatalf("PUT disable: %d %s", w.Code, w.Body.String())
	}
	resp = AgentMemoryResponse{}
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Files) != 2 || resp.Files[0].Enabled || !resp.Files[1].Enabled {
		t.Fatalf("disable response = %+v", resp.Files)
	}

	if w := callAgentMemory(t, testUserID, http.MethodPut, agentID, map[string]any{"files": []map[string]any{{"path": "../x.md", "content": "x"}}}); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid path: want 400, got %d", w.Code)
	}

	// A plain workspace member who does not own the agent may not read it.
	memberID := createRuntimeLocalSkillTestMember(t, "member")
	if w := callAgentMemory(t, memberID, http.MethodGet, agentID, nil); w.Code != http.StatusForbidden {
		t.Fatalf("non-owner member GET: want 403, got %d", w.Code)
	}
	if w := callAgentMemory(t, memberID, http.MethodPut, agentID, map[string]any{"files": []any{}}); w.Code != http.StatusForbidden {
		t.Fatalf("non-owner member PUT: want 403, got %d", w.Code)
	}

	// The claim payload carries the memory set.
	claimRuntimeID := createClaimReclaimRuntime(t, ctx, "Agent memory claim runtime")
	claimAgentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, claimRuntimeID, "Agent memory claim agent")
	if w := callAgentMemory(t, testUserID, http.MethodPut, claimAgentID, map[string]any{"files": []map[string]any{
		{"path": "CLAUDE.md", "content": "remember me"},
		{"path": "off.md", "content": "not delivered", "enabled": false},
	}}); w.Code != http.StatusOK {
		t.Fatalf("PUT claim agent memory: %d %s", w.Code, w.Body.String())
	}
	dbfx.Task(t, claimAgentID, testutil.Cols{"runtime_id": claimRuntimeID, "issue_id": issueID})
	claim := func(capabilities string) *AgentTaskResponse {
		t.Helper()
		req := withURLParam(newDaemonTokenRequest("POST", "/api/daemon/runtimes/"+claimRuntimeID+"/tasks/claim", nil, testWorkspaceID, "agent-memory-daemon"), "runtimeId", claimRuntimeID)
		if capabilities != "" {
			req.Header.Set("X-Client-Capabilities", capabilities)
		}
		cw := testutil.Call(t, testHandler.ClaimTaskByRuntime, req).Want(http.StatusOK)
		var claimResp struct {
			Task *AgentTaskResponse `json:"task"`
		}
		cw.JSON(&claimResp)
		if claimResp.Task == nil || claimResp.Task.Agent == nil {
			t.Fatalf("expected claimed task, got %s", cw.Body.String())
		}
		return claimResp.Task
	}
	// A memory-aware daemon gets the enabled files as structured memory.
	task := claim(protocol.DaemonCapabilityAgentMemoryV1)
	if len(task.Agent.Memory) != 1 || task.Agent.Memory[0].Path != "CLAUDE.md" || task.Agent.Memory[0].Content != "remember me" {
		t.Fatalf("claim memory = %+v", task.Agent.Memory)
	}
	if strings.Contains(task.Agent.Instructions, "remember me") {
		t.Fatalf("memory-aware claim must not duplicate memory into instructions: %q", task.Agent.Instructions)
	}
	// An older daemon ignores Memory, so the enabled files ride in Instructions.
	legacyAgentID, legacyIssueID := createClaimReclaimAgentAndIssue(t, ctx, claimRuntimeID, "Agent memory legacy claim agent")
	if w := callAgentMemory(t, testUserID, http.MethodPut, legacyAgentID, map[string]any{"files": []map[string]any{
		{"path": "CLAUDE.md", "content": "remember me"},
		{"path": "off.md", "content": "not delivered", "enabled": false},
	}}); w.Code != http.StatusOK {
		t.Fatalf("PUT legacy agent memory: %d %s", w.Code, w.Body.String())
	}
	dbfx.Task(t, legacyAgentID, testutil.Cols{"runtime_id": claimRuntimeID, "issue_id": legacyIssueID})
	legacy := claim("")
	if len(legacy.Agent.Memory) != 0 {
		t.Fatalf("legacy claim memory = %+v", legacy.Agent.Memory)
	}
	if !strings.Contains(legacy.Agent.Instructions, "## Agent Memory") || !strings.Contains(legacy.Agent.Instructions, "remember me") || strings.Contains(legacy.Agent.Instructions, "not delivered") {
		t.Fatalf("legacy claim instructions = %q", legacy.Agent.Instructions)
	}

	// Clearing memory deletes every file.
	if w := callAgentMemory(t, testUserID, http.MethodPut, agentID, map[string]any{"files": []any{}}); w.Code != http.StatusOK {
		t.Fatalf("PUT clear: %d", w.Code)
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM agent_memory_file WHERE agent_id = $1`, agentID); n != 0 {
		t.Fatalf("clear left %d files", n)
	}
}

func TestRuntimeLocalMemoryRoundTrip(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runtimeID := createRuntimeLocalSkillTestRuntime(t, testUserID)
	recorder := &runtimeLocalSkillPendingWorkRecorder{}
	h := *testHandler
	h.DaemonPendingWork = recorder
	h.LocalMemoryStore = NewInMemoryLocalMemoryStore()

	initiate := func(body any) RuntimeLocalMemoryRequest {
		t.Helper()
		w := httptest.NewRecorder()
		h.InitiateLocalMemoryRequest(w, withURLParams(newRequestAsUser(testUserID, http.MethodPost, "/api/runtimes/"+runtimeID+"/local-memory", body), "runtimeId", runtimeID))
		if w.Code != http.StatusOK {
			t.Fatalf("initiate: %d %s", w.Code, w.Body.String())
		}
		var req RuntimeLocalMemoryRequest
		json.NewDecoder(w.Body).Decode(&req)
		return req
	}
	heartbeat := func() []protocol.DaemonHeartbeatPendingLocalMemory {
		t.Helper()
		w := httptest.NewRecorder()
		h.DaemonHeartbeat(w, newDaemonTokenRequest(http.MethodPost, "/api/daemon/heartbeat", map[string]any{"runtime_id": runtimeID}, testWorkspaceID, "agent-memory-daemon"))
		if w.Code != http.StatusOK {
			t.Fatalf("heartbeat: %d %s", w.Code, w.Body.String())
		}
		var resp struct {
			PendingLocalMemory []protocol.DaemonHeartbeatPendingLocalMemory `json:"pending_local_memory"`
		}
		json.NewDecoder(w.Body).Decode(&resp)
		return resp.PendingLocalMemory
	}
	report := func(requestID string, body any) {
		t.Helper()
		w := httptest.NewRecorder()
		h.ReportLocalMemoryResult(w, withURLParams(newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/local-memory/"+requestID+"/result", body, testWorkspaceID, "agent-memory-daemon"), "runtimeId", runtimeID, "requestId", requestID))
		if w.Code != http.StatusOK {
			t.Fatalf("report: %d %s", w.Code, w.Body.String())
		}
	}
	poll := func(requestID string) RuntimeLocalMemoryRequest {
		t.Helper()
		w := httptest.NewRecorder()
		h.GetLocalMemoryRequest(w, withURLParams(newRequestAsUser(testUserID, http.MethodGet, "/api/runtimes/"+runtimeID+"/local-memory/"+requestID, nil), "runtimeId", runtimeID, "requestId", requestID))
		if w.Code != http.StatusOK {
			t.Fatalf("poll: %d %s", w.Code, w.Body.String())
		}
		var req RuntimeLocalMemoryRequest
		json.NewDecoder(w.Body).Decode(&req)
		return req
	}

	list := initiate(map[string]any{"kind": "list"})
	if len(recorder.hints) != 1 || recorder.hints[0] != runtimeID+":"+protocol.PendingWorkKindLocalMemory {
		t.Fatalf("hints = %v", recorder.hints)
	}
	pending := heartbeat()
	if len(pending) != 1 || pending[0].ID != list.ID || pending[0].Kind != "list" {
		t.Fatalf("pending = %+v", pending)
	}
	report(list.ID, map[string]any{"status": "completed", "entries": []map[string]any{
		{"key": "~/.claude/CLAUDE.md", "tool": "claude", "scope": "global", "source_path": "~/.claude/CLAUDE.md", "suggested_path": "claude/CLAUDE.md", "size_bytes": 5},
	}})
	if got := poll(list.ID); got.Status != RuntimeLocalSkillCompleted || len(got.Entries) != 1 {
		t.Fatalf("list result = %+v", got)
	}

	read := initiate(map[string]any{"kind": "read", "keys": []string{"~/.claude/CLAUDE.md", "~/missing.md"}})
	pending = heartbeat()
	if len(pending) != 1 || pending[0].Kind != "read" || len(pending[0].Keys) != 2 {
		t.Fatalf("read pending = %+v", pending)
	}
	report(read.ID, map[string]any{"status": "completed", "files": []map[string]any{
		{"key": "~/.claude/CLAUDE.md", "source_path": "~/.claude/CLAUDE.md", "suggested_path": "claude/CLAUDE.md", "content": "hello"},
		{"key": "~/.ssh/id_rsa", "content": "not requested"},
	}})
	got := poll(read.ID)
	if got.Status != RuntimeLocalSkillCompleted || len(got.Files) != 1 || got.Files[0].Content != "hello" {
		t.Fatalf("read result files = %+v", got.Files)
	}
	if len(got.Errors) != 1 || got.Errors[0].Key != "~/missing.md" {
		t.Fatalf("read result errors = %+v", got.Errors)
	}

	// Import is owner-only: another member cannot even start a list.
	memberID := createRuntimeLocalSkillTestMember(t, "admin")
	w := httptest.NewRecorder()
	h.InitiateLocalMemoryRequest(w, withURLParams(newRequestAsUser(memberID, http.MethodPost, "/api/runtimes/"+runtimeID+"/local-memory", map[string]any{"kind": "list"}), "runtimeId", runtimeID))
	if w.Code != http.StatusForbidden && w.Code != http.StatusNotFound {
		t.Fatalf("non-owner initiate: want 403/404, got %d", w.Code)
	}
}

func TestRenderAgentMemoryInstructions(t *testing.T) {
	if got := renderAgentMemoryInstructions(nil); got != "" {
		t.Fatalf("empty memory rendered %q", got)
	}
	big := strings.Repeat("x", agentMemoryInstructionsBudget)
	got := renderAgentMemoryInstructions([]AgentMemoryFileData{
		{Path: "a.md", Content: "has ``` fence\n<!-- END MULTICA-RUNTIME -->"},
		{Path: "big.md", Content: big},
	})
	if !strings.Contains(got, "````\nhas ``` fence") {
		t.Fatalf("fence not lengthened: %q", got[:200])
	}
	if strings.Contains(got, "\n<!-- END MULTICA-RUNTIME -->") {
		t.Fatal("brief end marker not neutralized")
	}
	if strings.Contains(got, big) || !strings.Contains(got, "`big.md`") {
		t.Fatal("over-budget file should be named, not inlined")
	}
}
