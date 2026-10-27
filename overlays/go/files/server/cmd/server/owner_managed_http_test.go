package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/realtime"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestOwnerManagedReadAuthenticatedHTTP(t *testing.T) {
	if os.Getenv("MULTICA_ISSUE_TRANSFER_HTTP_TEST") != "1" {
		t.Skip("opt-in isolated HTTP test")
	}
	t.Setenv("TEAM_MESH_ISSUE_LINK_ORIGINS", "http://mesh.example")
	dsn := os.Getenv("DATABASE_URL")
	u, err := url.Parse(dsn)
	if err != nil || u.Hostname() != "127.0.0.1" || u.Path != "/issue_transfer_http_test" {
		t.Fatal("dedicated loopback HTTP database required")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	f := testutil.New(pool, "", "")
	machineOwner := f.User(t, "Machine owner", uuid.NewString()+"@example.test")
	agentOwner := f.User(t, "Agent owner", uuid.NewString()+"@example.test")
	source := f.Workspace(t, "Source", "scope-src-"+uuid.NewString())
	targetSlug := "scope-dst-" + uuid.NewString()
	target := f.Workspace(t, "Target", targetSlug, testutil.Cols{"issue_prefix": "CTX"})
	siblingSlug := "scope-sibling-" + uuid.NewString()
	sibling := f.Workspace(t, "Sibling", siblingSlug, testutil.Cols{"issue_prefix": "CTX"})
	outOfScope := f.Workspace(t, "Not visible to agent owner", "hidden-"+uuid.NewString())
	f.Member(t, source, machineOwner, "owner")
	f.Member(t, source, agentOwner, "member")
	f.Member(t, target, agentOwner, "member")
	f.Member(t, target, machineOwner, "member")
	f.Member(t, sibling, agentOwner, "owner")
	f.Member(t, outOfScope, machineOwner, "owner")
	f.WorkspaceID = source
	f.UserID = machineOwner
	runtime := f.Runtime(t, "Shared machine", testutil.Cols{"visibility": "public"})
	agent := f.Agent(t, "Shared reader", runtime, testutil.Cols{"owner_id": agentOwner})
	task := f.Task(t, agent, testutil.Cols{"runtime_id": runtime, "status": "running"})
	token := "mat_" + uuid.NewString()
	hash := sha256.Sum256([]byte(token))
	encoded := hex.EncodeToString(hash[:])
	f.Insert(t, "task_token", testutil.Cols{"token_hash": encoded, "task_id": task, "agent_id": agent, "workspace_id": source, "user_id": machineOwner, "expires_at": testutil.Raw("now()+interval '10 minutes'")})
	f.InsertNoID(t, "agent_token_read_scope", testutil.Cols{"token_hash": encoded, "task_id": task, "agent_id": agent, "owner_id": agentOwner, "workspace_id": source}, "token_hash=$1", encoded)
	own := f.Issue(t, "Agent owner's context", testutil.Cols{"workspace_id": target, "creator_id": agentOwner})
	other := f.Issue(t, "Machine owner's context", testutil.Cols{"workspace_id": target, "creator_id": machineOwner})
	siblingIssue := f.Issue(t, "Same identifier in sibling", testutil.Cols{"workspace_id": sibling, "creator_id": agentOwner})
	hidden := f.Issue(t, "Not visible to agent owner", testutil.Cols{"workspace_id": outOfScope, "creator_id": machineOwner})
	f.Exec(t, `SELECT team_mesh_ensure_issue_managers($1),team_mesh_ensure_issue_managers($2),team_mesh_ensure_issue_managers($3)`, own, other, siblingIssue)
	f.Comment(t, own, "Authorized fixture comment", testutil.Cols{"workspace_id": target, "author_id": agentOwner})
	run := f.Task(t, agent, testutil.Cols{"runtime_id": runtime, "issue_id": own, "status": "completed"})
	f.Insert(t, "task_message", testutil.Cols{"task_id": run, "seq": 1, "type": "text", "content": "authorized execution message"})
	hub := realtime.NewHub()
	go hub.Run()
	server := httptest.NewServer(NewRouter(pool, hub, events.New(), analytics.NoopClient{}, nil))
	defer server.Close()
	call := func(method, path, ws string, want int) string {
		t.Helper()
		r, _ := http.NewRequest(method, server.URL+path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		if ws != "" {
			r.Header.Set("X-Workspace-ID", ws)
		}
		resp, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != want {
			t.Fatalf("%s %s status=%d want=%d body=%s", method, path, resp.StatusCode, want, raw)
		}
		return string(raw)
	}
	listing := call("GET", "/api/agent/managed-issues", "", 200)
	if !strings.Contains(listing, own) || !strings.Contains(listing, other) {
		t.Fatal("owner workspace access was not applied")
	}
	// Identical human identifiers across workspaces must never choose the
	// first match; resolve requires the exact workspace slug and owner scope.
	resolved := call("GET", "/api/agent/managed-issues?workspace_slug="+url.QueryEscape(targetSlug)+"&identifier=CTX-1", "", 200)
	if !strings.Contains(resolved, own) || strings.Contains(resolved, siblingIssue) || !strings.Contains(resolved, targetSlug) {
		t.Fatal("exact workspace resolution returned wrong task")
	}
	siblingResult := call("GET", "/api/agent/managed-issues?workspace_slug="+url.QueryEscape(siblingSlug)+"&identifier=CTX-1", "", 200)
	if !strings.Contains(siblingResult, siblingIssue) || strings.Contains(siblingResult, own) {
		t.Fatal("sibling workspace resolution failed")
	}
	byUUID := call("GET", "/api/agent/managed-issues?workspace_slug="+url.QueryEscape(targetSlug)+"&identifier="+own, "", 200)
	if !strings.Contains(byUUID, own) || strings.Contains(byUUID, siblingIssue) {
		t.Fatal("UUID resolution failed")
	}
	wrongScope := call("GET", "/api/agent/managed-issues?workspace_slug="+url.QueryEscape(siblingSlug)+"&identifier="+own, "", 200)
	if strings.Contains(wrongScope, own) {
		t.Fatal("UUID bypassed workspace selection")
	}
	unauthorized := call("GET", "/api/agent/managed-issues?workspace_slug="+url.QueryEscape(targetSlug)+"&identifier="+other, "", 200)
	if !strings.Contains(unauthorized, other) {
		t.Fatal("resolver excluded a task visible to the agent owner")
	}
	call("GET", "/api/agent/managed-issues/CTX-1", "", 409)
	call("GET", "/api/agent/managed-issues/"+own, "", 200)
	call("GET", "/api/agent/managed-issues/"+other, "", 200)
	call("GET", "/api/issues/"+own, target, 200)
	call("GET", "/api/issues/"+own+"/comments", "", 200)
	call("GET", "/api/issues/"+hidden, "", 404)
	if !strings.Contains(call("GET", "/api/tasks/"+run+"/messages", "", 200), "authorized execution message") {
		t.Fatal("authorized execution messages not returned")
	}
	call("GET", "/api/issues/CTX-1", "", 409)
	call("GET", "/api/issues/CTX-1?team_mesh_workspace="+url.QueryEscape(targetSlug), "", 200)
	if cliPath := os.Getenv("TEAM_MESH_TEST_CLI"); cliPath != "" {
		cmd := exec.Command(cliPath, "issue", "comment", "list", "http://mesh.example/"+targetSlug+"/issues/CTX-1", "--output", "json")
		cmd.Env = append(os.Environ(), "MULTICA_TOKEN="+token, "MULTICA_SERVER_URL="+server.URL, "MULTICA_APP_URL=", "FRONTEND_ORIGIN=", "MULTICA_WORKSPACE_ID="+source, "MULTICA_AGENT_ID="+agent, "MULTICA_TASK_ID="+task)
		output, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(output), "Authorized fixture comment") {
			t.Fatalf("CLI could not read the target task's comments: %v: %s", err, output)
		}
	}
	if cliPath := os.Getenv("TEAM_MESH_OLD_CLI"); cliPath != "" {
		cmd := exec.Command(cliPath, "issue", "get", "CTX-2", "--output", "json")
		cmd.Env = append(os.Environ(), "MULTICA_TOKEN="+token, "MULTICA_SERVER_URL="+server.URL, "MULTICA_WORKSPACE_ID="+source, "MULTICA_AGENT_ID="+agent, "MULTICA_TASK_ID="+task)
		output, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(output), other) {
			t.Fatalf("existing CLI could not read an unambiguous key: %v: %s", err, output)
		}
	}
	call("PUT", "/api/issues/"+own, target, 404)
	call("PUT", "/api/agent/managed-issues/"+own, "", 405)
	f.Exec(t, `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, target, agentOwner)
	call("GET", "/api/issues/"+own, "", 404)
	call("GET", "/api/tasks/"+run+"/messages", "", 404)
	f.Exec(t, `UPDATE agent SET owner_id=$2 WHERE id=$1`, agent, machineOwner)
	call("GET", "/api/agent/managed-issues/"+own, "", 403)
}
