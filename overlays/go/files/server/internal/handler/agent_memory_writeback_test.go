package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func reportAgentMemoryChanges(t *testing.T, taskID string, changes []DaemonAgentMemoryChange) DaemonAgentMemoryChangesResponse {
	t.Helper()
	w := httptest.NewRecorder()
	req := withURLParams(newDaemonTokenRequest(http.MethodPost, "/api/daemon/tasks/"+taskID+"/memory-changes",
		DaemonAgentMemoryChangesRequest{Changes: changes}, testWorkspaceID, "agent-memory-daemon"), "taskId", taskID)
	testHandler.ReportAgentMemoryChanges(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("report memory changes: %d %s", w.Code, w.Body.String())
	}
	var resp DaemonAgentMemoryChangesResponse
	json.NewDecoder(w.Body).Decode(&resp)
	return resp
}

// runningMemoryTask is a task that just ran on runtimeID and may report its
// memory changes once.
func runningMemoryTask(t *testing.T, agentID, runtimeID, issueID string) string {
	t.Helper()
	return dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID, "status": "running", "started_at": testutil.Raw("now()")})
}

func callAgentMemoryRoute(t *testing.T, handler func(http.ResponseWriter, *http.Request), method, path string, body any, params ...string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	handler(w, withURLParams(newRequestAsUser(testUserID, method, path, body), params...))
	return w
}

func listMemoryProposals(t *testing.T, agentID, status string) []AgentMemoryProposalResponse {
	t.Helper()
	w := callAgentMemoryRoute(t, testHandler.ListAgentMemoryProposals, http.MethodGet,
		"/api/agents/"+agentID+"/memory/proposals?status="+status, nil, "id", agentID)
	if w.Code != http.StatusOK {
		t.Fatalf("list proposals: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Proposals []AgentMemoryProposalResponse `json:"proposals"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	return resp.Proposals
}

func memoryContent(t *testing.T, agentID string) map[string]string {
	t.Helper()
	w := callAgentMemory(t, testUserID, http.MethodGet, agentID, nil)
	var resp AgentMemoryResponse
	json.NewDecoder(w.Body).Decode(&resp)
	out := map[string]string{}
	for _, f := range resp.Files {
		out[f.Path] = f.Content
	}
	return out
}

func TestAgentMemoryWritebackReviewAutoAndOff(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := dbfx.Runtime(t, "Agent memory writeback runtime", testutil.Cols{"daemon_id": "agent-memory-daemon"})
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "Agent memory writeback agent")
	run := func() string { return runningMemoryTask(t, agentID, runtimeID, issueID) }
	if w := callAgentMemory(t, testUserID, http.MethodPut, agentID, map[string]any{"files": []map[string]any{
		{"path": "a.md", "content": "one"},
		{"path": "gone.md", "content": "bye"},
	}}); w.Code != http.StatusOK {
		t.Fatalf("seed memory: %d %s", w.Code, w.Body.String())
	}

	// Review mode (default): nothing is applied, every change waits.
	resp := reportAgentMemoryChanges(t, run(), []DaemonAgentMemoryChange{
		{Path: "a.md", Action: "upsert", Content: "two", BaseContent: strPtr("one"), Origin: "agent"},
		{Path: "new.md", Action: "upsert", Content: "fresh", Origin: "agent"},
		{Path: "gone.md", Action: "delete", BaseContent: strPtr("bye"), Origin: "agent"},
		{Path: "same.md", Action: "delete", Origin: "agent"},
		{Path: "../escape.md", Action: "upsert", Content: "x", Origin: "agent"},
	})
	if resp.Mode != "review" || resp.Proposed != 3 || resp.Applied != 0 || resp.Skipped != 2 {
		t.Fatalf("review report = %+v", resp)
	}
	pending := listMemoryProposals(t, agentID, "pending")
	if len(pending) != 3 {
		t.Fatalf("pending = %+v", pending)
	}
	byPath := map[string]AgentMemoryProposalResponse{}
	for _, p := range pending {
		byPath[p.Path] = p
	}
	if p := byPath["a.md"]; p.CurrentContent == nil || *p.CurrentContent != "one" || p.Content != "two" {
		t.Fatalf("a.md proposal = %+v", p)
	}
	if got := memoryContent(t, agentID); got["a.md"] != "one" {
		t.Fatalf("review mode applied a change: %v", got)
	}

	accept := func(id string) *httptest.ResponseRecorder {
		return callAgentMemoryRoute(t, testHandler.AcceptAgentMemoryProposal, http.MethodPost,
			"/api/agents/"+agentID+"/memory/proposals/"+id+"/accept", nil, "id", agentID, "proposalId", id)
	}
	if w := accept(byPath["a.md"].ID); w.Code != http.StatusOK {
		t.Fatalf("accept a.md: %d %s", w.Code, w.Body.String())
	}
	if w := accept(byPath["gone.md"].ID); w.Code != http.StatusOK {
		t.Fatalf("accept delete: %d %s", w.Code, w.Body.String())
	}
	if w := callAgentMemoryRoute(t, testHandler.RejectAgentMemoryProposal, http.MethodPost,
		"/api/agents/"+agentID+"/memory/proposals/"+byPath["new.md"].ID+"/reject", nil,
		"id", agentID, "proposalId", byPath["new.md"].ID); w.Code != http.StatusOK {
		t.Fatalf("reject: %d %s", w.Code, w.Body.String())
	}
	if w := accept(byPath["a.md"].ID); w.Code != http.StatusConflict {
		t.Fatalf("second accept: want 409, got %d", w.Code)
	}
	got := memoryContent(t, agentID)
	if got["a.md"] != "two" || got["new.md"] != "" || len(got) != 1 {
		t.Fatalf("after review = %v", got)
	}

	// A newer proposal for the same path supersedes the older one.
	reportAgentMemoryChanges(t, run(), []DaemonAgentMemoryChange{{Path: "b.md", Action: "upsert", Content: "v1", Origin: "agent"}})
	reportAgentMemoryChanges(t, run(), []DaemonAgentMemoryChange{{Path: "b.md", Action: "upsert", Content: "v2", Origin: "agent"}})
	if pending := listMemoryProposals(t, agentID, "pending"); len(pending) != 1 || pending[0].Content != "v2" {
		t.Fatalf("supersede: %+v", pending)
	}

	// Auto mode applies clean changes, keeps conflicts for review, and
	// drops runtime-origin changes when capture is off.
	if w := callAgentMemoryRoute(t, testHandler.UpdateAgentMemorySettings, http.MethodPut, "/api/agents/"+agentID+"/memory/settings",
		map[string]any{"writeback": "auto", "capture_runtime_memory": false}, "id", agentID); w.Code != http.StatusOK {
		t.Fatalf("settings: %d %s", w.Code, w.Body.String())
	}
	resp = reportAgentMemoryChanges(t, run(), []DaemonAgentMemoryChange{
		{Path: "c.md", Action: "upsert", Content: "auto", Origin: "agent"},
		{Path: "a.md", Action: "upsert", Content: "three", BaseContent: strPtr("stale"), Origin: "agent"},
		{Path: "auto/MEMORY.md", Action: "upsert", Content: "native", Origin: "runtime"},
	})
	if resp.Mode != "auto" || resp.Proposed != 2 || resp.Applied != 1 || resp.Skipped != 1 {
		t.Fatalf("auto report = %+v", resp)
	}
	got = memoryContent(t, agentID)
	if got["c.md"] != "auto" || got["a.md"] != "two" {
		t.Fatalf("after auto = %v", got)
	}
	var conflict *AgentMemoryProposalResponse
	for _, p := range listMemoryProposals(t, agentID, "pending") {
		if p.Path == "a.md" {
			conflict = &p
		}
	}
	if conflict == nil || !strings.Contains(conflict.Note, "changed") {
		t.Fatalf("conflict proposal = %+v", conflict)
	}

	// The claim tells a memory-aware daemon about the policy.
	if wb := testHandler.agentMemoryWritebackForClaim(ctx, parseUUID(agentID)); wb == nil || wb.Mode != "auto" || wb.CaptureRuntime {
		t.Fatalf("claim write-back = %+v", wb)
	}

	// Off: reports are ignored and the claim carries no policy.
	callAgentMemoryRoute(t, testHandler.UpdateAgentMemorySettings, http.MethodPut, "/api/agents/"+agentID+"/memory/settings",
		map[string]any{"writeback": "off"}, "id", agentID)
	if resp := reportAgentMemoryChanges(t, run(), []DaemonAgentMemoryChange{{Path: "d.md", Action: "upsert", Content: "x", Origin: "agent"}}); resp.Proposed != 0 || resp.Skipped != 1 {
		t.Fatalf("off report = %+v", resp)
	}
	if wb := testHandler.agentMemoryWritebackForClaim(ctx, parseUUID(agentID)); wb != nil {
		t.Fatalf("off claim write-back = %+v", wb)
	}
}

// A runtime import is a one-time copy: the platform keeps the content and
// remembers where it came from, but nothing ties the file to that machine.
func TestAgentMemoryRuntimeImportIsACopy(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := createRuntimeLocalSkillTestRuntime(t, testUserID)
	agentID := createAgentMemoryTestAgent(t, testUserID)
	// auto_sync from an older page is ignored rather than rejected.
	file := map[string]any{"path": "claude/CLAUDE.md", "content": "v1", "source": "runtime",
		"source_runtime_id": runtimeID, "source_path": "~/.claude/CLAUDE.md", "auto_sync": true}
	w := callAgentMemory(t, testUserID, http.MethodPut, agentID, map[string]any{"files": []map[string]any{file}})
	if w.Code != http.StatusOK {
		t.Fatalf("save imported file: %d %s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); strings.Contains(body, "auto_sync") || strings.Contains(body, "sync_supported") {
		t.Fatalf("response still carries sync fields: %s", body)
	}

	// Defaults: review the agent's own edits, leave the runtime's memory out.
	w = callAgentMemory(t, testUserID, http.MethodGet, agentID, nil)
	var resp AgentMemoryResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Files) != 1 || resp.Files[0].Source != "runtime" || resp.Files[0].SourcePath != "~/.claude/CLAUDE.md" {
		t.Fatalf("imported file = %+v", resp.Files)
	}
	if resp.Settings.Writeback != "review" || resp.Settings.CaptureRuntimeMemory {
		t.Fatalf("default settings = %+v", resp.Settings)
	}
	if wb := testHandler.agentMemoryWritebackForClaim(ctx, parseUUID(agentID)); wb == nil || wb.Mode != "review" || wb.CaptureRuntime {
		t.Fatalf("default claim write-back = %+v", wb)
	}
}

func TestAgentMemoryReportAuthorization(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := dbfx.Runtime(t, "Agent memory report auth runtime", testutil.Cols{"daemon_id": "agent-memory-daemon"})
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "Agent memory report auth agent")
	change := []DaemonAgentMemoryChange{{Path: "x.md", Action: "upsert", Content: "x", Origin: "agent"}}
	call := func(req *http.Request, taskID string) int {
		t.Helper()
		w := httptest.NewRecorder()
		testHandler.ReportAgentMemoryChanges(w, withURLParams(req, "taskId", taskID))
		return w.Code
	}
	body := DaemonAgentMemoryChangesRequest{Changes: change}
	path := func(taskID string) string { return "/api/daemon/tasks/" + taskID + "/memory-changes" }

	// Another workspace member's PAT is refused, even with a valid task id.
	taskID := runningMemoryTask(t, agentID, runtimeID, issueID)
	memberID := createRuntimeLocalSkillTestMember(t, "member")
	if code := call(newRequestAsUser(memberID, http.MethodPost, path(taskID), body), taskID); code != http.StatusForbidden {
		t.Fatalf("other member: want 403, got %d", code)
	}
	// So is a different daemon.
	if code := call(newDaemonTokenRequest(http.MethodPost, path(taskID), body, testWorkspaceID, "someone-elses-daemon"), taskID); code != http.StatusForbidden {
		t.Fatalf("other daemon: want 403, got %d", code)
	}
	// The runtime owner's PAT (how a local daemon signs in) is accepted once.
	if code := call(newRequestAsUser(testUserID, http.MethodPost, path(taskID), body), taskID); code != http.StatusOK {
		t.Fatalf("runtime owner: want 200, got %d", code)
	}
	if code := call(newDaemonTokenRequest(http.MethodPost, path(taskID), body, testWorkspaceID, "agent-memory-daemon"), taskID); code != http.StatusConflict {
		t.Fatalf("replay: want 409, got %d", code)
	}
	// A run that ended long ago cannot report any more.
	old := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID, "status": "completed",
		"started_at": testutil.Raw("now() - interval '2 hours'"), "completed_at": testutil.Raw("now() - interval '1 hour'")})
	if code := call(newDaemonTokenRequest(http.MethodPost, path(old), body, testWorkspaceID, "agent-memory-daemon"), old); code != http.StatusConflict {
		t.Fatalf("stale task: want 409, got %d", code)
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM agent_memory_proposal WHERE agent_id = $1`, agentID); n != 1 {
		t.Fatalf("want exactly one proposal from the one accepted report, got %d", n)
	}
}

func TestAgentMemorySaveRefusesStaleVersion(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := createAgentMemoryTestAgent(t, testUserID)
	put := func(body map[string]any) (*httptest.ResponseRecorder, AgentMemoryResponse) {
		t.Helper()
		w := callAgentMemory(t, testUserID, http.MethodPut, agentID, body)
		var resp AgentMemoryResponse
		json.NewDecoder(strings.NewReader(w.Body.String())).Decode(&resp)
		return w, resp
	}
	w, first := put(map[string]any{"files": []map[string]any{{"path": "a.md", "content": "one"}}})
	if w.Code != http.StatusOK || first.Version == "" {
		t.Fatalf("seed: %d %+v", w.Code, first)
	}
	// Another writer (a run or another tab) changes the set.
	w, second := put(map[string]any{"files": []map[string]any{{"path": "a.md", "content": "two"}}, "base_version": first.Version})
	if w.Code != http.StatusOK || second.Version == first.Version {
		t.Fatalf("save from the current version: %d %+v", w.Code, second)
	}
	// A save from the page loaded before that change is refused.
	if w, _ := put(map[string]any{"files": []map[string]any{{"path": "a.md", "content": "stale page"}}, "base_version": first.Version}); w.Code != http.StatusConflict {
		t.Fatalf("stale save: want 409, got %d", w.Code)
	}
	if got := memoryContent(t, agentID); got["a.md"] != "two" {
		t.Fatalf("stale save changed memory: %v", got)
	}
	// Re-saving identical content keeps the version stable.
	if _, again := put(map[string]any{"files": []map[string]any{{"path": "a.md", "content": "two"}}, "base_version": second.Version}); again.Version != second.Version {
		t.Fatalf("unchanged save moved the version: %s -> %s", second.Version, again.Version)
	}
}

func TestAgentMemoryReportsRuntimeSupport(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	oldRuntime := dbfx.Runtime(t, "agent-memory-old-daemon", testutil.Cols{"metadata": testutil.Raw(`'{"capabilities":["skill-bundles-v1"]}'::jsonb`)})
	newRuntime := dbfx.Runtime(t, "agent-memory-new-daemon", testutil.Cols{"metadata": testutil.Raw(`'{"capabilities":["agent-memory-v1"]}'::jsonb`)})
	agentID := dbfx.Agent(t, "agent-memory-support-agent", oldRuntime, testutil.Cols{"owner_id": testUserID})
	if w := callAgentMemory(t, testUserID, http.MethodPut, agentID, map[string]any{"files": []map[string]any{
		{"path": "imported.md", "content": "x", "source": "runtime", "source_runtime_id": newRuntime, "source_path": "~/.claude/CLAUDE.md"},
	}}); w.Code != http.StatusOK {
		t.Fatalf("seed: %d %s", w.Code, w.Body.String())
	}
	w := callAgentMemory(t, testUserID, http.MethodGet, agentID, nil)
	var resp AgentMemoryResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.RuntimeSupportsWriteback == nil || *resp.RuntimeSupportsWriteback {
		t.Fatalf("old daemon runtime should report no write-back support: %+v", resp.RuntimeSupportsWriteback)
	}
	// The runtime a file was imported from does not matter: only the
	// agent's own runtime decides whether write-back works.
	w = callAgentMemory(t, testUserID, http.MethodGet, dbfx.Agent(t, "agent-memory-support-new", newRuntime, testutil.Cols{"owner_id": testUserID}), nil)
	resp = AgentMemoryResponse{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.RuntimeSupportsWriteback == nil || !*resp.RuntimeSupportsWriteback {
		t.Fatalf("new daemon runtime should report write-back support: %+v", resp.RuntimeSupportsWriteback)
	}
}
