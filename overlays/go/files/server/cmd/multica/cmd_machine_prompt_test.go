package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMachineSystemPromptInputs(t *testing.T) {
	dir := t.TempDir()
	prompt := "你是开发助手。\nKeep $HOME, `code`, quotes \" and spaces.\r\n\n"
	file := filepath.Join(dir, "prompt.md")
	if err := os.WriteFile(file, []byte(prompt), 0600); err != nil {
		t.Fatal(err)
	}
	emptyFile := filepath.Join(dir, "empty.md")
	if err := os.WriteFile(emptyFile, nil, 0600); err != nil {
		t.Fatal(err)
	}
	badFile := filepath.Join(dir, "invalid.md")
	if err := os.WriteFile(badFile, []byte{0xff}, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		want *string
		err  string
	}{
		{"omitted", nil, nil, ""},
		{"inline", []string{"--system-prompt", prompt}, &prompt, ""},
		{"file", []string{"--system-prompt-file", file}, &prompt, ""},
		{"explicit clear", []string{"--system-prompt="}, machinePromptPtr(""), ""},
		{"empty file clears", []string{"--system-prompt-file", emptyFile}, machinePromptPtr(""), ""},
		{"exclusive", []string{"--system-prompt=", "--system-prompt-file", file}, nil, "either"},
		{"missing file", []string{"--system-prompt-file", filepath.Join(dir, "missing.md")}, nil, "read --system-prompt-file"},
		{"empty path", []string{"--system-prompt-file="}, nil, "requires a file path"},
		{"directory", []string{"--system-prompt-file", dir}, nil, "read --system-prompt-file"},
		{"invalid UTF-8", []string{"--system-prompt-file", badFile}, nil, "valid UTF-8"},
		{"NUL byte", []string{"--system-prompt", "before\x00after"}, nil, "NUL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := newMachineCommand()
			cmd, _, _ := root.Find([]string{"agent"})
			if err := cmd.ParseFlags(tc.args); err != nil {
				t.Fatal(err)
			}
			got, err := machineReadSystemPrompt(cmd)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("got %v", err)
				}
				return
			}
			if err != nil || (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
				t.Fatalf("prompt content/presence changed: %v", err)
			}
		})
	}
}

func TestMachineSystemPromptCreateUpdatePreserveAndClear(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "create", true: "reuse"}[existing], func(t *testing.T) {
			f := newMachineFixture(t)
			if existing {
				f.existing()
				f.current["runtime_id"] = machineRT
			}
			prompt := "你是测试助手。\n  Keep exact whitespace.\n"
			options := machineDefaults()
			options.SystemPrompt = &prompt
			result, err := enrollMachine(t.Context(), f.client, machineDaemon, options)
			if err != nil || f.current["instructions"] != prompt {
				t.Fatalf("prompt not saved: %v", err)
			}
			f.catalog = []map[string]any{f.current}
			var output bytes.Buffer
			cmd := newMachineCommand()
			cmd.SetOut(&output)
			if err := machinePrintResult(cmd, result, nil, ""); err != nil {
				t.Fatal(err)
			}
			var response map[string]any
			if json.Unmarshal(output.Bytes(), &response) != nil || response["instructions"] != nil || strings.Contains(output.String(), "Keep exact whitespace") {
				t.Fatal("prompt appeared in command output")
			}
			// A second call without the option must not clear instructions.
			if _, err := enrollMachine(t.Context(), f.client, machineDaemon, machineDefaults()); err != nil {
				t.Fatal(err)
			}
			if f.current["instructions"] != prompt {
				t.Fatal("omitted prompt cleared instructions")
			}
			if _, err := enrollMachine(t.Context(), f.client, machineDaemon, options); err != nil {
				t.Fatal(err)
			}
			if len(f.payloads) != 1 {
				t.Fatal("unchanged instructions caused another write")
			}
			options.SystemPrompt = machinePromptPtr("")
			if _, err := enrollMachine(t.Context(), f.client, machineDaemon, options); err != nil {
				t.Fatal(err)
			}
			if f.current["instructions"] != "" {
				t.Fatal("explicit empty prompt did not clear")
			}
			if strings.Contains(strings.Join(f.calls, "\n"), "/cancel-tasks") {
				t.Fatal("instructions-only update cancelled tasks")
			}
		})
	}
}

func TestMachineSystemPromptRebindStillCancelsTasks(t *testing.T) {
	f := newMachineFixture(t)
	f.existing()
	options := machineDefaults()
	options.SystemPrompt = machinePromptPtr("New instructions")
	if _, err := enrollMachine(t.Context(), f.client, machineDaemon, options); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(f.calls, "\n")
	if at := strings.Index(joined, "/cancel-tasks"); at < 0 || strings.Index(joined, "PUT /api/agents/") < at {
		t.Fatal("rebinding must still cancel tasks before updating")
	}
	if f.current["custom_env"] == nil || f.current["instructions"] != "New instructions" {
		t.Fatal("other settings lost or prompt missing")
	}
}

func TestMachineSystemPromptRejectsConcurrentEdit(t *testing.T) {
	f := newMachineFixture(t)
	f.existing()
	f.current["runtime_id"] = machineRT
	f.changeInstructions = true
	options := machineDefaults()
	options.SystemPrompt = machinePromptPtr("New instructions")
	if _, err := enrollMachine(t.Context(), f.client, machineDaemon, options); err == nil || !strings.Contains(err.Error(), "instructions changed") {
		t.Fatalf("concurrent edit not detected: %v", err)
	}
	if machineHasMutation(f.calls) {
		t.Fatal("concurrent edit caused a write or cancellation")
	}
}

func TestMachineSystemPromptValidationBeforeHTTP(t *testing.T) {
	f := newMachineFixture(t)
	options := machineDefaults()
	options.SystemPrompt = machinePromptPtr("bad\x00prompt")
	if _, err := enrollMachine(t.Context(), f.client, machineDaemon, options); err == nil || len(f.calls) != 0 {
		t.Fatal("invalid prompt reached the API")
	}
	root := newMachineCommand()
	rename, _, _ := root.Find([]string{"rename"})
	if rename.Flags().Lookup("system-prompt") != nil || rename.Flags().Lookup("system-prompt-file") != nil {
		t.Fatal("prompt flags should only apply to agent")
	}
}

func TestMachineSystemPromptFileErrorStopsBeforeWorkspacePreparation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, key := range []string{"MULTICA_AGENT_ID", "MULTICA_TASK_ID", "MULTICA_DAEMON_PORT"} {
		t.Setenv(key, "")
	}
	f := newMachineFixture(t)
	cmd := newMachineCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"agent", "--workspace-name", "Fixture", "--agent-name", "My agent", "--system-prompt-file", filepath.Join(t.TempDir(), "missing.md")})
	err := cmd.Execute()
	var result map[string]any
	if err == nil || !strings.Contains(err.Error(), "read --system-prompt-file") || len(f.calls) != 0 || json.Unmarshal(output.Bytes(), &result) != nil || result["success"] != false {
		t.Fatalf("file validation did not stop early: %v %s", err, output.String())
	}
}

func machinePromptPtr(value string) *string { return &value }
