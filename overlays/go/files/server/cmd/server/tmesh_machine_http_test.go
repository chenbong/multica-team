package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/auth"
)

func TestTmeshMachineEndpointsThroughRouter(t *testing.T) {
	releases := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("[{\"tag_name\":\"v0.6.1-overlay.10\",\"published_at\":\"2026-10-28T01:00:00Z\"}]"))
	}))
	defer releases.Close()
	t.Setenv("MULTICA_MACHINE_RELEASE_API_URL", releases.URL)
	version := authRequest(t, "GET", "/api/tmesh/releases/latest", nil)
	versionBody, _ := io.ReadAll(version.Body)
	version.Body.Close()
	if version.StatusCode != http.StatusOK || string(versionBody) != "v0.6.1-overlay.10\n" {
		t.Fatalf("release lookup status %d: %s", version.StatusCode, versionBody)
	}
	response := authRequest(t, "POST", "/api/tmesh/workspaces/ensure", map[string]string{"name": "Integration Tests"})
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		t.Fatalf("workspace ensure status: %d", response.StatusCode)
	}
	var workspace map[string]any
	readJSON(t, response, &workspace)
	if workspace["workspace_id"] != testWorkspaceID || workspace["workspace_created"] != false {
		t.Fatalf("workspace was not reused: %v", workspace)
	}
	lock := map[string]string{"workspace_id": testWorkspaceID, "agent_name": "Machine fixture"}
	first := authRequest(t, "POST", "/api/tmesh/agents/lock", lock)
	if first.StatusCode != http.StatusOK {
		first.Body.Close()
		t.Fatalf("agent lock status: %d", first.StatusCode)
	}
	second := authRequest(t, "POST", "/api/tmesh/agents/lock", lock)
	if second.StatusCode != http.StatusConflict {
		second.Body.Close()
		first.Body.Close()
		t.Fatalf("concurrent agent lock status: %d", second.StatusCode)
	}
	second.Body.Close()
	first.Body.Close()
}

func TestTmeshReleaseLookupAcceptsNewUserWithoutWorkspace(t *testing.T) {
	releases := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("[{\"tag_name\":\"v0.6.1-overlay.10\",\"published_at\":\"2026-10-28T01:00:00Z\"}]"))
	}))
	defer releases.Close()
	t.Setenv("MULTICA_MACHINE_RELEASE_API_URL", releases.URL)
	var userID string
	email := "fixture-" + uuid.NewString() + "@example.test"
	if err := testPool.QueryRow(t.Context(), "INSERT INTO \"user\"(name,email) VALUES('fixture',$1) RETURNING id::text", email).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), "DELETE FROM personal_access_token WHERE user_id=$1", userID)
		_, _ = testPool.Exec(context.Background(), "DELETE FROM \"user\" WHERE id=$1", userID)
	})
	token, err := auth.GeneratePATToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = testPool.Exec(t.Context(), "INSERT INTO personal_access_token(user_id,name,token_hash,token_prefix) VALUES($1,'fixture',$2,$3)", userID, auth.HashToken(token), token[:12]); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest("GET", testServer.URL+"/api/tmesh/releases/latest", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || string(body) != "v0.6.1-overlay.10\n" {
		t.Fatalf("workspace-free PAT release lookup: %d %s", response.StatusCode, body)
	}
}

// This uses a fixture runtime and a temporary HOME; it never starts a daemon
// or invokes a real agent CLI.
func TestTmeshMachineCLIThroughRouter(t *testing.T) {
	binary := os.Getenv("TMESH_CLI_TEST_BINARY")
	if binary == "" {
		t.Skip("set TMESH_CLI_TEST_BINARY to the isolated CLI build")
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".multica"), 0700); err != nil {
		t.Fatal(err)
	}
	did := uuid.NewString()
	if err := os.WriteFile(filepath.Join(home, ".multica", "daemon.id"), []byte(did+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var profileID string
	err := testPool.QueryRow(t.Context(), `INSERT INTO runtime_profile
		(workspace_id,display_name,protocol_family,command_name,description,created_by,enabled)
		VALUES ($1,'ducc fixture','claude','ducc','multica:auto-internal-runtime:ducc',$2,true) RETURNING id::text`, testWorkspaceID, testUserID).Scan(&profileID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), "DELETE FROM runtime_profile WHERE id=$1", profileID)
	})
	var runtimeID string
	err = testPool.QueryRow(t.Context(), `INSERT INTO agent_runtime
		(workspace_id,daemon_id,name,runtime_mode,provider,status,device_info,metadata,owner_id,last_seen_at,visibility,profile_id)
		VALUES ($1,$2,'CLI fixture','local','claude','online','fixture','{}'::jsonb,$3,now(),'private',$4) RETURNING id::text`, testWorkspaceID, did, testUserID, profileID).Scan(&runtimeID)
	if err != nil {
		t.Fatal(err)
	}
	name := "CLI fixture " + uuid.NewString()
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), "DELETE FROM agent_skill WHERE agent_id IN (SELECT id FROM agent WHERE workspace_id=$1 AND name=$2)", testWorkspaceID, name)
		_, _ = testPool.Exec(context.Background(), "DELETE FROM agent WHERE workspace_id=$1 AND name=$2", testWorkspaceID, name)
		_, _ = testPool.Exec(context.Background(), "DELETE FROM user_machine_name WHERE user_id=$1 AND machine_key=$2", testUserID, "daemon:local:"+did)
		_, _ = testPool.Exec(context.Background(), "DELETE FROM agent_runtime WHERE id=$1", runtimeID)
	})
	run := func(args ...string) map[string]any {
		t.Helper()
		cmd := exec.Command(binary, append([]string{"--profile", "fixture", "tmesh-machine"}, args...)...)
		cmd.Env = append(os.Environ(), "HOME="+home, "MULTICA_TOKEN="+testToken, "MULTICA_SERVER_URL="+testServer.URL,
			"MULTICA_WORKSPACE_ID=", "MULTICA_AGENT_ID=", "MULTICA_TASK_ID=", "MULTICA_DAEMON_PORT=", "MULTICA_TASK_CONFIG_ROOT=")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("CLI %v failed: %v %s", args, err, out)
		}
		var result map[string]any
		if json.Unmarshal(out, &result) != nil || result["success"] != true {
			t.Fatalf("invalid CLI result: %s", out)
		}
		return result
	}
	rename := run("rename", "--workspace-name", "Integration Tests", "--machine-name", "CLI fixture machine", "--wait", "3s")
	if rename["machine_name"] != "CLI fixture machine" {
		t.Fatal("machine was not renamed")
	}
	createSkill := func(scope string) (string, string) {
		t.Helper()
		skillName := "machine-skill-" + uuid.NewString()
		resp := authRequest(t, "POST", "/api/skills", map[string]string{"name": skillName, "content": "Synthetic local test skill", "scope": scope})
		if resp.StatusCode != http.StatusCreated {
			defer resp.Body.Close()
			data, _ := io.ReadAll(resp.Body)
			t.Fatalf("skill fixture status %d: %s", resp.StatusCode, data)
		}
		var skill map[string]any
		readJSON(t, resp, &skill)
		id := skill["id"].(string)
		t.Cleanup(func() {
			_, _ = testPool.Exec(context.Background(), "DELETE FROM agent_skill WHERE skill_id=$1", id)
			_, _ = testPool.Exec(context.Background(), "DELETE FROM skill WHERE id=$1", id)
		})
		return id, skillName
	}
	skillA, skillName := createSkill("workspace")
	skillB, _ := createSkill("personal")
	first := run("agent", "--workspace-name", "Integration Tests", "--agent-name", name, "--runtime", "ducc", "--wait", "3s", "--skill-list", skillName, "--system-prompt", "Initial fixture instructions")
	var initialInstructions string
	if err := testPool.QueryRow(t.Context(), "SELECT instructions FROM agent WHERE id=$1", first["agent_id"]).Scan(&initialInstructions); err != nil || initialInstructions != "Initial fixture instructions" {
		t.Fatalf("initial instructions were not persisted: %v", err)
	}
	if _, err := testPool.Exec(t.Context(), "UPDATE agent_skill SET enabled=false WHERE agent_id=$1 AND skill_id=$2", first["agent_id"], skillA); err != nil {
		t.Fatal(err)
	}
	promptFile := filepath.Join(home, "prompt.md")
	promptText := "你是测试助手。\nKeep spaces, $HOME and `code`.\r\n\n"
	if err := os.WriteFile(promptFile, []byte(promptText), 0600); err != nil {
		t.Fatal(err)
	}
	second := run("agent", "--workspace-name", "Integration Tests", "--agent-name", name, "--runtime", "ducc", "--wait", "3s", "--skill-list", skillName+","+skillB, "--system-prompt-file", promptFile)
	if first["agent_created"] != true || second["agent_created"] != false || first["agent_id"] != second["agent_id"] {
		t.Fatalf("agent was not reused: %v %v", first, second)
	}
	var visibility, bound string
	if err := testPool.QueryRow(t.Context(), "SELECT visibility,runtime_id::text FROM agent WHERE id=$1", first["agent_id"]).Scan(&visibility, &bound); err != nil || visibility != "workspace" || bound != runtimeID {
		t.Fatal(fmt.Errorf("wrong agent visibility %q or runtime %q: %v", visibility, bound, err))
	}
	// Omitting the flag preserves both bindings, and repeating one does not
	// replace the other assignment.
	run("agent", "--workspace-name", "Integration Tests", "--agent-name", name, "--wait", "3s")
	run("agent", "--workspace-name", "Integration Tests", "--agent-name", name, "--wait", "3s", "--skill-list", skillA)
	var instructions string
	if err := testPool.QueryRow(t.Context(), "SELECT instructions FROM agent WHERE id=$1", first["agent_id"]).Scan(&instructions); err != nil || instructions != promptText {
		t.Fatalf("instructions were not preserved exactly: %v", err)
	}
	var total, enabled int
	if err := testPool.QueryRow(t.Context(), "SELECT count(*),count(*) FILTER (WHERE enabled) FROM agent_skill WHERE agent_id=$1", first["agent_id"]).Scan(&total, &enabled); err != nil || total != 2 || enabled != 2 {
		t.Fatalf("bindings not preserved/enabled: %d %d %v", total, enabled, err)
	}
	cmd := exec.Command(binary, "--profile", "fixture", "tmesh-machine", "agent", "--workspace-name", "Integration Tests", "--agent-name", name, "--machine-name", "must-not-rename", "--skill-list", "missing-"+uuid.NewString())
	cmd.Env = append(os.Environ(), "HOME="+home, "MULTICA_TOKEN="+testToken, "MULTICA_SERVER_URL="+testServer.URL,
		"MULTICA_WORKSPACE_ID=", "MULTICA_AGENT_ID=", "MULTICA_TASK_ID=", "MULTICA_DAEMON_PORT=", "MULTICA_TASK_CONFIG_ROOT=")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "not found or is not accessible") {
		t.Fatalf("missing skill was not rejected: %s %v", out, err)
	}
	var machineName string
	if err := testPool.QueryRow(t.Context(), "SELECT name FROM user_machine_name WHERE user_id=$1 AND machine_key=$2", testUserID, "daemon:local:"+did).Scan(&machineName); err != nil || machineName != "CLI fixture machine" {
		t.Fatalf("invalid list changed machine name: %s %v", machineName, err)
	}
	run("agent", "--workspace-name", "Integration Tests", "--agent-name", name, "--wait", "3s", "--system-prompt", "")
	if err := testPool.QueryRow(t.Context(), "SELECT instructions FROM agent WHERE id=$1", first["agent_id"]).Scan(&instructions); err != nil || instructions != "" {
		t.Fatalf("explicit empty prompt did not clear instructions: %v", err)
	}
}
