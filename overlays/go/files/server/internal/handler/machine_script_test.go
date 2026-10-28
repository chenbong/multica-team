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

func runMachineScriptFixture(t *testing.T, machine, fail string) (map[string]any, string, [][]string, error) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.jsonl")
	fakeCLI := strings.Join([]string{
		"#!/usr/bin/env python3",
		"import json,os,sys",
		"args=sys.argv[1:]",
		"with open(os.environ['FIXTURE_CALLS'],'a') as f: f.write(json.dumps(args)+'\\n')",
		"if '--help' in args:",
		" if os.environ.get('FIXTURE_FAILURE') == 'missing_feature': print('--workspace-name --tmesh-noninteractive'); sys.exit(0)",
		" print('--set-default-if-unset --workspace-name --tmesh-noninteractive'); sys.exit(0)",
		"stage=''",
		"if 'ensure' in args: stage='workspace'",
		"elif 'login' in args: stage='login'",
		"elif 'setup' in args: stage='runtime_setup'",
		"elif 'restart' in args: stage='daemon_restart'",
		"elif 'rename' in args: stage='machine_rename'",
		"elif 'agent' in args: stage='agent'",
		"failed=bool(stage and stage == os.environ.get('FIXTURE_FAILURE'))",
		"if stage in ('workspace','machine_rename','agent'):",
		" print(json.dumps({'success':not failed,'workspace_id':'11111111-1111-4111-8111-111111111111','completed_steps':[stage]}))",
		"else: print('ordinary CLI log')",
		"sys.exit(7 if failed else 0)",
	}, "\n") + "\n"
	files := map[string]string{
		"multica": fakeCLI,
		"ducc":    fakeCLI,
		"curl":    "#!/bin/sh\nif [ \"$FIXTURE_FAILURE\" = install ]; then exit 7; fi\nprintf 'echo installer-log\\n'\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0700); err != nil {
			t.Fatal(err)
		}
	}
	command := machineOnboardingCommand(machineCommandConfig{
		InstallURL: "https://example.test/install.sh", ServerURL: "https://api.example.test",
		AppURL: "https://app.example.test", Runtimes: "ducc,ducx", Shims: "ducc,ducx",
	}, "owner@example.test", "mul_fixture-token", "owner", machineProvisionInput{MachineName: machine})
	cmd := exec.Command("bash")
	cmd.Stdin = strings.NewReader(command)
	cmd.Env = append(os.Environ(), "HOME="+dir, "MULTICA_BIN_DIR="+dir, "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "FIXTURE_CALLS="+logPath, "FIXTURE_FAILURE="+fail)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var result map[string]any
	if json.Unmarshal(stdout.Bytes(), &result) != nil || strings.Contains(stdout.String(), "mul_fixture-token") || strings.Contains(stderr.String(), "mul_fixture-token") {
		t.Fatalf("unsafe or invalid output: %q", stdout.String())
	}
	var calls [][]string
	data, _ := os.ReadFile(logPath)
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var args []string
		if line != "" && json.Unmarshal([]byte(line), &args) == nil {
			calls = append(calls, args)
		}
	}
	return result, stderr.String(), calls, err
}

func TestMachineScriptRejectsAnOldCLIWithClearError(t *testing.T) {
	result, stderr, calls, err := runMachineScriptFixture(t, "Fixture machine", "missing_feature")
	if err == nil || result["failed_step"] != "install" || !strings.Contains(stderr, "does not support Team Mesh workspace preparation") {
		t.Fatalf("old CLI was not clearly rejected: %v %v %s", result, err, stderr)
	}
	for _, args := range calls {
		for _, value := range args {
			if value == "--set-default-if-unset" {
				t.Fatal("workspace preparation ran with an old CLI")
			}
		}
	}
}

func TestMachineScriptStagesAndPartialFailures(t *testing.T) {
	for _, fail := range []string{"", "install", "workspace", "login", "runtime_setup", "daemon_restart", "machine_rename", "agent"} {
		t.Run(fail, func(t *testing.T) {
			result, stderr, _, err := runMachineScriptFixture(t, "Fixture machine", fail)
			if (fail == "") != (err == nil) || result["success"] != (fail == "") {
				t.Fatalf("unexpected result: %v %v %s", result, err, stderr)
			}
			if fail != "" && result["failed_step"] != fail {
				t.Fatalf("wrong failed step: %v", result)
			}
			if fail == "agent" {
				machine, _ := result["machine"].(map[string]any)
				if machine["success"] != true {
					t.Fatal("completed machine rename was lost")
				}
			}
		})
	}
}

func TestMachineScriptUsesLiteralNamesAndOptionalMachine(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "must-not-exist")
	name := "Fixture ' $(touch " + marker + ")"
	result, _, calls, err := runMachineScriptFixture(t, name, "")
	if err != nil || result["success"] != true {
		t.Fatalf("script failed: %v %v", err, result)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("name was executed as shell code")
	}
	var rename, agent, model bool
	for _, args := range calls {
		if len(args) == 3 && args[0] == "config" && args[1] == "model" && args[2] == "Opus 4.8" {
			model = true
		}
		for i, value := range args {
			if i+1 < len(args) && value == "--machine-name" && args[i+1] == name {
				rename = true
			}
			if i+1 < len(args) && value == "--agent-name" && args[i+1] == name {
				agent = true
			}
		}
	}
	if !rename || !agent || !model {
		t.Fatal("machine name or agreed ducc model was not passed correctly")
	}
	_, _, calls, err = runMachineScriptFixture(t, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range calls {
		for _, value := range args {
			if value == "--machine-name" || value == "--agent-name" {
				t.Fatal("optional machine unexpectedly triggered enrollment")
			}
		}
	}
}
