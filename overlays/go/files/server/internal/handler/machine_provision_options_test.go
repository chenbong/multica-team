package handler

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func machineOptionBody(t *testing.T, username string, values map[string]any) string {
	t.Helper()
	body := map[string]any{"username": username, "machine_name": "fixture-machine"}
	for key, value := range values {
		body[key] = value
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestMachineProvisionAcceptsAgentOptionsAndLongPrompts(t *testing.T) {
	username := machineTestName()
	h := machineTestConfig(t, username)
	prompt := strings.Repeat("中文提示词\n", 1200) + "'\" $HOME __CLI__ __AGENT_OPTIONS__\n\n"
	body := machineOptionBody(t, username, map[string]any{"skill_list": []string{" search ", "report", "search"}, "system_prompt": prompt})
	response := machineCall(t, h, body, provisionServiceToken)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "--skill-list 'search,report'") || !strings.Contains(response.Body.String(), "--system-prompt "+machineShellQuote(prompt)) {
		t.Fatalf("options not accepted: status %d", response.Code)
	}
	if !strings.Contains(response.Header().Get("Cache-Control"), "no-store") {
		t.Fatal("response may be cached")
	}
	retry := machineCall(t, h, body, provisionServiceToken)
	if retry.Code != 200 || retry.Body.String() != response.Body.String() {
		t.Fatal("repeat did not preserve credentials and options")
	}
	clear := machineCall(t, h, machineOptionBody(t, username, map[string]any{"system_prompt": ""}), provisionServiceToken)
	if clear.Code != 200 || !strings.Contains(clear.Body.String(), "--system-prompt ''") {
		t.Fatal("explicit empty prompt was lost")
	}
	baseline := machineCall(t, h, machineBody(username), provisionServiceToken)
	nullOptions := machineCall(t, h, `{"username":"`+username+`","skill_list":null,"system_prompt":null}`, provisionServiceToken)
	if baseline.Code != 200 || nullOptions.Code != 200 || baseline.Body.String() != nullOptions.Body.String() || strings.Contains(baseline.Body.String(), "--skill-list") || strings.Contains(baseline.Body.String(), "--system-prompt") {
		t.Fatal("omitted/null fields changed old behavior")
	}
}

func TestMachineProvisionRejectsInvalidAgentOptionsBeforeAccountChanges(t *testing.T) {
	username := machineTestName()
	h := machineTestConfig(t, username)
	cases := []map[string]any{
		{"machine_name": "", "skill_list": []string{"search"}},
		{"machine_name": "", "system_prompt": ""},
		{"skill_list": []string{}}, {"skill_list": []string{" "}}, {"skill_list": []string{"bad\nname"}},
		{"skill_list": []string{strings.Repeat("x", 129)}}, {"skill_list": make([]string, 101)},
		{"skill_list": "search,report"}, {"skill_list": []any{true}},
		{"system_prompt": true}, {"system_prompt": "before\x00after"},
		{"system_prompt": strings.Repeat("p", maxMachineSystemPromptBytes+1)},
		{"skill-list": []string{"search"}}, {"system-promt": "wrong field"},
	}
	for i, values := range cases {
		response := machineCall(t, h, machineOptionBody(t, username, values), provisionServiceToken)
		if response.Code != 400 {
			t.Fatalf("case %d: got status %d", i, response.Code)
		}
		if strings.Contains(response.Body.String(), "mul_") {
			t.Fatal("validation exposed credentials")
		}
	}
	for _, body := range []string{"{\"username\":\"" + username + "\",\"system_prompt\":\"\xff\"}", machineBody(username) + strings.Repeat(" ", maxMachineProvisionBodyBytes)} {
		if response := machineCall(t, h, body, provisionServiceToken); response.Code != 400 {
			t.Fatalf("invalid raw input status %d", response.Code)
		}
	}
	if _, err := h.Queries.GetUserByEmail(context.Background(), username+"@example.test"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("invalid options created an account")
	}
}

func TestMachineProvisionPromptSizeAndSkillNormalization(t *testing.T) {
	prompt := strings.Repeat("p", maxMachineSystemPromptBytes)
	input := machineProvisionInput{MachineName: "fixture", SkillList: []string{" a ", "b", "a"}, SystemPrompt: &prompt}
	if err := input.validate(); err != nil || !reflect.DeepEqual(input.SkillList, []string{"a", "b"}) || *input.SystemPrompt != prompt {
		t.Fatalf("valid boundary input changed: %v", err)
	}
}

func TestMachineScriptAgentOptionsStayLiteralAndOnlyReachAgent(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "must-not-exist")
	prompt := "中文\n'\" $(touch " + marker + ") `touch " + marker + "` $HOME __CLI__ __WORKSPACE__ __AGENT_OPTIONS__\r\n\n"
	skills := []string{"search", "comma,name", "quoted'skill", "double\"quote", "__CLI__"}
	input := machineProvisionInput{MachineName: "fixture", SkillList: skills, SystemPrompt: &prompt}
	if err := input.validate(); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, calls, err := runMachineScriptInputFixture(t, input, "trace")
	if err != nil {
		t.Fatalf("generated script failed: %v", err)
	}
	if strings.Contains(stdout, "__AGENT_OPTIONS__") || strings.Contains(stderr, "__AGENT_OPTIONS__") {
		t.Fatal("prompt text appeared in output")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("prompt was executed as shell code")
	}
	found := false
	for _, args := range calls {
		isAgent := scriptArgsContain(args, "tmesh-machine") && scriptArgsContain(args, "agent")
		for i, value := range args {
			if value != "--skill-list" && value != "--system-prompt" {
				continue
			}
			if !isAgent || i+1 >= len(args) {
				t.Fatal("agent options leaked to another command")
			}
			if value == "--system-prompt" && args[i+1] != prompt {
				t.Fatal("prompt whitespace or placeholder text changed")
			}
			if value == "--skill-list" {
				got, err := csv.NewReader(strings.NewReader(args[i+1])).Read()
				if err != nil || !reflect.DeepEqual(got, skills) {
					t.Fatal("skill CSV did not round-trip")
				}
				found = true
			}
		}
	}
	if !found {
		t.Fatal("options missing from agent command")
	}
}
