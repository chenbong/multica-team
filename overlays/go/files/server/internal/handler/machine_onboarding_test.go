package handler

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestMachineProvisionReusesOnlyOwnedUsernameWorkspace(t *testing.T) {
	username := machineTestName()
	user := dbfx.User(t, "Fixture", username+"@example.test")
	owned := dbfx.Workspace(t, username, username)
	dbfx.Member(t, owned, user, "owner")
	h := machineTestConfig(t, username)
	for i := 0; i < 2; i++ {
		response := machineCall(t, h, machineBody(username), provisionServiceToken)
		if response.Code != 200 || !strings.Contains(response.Body.String(), "tmesh-machine prepare --workspace-id '"+owned+"'") {
			t.Fatalf("owned workspace not selected: %d", response.Code)
		}
	}
	rows, err := h.Queries.ListWorkspaces(context.Background(), parseUUID(user))
	if err != nil || len(rows) != 1 {
		t.Fatal("owned workspace duplicated")
	}
}

func TestMachineProvisionDoesNotReuseSomeoneElsesSameNameWorkspace(t *testing.T) {
	username := machineTestName()
	user := dbfx.User(t, "Fixture", username+"@example.test")
	foreign := dbfx.Workspace(t, username, username)
	dbfx.Member(t, foreign, user, "admin")
	h := machineTestConfig(t, username)
	response := machineCall(t, h, machineBody(username), provisionServiceToken)
	if response.Code != 200 {
		t.Fatalf("status %d", response.Code)
	}
	rows, err := h.Queries.ListWorkspaces(context.Background(), parseUUID(user))
	if err != nil || len(rows) != 2 {
		t.Fatal("personal workspace not created")
	}
	for _, row := range rows {
		if uuidToString(row.ID) == foreign {
			continue
		}
		if row.Name != username || !strings.HasPrefix(row.Slug, username+"-") {
			t.Fatal("slug conflict was not resolved safely")
		}
	}
	var role string
	_ = testPool.QueryRow(context.Background(), `SELECT role FROM member WHERE user_id=$1 AND workspace_id=$2`, user, foreign).Scan(&role)
	if role != "admin" {
		t.Fatal("foreign workspace permissions changed")
	}
}

func TestMachineProvisionAmbiguousOwnedWorkspacesFailWithoutGuessing(t *testing.T) {
	username := machineTestName()
	user := dbfx.User(t, "Fixture", username+"@example.test")
	for i := 0; i < 2; i++ {
		ws := dbfx.Workspace(t, username, "fixture-"+uuid.NewString())
		dbfx.Member(t, ws, user, "owner")
	}
	h := machineTestConfig(t, username)
	if response := machineCall(t, h, machineBody(username), provisionServiceToken); response.Code != 409 {
		t.Fatalf("ambiguous workspace status %d", response.Code)
	}
}

func TestMachineProvisionOptionalFieldsAreValidatedBeforeCreatingAccounts(t *testing.T) {
	username := machineTestName()
	h := machineTestConfig(t, username)
	for _, extra := range []string{`,"model":"x"`, `,"thinking_level":"high"`, `,"runtime":"../ducc"`, `,"agent_name":"x\ny"`, `,"machine_name":"x\u0000y"`, `,"agent_name":"x","model":false`} {
		body := `{"username":"` + username + `"` + extra + `}`
		if response := machineCall(t, h, body, provisionServiceToken); response.Code != 400 {
			t.Fatalf("invalid options status %d", response.Code)
		}
	}
	if _, err := h.Queries.GetUserByEmail(context.Background(), username+"@example.test"); err == nil {
		t.Fatal("invalid options created an account")
	}
}

func TestMachineProvisionUsernameSlugNormalizesDotsAndUnderscores(t *testing.T) {
	username := "test." + strings.ReplaceAll(uuid.NewString(), "-", "")[:8] + "_owner"
	h := machineTestConfig(t, username)
	response := machineCall(t, h, machineBody(username), provisionServiceToken)
	if response.Code != 200 {
		t.Fatalf("status %d", response.Code)
	}
	user, err := h.Queries.GetUserByEmail(context.Background(), username+"@example.test")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := h.Queries.ListWorkspaces(context.Background(), user.ID)
	if err != nil || len(rows) != 1 {
		t.Fatal("workspace missing")
	}
	if rows[0].Name != username || rows[0].Slug != strings.NewReplacer(".", "-", "_", "-").Replace(username) {
		t.Fatal("username/display name/slug mismatch")
	}
}

func TestMachineProvisionGeneratedScriptQuotesArgumentsAndPreservesOrder(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash unavailable")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.jsonl")
	marker := filepath.Join(dir, "must-not-exist")
	cli := `#!/usr/bin/env python3
import os,json,sys
with open(os.environ['FIXTURE_CALLS'],'a') as f:f.write(json.dumps(sys.argv[1:])+'\n')
`
	for name, content := range map[string]string{"multica": cli, "ducc": "#!/bin/sh\nexit 0\n", "curl": "#!/bin/sh\nexit 0\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0700); err != nil {
			t.Fatal(err)
		}
	}
	malicious := "Host ' $(touch " + marker + ")"
	model, thinking := "custom-model", "high"
	input := machineProvisionInput{MachineName: malicious, AgentName: "My agent", Runtime: "ducc", Model: &model, ThinkingLevel: &thinking}
	command := machineOnboardingCommand(machineCommandConfig{InstallURL: "https://example.test/install.sh", ServerURL: "https://api.example.test", AppURL: "https://app.example.test"}, "owner@example.test", "fixture-token", "11111111-1111-4111-8111-111111111111", input)
	cmd := exec.Command("bash")
	cmd.Stdin = strings.NewReader(command)
	cmd.Env = append(os.Environ(), "HOME="+dir, "MULTICA_BIN_DIR="+dir, "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "FIXTURE_CALLS="+logPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture script failed: %v %s", err, output)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("name interpreted as a shell command")
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var args []string
		if err = json.Unmarshal([]byte(line), &args); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, args)
	}
	indices := map[string]int{}
	for i, args := range calls {
		for _, value := range args {
			if value == "prepare" || value == "login" || value == "restart" || value == "enroll" {
				indices[value] = i
			}
		}
	}
	if !(indices["prepare"] < indices["login"] && indices["login"] < indices["restart"] && indices["restart"] < indices["enroll"]) {
		t.Fatalf("wrong execution order: %v", indices)
	}
	last := calls[len(calls)-1]
	found := false
	for _, value := range last {
		if value == malicious {
			found = true
		}
	}
	if !found {
		t.Fatal("machine name did not round-trip as one literal argument")
	}
}

func TestMachineProvisionScriptEmitsOneJSONOnSuccessAndFailure(t *testing.T) {
	for _, failure := range []string{"", "install", "login", "runtime_setup", "daemon_restart", "enrollment"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			fake := `#!/usr/bin/env python3
import os,json,sys
args=sys.argv[1:]
stage=''
if 'login' in args: stage='login'
elif 'setup' in args: stage='runtime_setup'
elif 'restart' in args: stage='daemon_restart'
elif 'enroll' in args: stage='enrollment'
failed=stage and stage==os.environ.get('FIXTURE_FAILURE')
if 'enroll' in args:
 print(json.dumps({'success':not bool(failed),'daemon_id':'fixture-machine','runtime_id':'fixture-runtime','completed_steps':['machine_renamed']}))
else: print('ordinary CLI log')
sys.exit(7 if failed else 0)
`
			for name, content := range map[string]string{
				"multica": fake,
				"ducc":    "#!/bin/sh\necho 'runtime log'\n",
				"curl":    "#!/bin/sh\nif [ \"$FIXTURE_FAILURE\" = install ]; then exit 7; fi\nprintf 'echo installer-log\\n'\n",
			} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0700); err != nil {
					t.Fatal(err)
				}
			}
			command := machineOnboardingCommand(machineCommandConfig{InstallURL: "https://example.test/install.sh"}, "owner@example.test", "fixture-token", "11111111-1111-4111-8111-111111111111", machineProvisionInput{})
			cmd := exec.Command("bash")
			cmd.Stdin = strings.NewReader(command)
			cmd.Env = append(os.Environ(), "HOME="+dir, "MULTICA_BIN_DIR="+dir, "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "FIXTURE_FAILURE="+failure)
			var stderr strings.Builder
			cmd.Stderr = &stderr
			output, err := cmd.Output()
			if (failure == "") != (err == nil) {
				t.Fatalf("wrong exit status: %v %s", err, stderr.String())
			}
			var result map[string]any
			if json.Unmarshal(output, &result) != nil || result["success"] != (failure == "") || strings.Contains(string(output), "fixture-token") {
				t.Fatalf("stdout must contain exactly one safe JSON result: %s", output)
			}
			if failure != "" && result["failed_step"] != failure {
				t.Fatalf("wrong failed step: %v", result)
			}
			if failure == "enrollment" || failure == "" {
				detail, _ := result["result"].(map[string]any)
				if detail["daemon_id"] != "fixture-machine" {
					t.Fatal("machine IDs and partial enrollment results were lost")
				}
			}
			if strings.Contains(stderr.String(), "fixture-token") {
				t.Fatal("login credential exposed in logs")
			}
		})
	}
}
