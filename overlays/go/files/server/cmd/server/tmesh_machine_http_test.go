package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestTmeshMachineEndpointsThroughRouter(t *testing.T) {
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
	first := run("agent", "--workspace-name", "Integration Tests", "--agent-name", name, "--runtime", "ducc", "--wait", "3s")
	second := run("agent", "--workspace-name", "Integration Tests", "--agent-name", name, "--runtime", "ducc", "--wait", "3s")
	if first["agent_created"] != true || second["agent_created"] != false || first["agent_id"] != second["agent_id"] {
		t.Fatalf("agent was not reused: %v %v", first, second)
	}
	var visibility, bound string
	if err := testPool.QueryRow(t.Context(), "SELECT visibility,runtime_id::text FROM agent WHERE id=$1", first["agent_id"]).Scan(&visibility, &bound); err != nil || visibility != "workspace" || bound != runtimeID {
		t.Fatal(fmt.Errorf("wrong agent visibility %q or runtime %q: %v", visibility, bound, err))
	}
}
