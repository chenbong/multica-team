package handler

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runMachineScriptFixture(t *testing.T, machine, fail string, versions ...string) (string, string, [][]string, error) {
	t.Helper()
	return runMachineScriptInputFixture(t, machineProvisionInput{MachineName: machine}, fail, versions...)
}

func runMachineScriptInputFixture(t *testing.T, input machineProvisionInput, fail string, versions ...string) (string, string, [][]string, error) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.jsonl")
	fakeCLI := strings.Join([]string{
		"#!/usr/bin/env python3",
		"import json,os,sys",
		"args=sys.argv[1:]",
		"with open(os.environ['FIXTURE_CALLS'],'a') as f: f.write(json.dumps(args)+'\\n')",
		"if '--help' in args: sys.exit(88)",
		"stage=''",
		"if 'ensure' in args: stage='workspace'",
		"elif 'login' in args: stage='login'",
		"elif 'setup' in args: stage='runtime_setup'",
		"elif 'restart' in args: stage='daemon_restart'",
		"elif 'rename' in args: stage='machine_rename'",
		"elif 'agent' in args: stage='agent'",
		"failed=bool(stage and (stage == os.environ.get('FIXTURE_FAILURE') or (stage == 'workspace' and os.environ.get('FIXTURE_FAILURE') == 'missing_feature')))",
		"saved=os.path.join(os.environ['HOME'],'fixture-login')",
		"if stage == 'login' and not failed: open(saved,'w').write(args[args.index('--token')+1])",
		"if stage in ('workspace','machine_rename','agent'):",
		" if not os.path.isfile(saved) or (os.environ.get('MULTICA_TOKEN') or open(saved).read()) != os.environ['FIXTURE_EXPECTED_TOKEN']: sys.exit(9)",
		"if stage in ('workspace','machine_rename','agent'):",
		" print(json.dumps({'success':not failed,'workspace_id':'11111111-1111-4111-8111-111111111111','completed_steps':[stage]}))",
		"else: print('ordinary CLI log')",
		"sys.exit(7 if failed else 0)",
	}, "\n") + "\n"
	files := map[string]string{
		"multica": fakeCLI,
		"ducc":    fakeCLI,
		"curl": strings.Join([]string{
			"#!/bin/sh",
			"case \"$*\" in *\"/api/tmesh/releases/latest\"*) exit 66;; esac",
			"if [ \"$FIXTURE_FAILURE\" = install ]; then exit 7; fi",
			"cat <<'INSTALL'",
			"#!/bin/sh",
			"printf '[\"installer-version\",\"%s\"]\\n' \"${MULTICA_VERSION-}\" >> \"$FIXTURE_CALLS\"",
			"INSTALL",
		}, "\n") + "\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0700); err != nil {
			t.Fatal(err)
		}
	}
	command := machineOnboardingCommand(machineCommandConfig{
		InstallURL: "https://example.test/install.sh", ServerURL: "https://api.example.test",
		AppURL: "https://app.example.test", Runtimes: "ducc,ducx", Shims: "ducc,ducx",
	}, "owner@example.test", "mul_fixture-token", input)
	if strings.Contains(command, "tmesh_finish") || strings.Contains(command, "tmesh_result_dir") || strings.Contains(command, "failed_step") || strings.Contains(command, "--help") || strings.Contains(command, "/api/tmesh/releases/latest") || strings.Contains(command, "MULTICA_VERSION") || strings.Contains(command, "--version") || strings.Contains(command, "MULTICA_TOKEN=") || strings.Contains(command, "unset MULTICA_TOKEN") || strings.Contains(command, "--set-default-if-unset") {
		t.Fatal("script contains an aggregate, version selector or compatibility precheck")
	}
	cmd := exec.Command("bash")
	if fail == "trace" {
		cmd = exec.Command("bash", "-x")
	}
	cmd.Stdin = strings.NewReader(command)
	version := ""
	if len(versions) > 0 {
		version = versions[0]
	}
	inherited, expected := "", "mul_fixture-token"
	if fail == "inherited_token" {
		inherited, expected = "mul_other-fixture", "mul_other-fixture"
	}
	cmd.Env = append(os.Environ(), "MULTICA_TOKEN="+inherited, "FIXTURE_EXPECTED_TOKEN="+expected, "MULTICA_VERSION="+version, "HOME="+dir, "MULTICA_BIN_DIR="+dir, "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "FIXTURE_CALLS="+logPath, "FIXTURE_FAILURE="+fail)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if strings.Contains(stdout.String(), "mul_fixture-token") || strings.Contains(stderr.String(), "mul_fixture-token") {
		t.Fatal("script output exposed a credential")
	}
	var calls [][]string
	data, _ := os.ReadFile(logPath)
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var args []string
		if line != "" && json.Unmarshal([]byte(line), &args) == nil {
			calls = append(calls, args)
		}
	}
	return stdout.String(), stderr.String(), calls, err
}

func TestMachineScriptSuppressesShellTraceOfCredentials(t *testing.T) {
	_, _, _, err := runMachineScriptFixture(t, "Fixture machine", "trace")
	if err != nil {
		t.Fatalf("traced invocation failed: %v", err)
	}
}
func TestMachineScriptStopsWhenRequiredCLICommandFails(t *testing.T) {
	_, _, calls, err := runMachineScriptFixture(t, "Fixture machine", "missing_feature")
	if err == nil || !scriptCallsStage(calls, "ensure") || scriptCallsStage(calls, "restart") || scriptCallsStage(calls, "agent") {
		t.Fatal("incompatible CLI must fail on the real command and stop subsequent operations")
	}
}
func TestMachineScriptPreservesInheritedToken(t *testing.T) {
	_, _, calls, err := runMachineScriptFixture(t, "Fixture machine", "inherited_token")
	if err != nil || !scriptCallsStage(calls, "ensure") || !scriptCallsStage(calls, "agent") {
		t.Fatalf("inherited environment was not preserved: %v", err)
	}
}
func scriptCallsStage(calls [][]string, stage string) bool {
	for _, args := range calls {
		helper := false
		for _, value := range args {
			if value == "--help" {
				helper = true
			}
		}
		if helper {
			continue
		}
		for _, value := range args {
			if value == stage {
				return true
			}
		}
	}
	return false
}
func TestMachineScriptStopsOnFailures(t *testing.T) {
	for _, fail := range []string{"install", "workspace", "login", "runtime_setup", "daemon_restart", "machine_rename", "agent"} {
		t.Run(fail, func(t *testing.T) {
			_, _, calls, err := runMachineScriptFixture(t, "Fixture machine", fail)
			if err == nil {
				t.Fatalf("failure did not stop: %s %v", fail, err)
			}
			if fail != "agent" && scriptCallsStage(calls, "agent") {
				t.Fatalf("agent ran after %s failed", fail)
			}
			if fail != "agent" && fail != "machine_rename" && scriptCallsStage(calls, "rename") {
				t.Fatalf("rename ran after %s failed", fail)
			}
			if fail == "install" && scriptCallsStage(calls, "ensure") {
				t.Fatalf("workspace ran after %s failed", fail)
			}
			if fail == "login" && scriptCallsStage(calls, "ensure") {
				t.Fatal("workspace preparation ran after login failed")
			}
		})
	}
}
func TestMachineScriptUsesLiteralNamesAndOptionalMachine(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "must-not-exist")
	name := "Fixture ' $(touch " + marker + ")"
	stdout, _, calls, err := runMachineScriptFixture(t, name, "")
	if err != nil {
		t.Fatalf("script failed: %v", err)
	}
	if json.Valid([]byte(stdout)) {
		t.Fatal("script still emits one aggregated JSON document")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("name was executed as shell code")
	}
	var rename, agent, model, release, workspace bool
	for _, args := range calls {
		if len(args) == 2 && args[0] == "installer-version" && args[1] == "" {
			release = true
		}
		if len(args) == 3 && args[0] == "config" && args[1] == "model" && args[2] == "Opus 4.8" {
			model = true
		}
		for i, value := range args {
			if value == "ensure" && scriptArgsContain(args, "tmesh-workspace") {
				workspace = true
			}
			if i+1 < len(args) && value == "--machine-name" && args[i+1] == name {
				rename = true
			}
			if i+1 < len(args) && value == "--agent-name" && args[i+1] == name {
				agent = true
			}
		}
	}
	if !rename || !agent || !model || !release || !workspace {
		t.Fatal("expected workspace, machine, agent, version or ducc model missing")
	}
	_, _, calls, err = runMachineScriptFixture(t, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if scriptCallsStage(calls, "rename") || scriptCallsStage(calls, "agent") || !scriptCallsStage(calls, "ensure") || !scriptCallsStage(calls, "restart") {
		t.Fatal("optional machine behavior changed")
	}
}
func scriptArgsContain(args []string, value string) bool {
	for _, arg := range args {
		if arg == value {
			return true
		}
	}
	return false
}

func TestMachineScriptHonorsManuallyProvidedVersion(t *testing.T) {
	_, _, calls, err := runMachineScriptFixture(t, "Fixture machine", "", "v0.6.1-overlay.10")
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range calls {
		if len(args) == 2 && args[0] == "installer-version" && args[1] == "v0.6.1-overlay.10" {
			return
		}
	}
	t.Fatal("the install script did not receive the caller's version")
}

func TestMachineScriptRetainsAddComputerCommands(t *testing.T) {
	cfg := machineCommandConfig{InstallURL: "https://example.test/install.sh", ServerURL: "https://api.example.test", AppURL: "https://app.example.test", Runtimes: "ducc,ducx", Shims: "ducc,ducx"}
	web := machineLinuxCommand(cfg, "owner@example.test", "mul_fixture-token")
	script := machineOnboardingCommand(cfg, "owner@example.test", "mul_fixture-token", machineProvisionInput{MachineName: "demo-machine"})
	for _, line := range strings.Split(web, "\n") {
		if line == "" || strings.HasPrefix(line, "#") || strings.Contains(line, " login --token ") {
			continue
		}
		if !strings.Contains(script, line) {
			t.Fatalf("add-computer command missing from onboarding: %s", line)
		}
	}
	for _, expected := range []string{"tmesh-workspace ensure --name 'pdc-loop'", "login --tmesh-noninteractive --token", "tmesh-machine rename --workspace-name 'pdc-loop' --machine-name 'demo-machine'", "tmesh-machine agent --workspace-name 'pdc-loop' --agent-name 'demo-machine' --runtime ducc"} {
		if !strings.Contains(script, expected) {
			t.Fatalf("missing required onboarding step %s", expected)
		}
	}
	login := strings.Index(script, "login --tmesh-noninteractive --token")
	ensure := strings.Index(script, "tmesh-workspace ensure --name")
	if login < 0 || ensure <= login {
		t.Fatal("login and workspace preparation are out of order")
	}
}

func TestMachineScriptUsesCustomWorkspaceLiterally(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "must-not-exist")
	workspace := "Research ' $(touch " + marker + ")"
	_, _, calls, err := runMachineScriptInputFixture(t, machineProvisionInput{MachineName: "demo", WorkspaceName: workspace}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("workspace name was executed as shell code")
	}
	var ensure, rename, agent bool
	for _, args := range calls {
		for i, value := range args {
			if i+1 >= len(args) || args[i+1] != workspace {
				continue
			}
			if value == "--name" && scriptArgsContain(args, "ensure") {
				ensure = true
			}
			if value == "--workspace-name" && scriptArgsContain(args, "rename") {
				rename = true
			}
			if value == "--workspace-name" && scriptArgsContain(args, "agent") {
				agent = true
			}
		}
	}
	if !ensure || !rename || !agent {
		t.Fatal("workspace name was not shared by all onboarding commands")
	}
}
