package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/spf13/cobra"
)

const machineWS = "11111111-1111-4111-8111-111111111111"
const machineUser = "22222222-2222-4222-8222-222222222222"
const machineDaemon = "33333333-3333-4333-8333-333333333333"
const machineRT = "44444444-4444-4444-8444-444444444444"
const machineAgent = "55555555-5555-4555-8555-555555555555"
const machineProfile = "66666666-6666-4666-8666-666666666666"

type machineFixture struct {
	client             *cli.APIClient
	current            map[string]any
	catalog            []map[string]any
	runtimes           []map[string]any
	profiles           []map[string]any
	owner              bool
	nonMember          bool
	failPut            bool
	changeDetail       bool
	changeInstructions bool
	modelCatalog       machineModelCatalog
	skills             []machineSkill
	agentSkills        []machineSkill
	failSkills         string
	calls              []string
	payloads           []map[string]any
}

func newMachineFixture(t *testing.T) *machineFixture {
	t.Helper()
	f := &machineFixture{owner: true,
		runtimes: []map[string]any{{"id": machineRT, "daemon_id": machineDaemon, "owner_id": machineUser, "workspace_id": machineWS,
			"runtime_mode": "local", "status": "online", "provider": "claude", "profile_id": machineProfile}},
		profiles: []map[string]any{{"id": machineProfile, "command_name": "ducc", "enabled": true}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userScoped := r.URL.Path == "/api/tmesh/agents/lock" || r.URL.Path == "/api/tmesh/workspaces/ensure"
		if r.Header.Get("Authorization") != "Bearer fixture-user-token" || (!userScoped && r.Header.Get("X-Workspace-ID") != machineWS) {
			t.Error("wrong account/workspace scope")
		}
		f.calls = append(f.calls, r.Method+" "+r.URL.RequestURI())
		if r.URL.Path == "/api/tmesh/agents/lock" {
			w.Header().Set("Content-Type", "application/x-ndjson")
			_, _ = w.Write([]byte("{\"locked\":true}\n"))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		var response any
		switch {
		case r.URL.Path == "/api/tmesh/workspaces/ensure":
			response = map[string]any{"workspace_id": machineWS, "workspace_name": "Fixture", "workspace_created": false}
		case r.URL.Path == "/api/me":
			response = map[string]any{"id": machineUser}
		case strings.HasSuffix(r.URL.Path, "/members"):
			role := "member"
			if f.owner {
				role = "owner"
			}
			response = []map[string]any{{"user_id": machineUser, "role": role}}
			if f.nonMember {
				response = []map[string]any{}
			}
		case r.URL.Path == "/api/runtimes":
			response = f.runtimes
		case r.URL.Path == "/api/workspaces/"+machineWS+"/runtime-profiles":
			response = map[string]any{"runtime_profiles": f.profiles}
		case strings.HasSuffix(r.URL.Path, "/models"):
			if f.modelCatalog.Status == "" {
				response = machineModelCatalog{Status: "completed"}
			} else {
				response = f.modelCatalog
			}
		case r.Method == "GET" && r.URL.Path == "/api/skills":
			if f.failSkills == "catalog" {
				http.Error(w, "fixture skill catalog failure", 503)
				return
			}
			response = f.skills
		case r.Method == "GET" && r.URL.Path == "/api/agents/"+machineAgent+"/skills":
			response = f.agentSkills
		case r.Method == "POST" && r.URL.Path == "/api/agents/"+machineAgent+"/skills/add":
			if f.failSkills == "add" {
				http.Error(w, "fixture skill add failure", 500)
				return
			}
			var body struct {
				SkillIDs []string `json:"skill_ids"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			for _, id := range body.SkillIDs {
				f.agentSkills = append(f.agentSkills, machineSkill{ID: id, Enabled: true})
			}
			response = f.agentSkills
		case r.Method == "PUT" && strings.Contains(r.URL.Path, "/skills/") && strings.HasSuffix(r.URL.Path, "/enabled"):
			if f.failSkills == "enable" {
				http.Error(w, "fixture skill enable failure", 500)
				return
			}
			var body struct {
				Enabled bool `json:"enabled"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			for i := range f.agentSkills {
				if strings.Contains(r.URL.Path, "/"+f.agentSkills[i].ID+"/") {
					f.agentSkills[i].Enabled = body.Enabled
				}
			}
			response = f.agentSkills
		case r.Method == "PATCH":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.payloads = append(f.payloads, body)
			response = map[string]any{"id": machineRT}
		case r.Method == "GET" && r.URL.Path == "/api/agents":
			response = f.catalog
		case r.Method == "GET" && r.URL.Path == "/api/agents/"+machineAgent:
			copy := map[string]any{}
			for key, value := range f.current {
				copy[key] = value
			}
			if f.changeDetail {
				copy["updated_at"] = "changed"
			}
			if f.changeInstructions {
				copy["instructions"] = "Changed by another editor"
			}
			response = copy
		case strings.HasSuffix(r.URL.Path, "/cancel-tasks"):
			response = map[string]any{"cancelled": 3}
		case strings.HasSuffix(r.URL.Path, "/restore"):
			response = f.current
		case r.Method == "POST" && r.URL.Path == "/api/agents", r.Method == "PUT" && r.URL.Path == "/api/agents/"+machineAgent:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.payloads = append(f.payloads, body)
			if f.failPut {
				http.Error(w, "fixture failure", 500)
				return
			}
			if f.current == nil {
				f.current = map[string]any{"id": machineAgent, "name": "My agent", "owner_id": machineUser, "workspace_id": machineWS}
			}
			for key, value := range body {
				f.current[key] = value
			}
			response = f.current
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.RequestURI())
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	t.Cleanup(server.Close)
	f.client = cli.NewAPIClient(server.URL, machineWS, "fixture-user-token")
	return f
}

func (f *machineFixture) existing() {
	f.current = map[string]any{"id": machineAgent, "name": "My agent", "owner_id": machineUser, "workspace_id": machineWS,
		"runtime_id": "77777777-7777-4777-8777-777777777777", "model": "claude-sonnet-4-6", "thinking_level": "high",
		"instructions": "keep instructions", "custom_env": map[string]any{"fixture": "keep"}, "updated_at": "same"}
	f.catalog = []map[string]any{f.current}
}
func machineDefaults() machineOptions {
	return machineOptions{AgentName: "My agent", Runtime: "ducc", Wait: time.Second}
}
func machineHasMutation(calls []string) bool {
	for _, call := range calls {
		if !strings.HasPrefix(call, "GET ") && !strings.HasSuffix(call, "/models") && !strings.HasSuffix(call, "/agents/lock") {
			return true
		}
	}
	return false
}

func TestMachineEnrollmentCreatesAndRenamesOnlyCurrentOwnedMachine(t *testing.T) {
	f := newMachineFixture(t)
	foreign := map[string]any{"id": machineAgent, "daemon_id": "other-machine", "owner_id": machineUser,
		"workspace_id": machineWS, "runtime_mode": "local", "status": "online", "provider": "claude", "profile_id": machineProfile}
	f.runtimes = append([]map[string]any{foreign}, f.runtimes...)
	options := machineDefaults()
	options.MachineName = "My computer"
	result, err := enrollMachine(context.Background(), f.client, machineDaemon, options)
	if err != nil {
		t.Fatal(err)
	}
	if result["agent_id"] != machineAgent || result["runtime_id"] != machineRT {
		t.Fatalf("wrong enrollment result: %v", result)
	}
	if f.payloads[0]["custom_name"] != "My computer" || f.payloads[0]["apply_to_machine"] != true {
		t.Fatal("machine rename did not use the shared machine-name API")
	}
	if f.payloads[1]["runtime_id"] != machineRT {
		t.Fatal("agent bound to another computer")
	}
	if f.payloads[1]["visibility"] != "workspace" {
		t.Fatal("new agent must be visible to the workspace")
	}
}

func TestMachineEnrollmentReusesIDHistoryAndCancelsBeforeRebinding(t *testing.T) {
	f := newMachineFixture(t)
	f.existing()
	result, err := enrollMachine(context.Background(), f.client, machineDaemon, machineDefaults())
	if err != nil {
		t.Fatal(err)
	}
	if result["agent_id"] != machineAgent || result["agent_created"] != false || result["cancelled_tasks"] != float64(3) {
		t.Fatalf("reuse result: %v", result)
	}
	joined := strings.Join(f.calls, "\n")
	cancelAt, putAt := strings.Index(joined, "/cancel-tasks"), strings.Index(joined, "PUT /api/agents/")
	if cancelAt < 0 || putAt < cancelAt {
		t.Fatal("must cancel queued/running tasks before rebinding")
	}
	for key := range f.payloads[0] {
		if key != "runtime_id" {
			t.Fatalf("unexpected configuration overwrite: %s", key)
		}
	}
	if f.current["instructions"] != "keep instructions" {
		t.Fatal("instructions changed")
	}
	f.calls = nil
	f.payloads = nil
	if _, err = enrollMachine(context.Background(), f.client, machineDaemon, machineDefaults()); err != nil {
		t.Fatal(err)
	}
	if machineHasMutation(f.calls) {
		t.Fatal("idempotent retry cancelled work or rewrote the agent")
	}
}

func TestMachineEnrollmentRestoresArchivedAgent(t *testing.T) {
	f := newMachineFixture(t)
	f.existing()
	f.current["archived_at"] = "2026-01-01T00:00:00Z"
	result, err := enrollMachine(context.Background(), f.client, machineDaemon, machineDefaults())
	if err != nil {
		t.Fatal(err)
	}
	if result["agent_restored"] != true || result["agent_id"] != machineAgent {
		t.Fatal("archived agent was not reused")
	}
}

func TestMachineEnrollmentRefusesForeignAgentOrWorkspaceAndConcurrentChanges(t *testing.T) {
	for _, kind := range []string{"agent-owner", "agent-workspace", "workspace-membership", "concurrent", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			f := newMachineFixture(t)
			f.existing()
			switch kind {
			case "agent-owner":
				f.current["owner_id"] = machineAgent
			case "agent-workspace":
				f.current["workspace_id"] = machineAgent
			case "workspace-membership":
				f.nonMember = true
			case "concurrent":
				f.changeDetail = true
			case "duplicate":
				f.catalog = append(f.catalog, f.current)
			}
			if _, err := enrollMachine(context.Background(), f.client, machineDaemon, machineDefaults()); err == nil {
				t.Fatal("unsafe enrollment accepted")
			}
			if machineHasMutation(f.calls) {
				t.Fatal("resources mutated before validation completed")
			}
		})
	}
}

func TestMachineEnrollmentDoesNotFallBackToAnotherRuntime(t *testing.T) {
	f := newMachineFixture(t)
	options := machineDefaults()
	options.Runtime = "ducx"
	options.Wait = 100 * time.Millisecond
	if _, err := enrollMachine(context.Background(), f.client, machineDaemon, options); err == nil || !strings.Contains(err.Error(), "no alternative") {
		t.Fatalf("missing runtime: %v", err)
	}
	if machineHasMutation(f.calls) {
		t.Fatal("missing runtime mutated resources")
	}
}

func TestMachineEnrollmentKeepsCompletedStepsOnFailure(t *testing.T) {
	f := newMachineFixture(t)
	f.failPut = true
	options := machineDefaults()
	options.MachineName = "Rename completed"
	result, err := enrollMachine(context.Background(), f.client, machineDaemon, options)
	if err == nil || result["machine_name"] != "Rename completed" {
		t.Fatal("partial completion was not retained")
	}
	for _, call := range f.calls {
		if strings.HasPrefix(call, "DELETE ") {
			t.Fatal("must not delete prior work on failure")
		}
	}
}

func TestMachineEnrollmentAllowsMembersToManageOwnedResources(t *testing.T) {
	f := newMachineFixture(t)
	f.owner = false
	if _, err := enrollMachine(context.Background(), f.client, machineDaemon, machineDefaults()); err != nil {
		t.Fatal(err)
	}
}

func TestMachineEnrollmentWithoutNamesOnlyReportsCurrentMachine(t *testing.T) {
	f := newMachineFixture(t)
	result, err := enrollMachine(context.Background(), f.client, machineDaemon, machineOptions{Wait: time.Second})
	if err != nil || result["runtime_id"] != machineRT || machineHasMutation(f.calls) {
		t.Fatalf("registration report changed resources: %v %v", result, err)
	}
}

func TestMachineResultIncludesPartialProgressAndRedactsCredentials(t *testing.T) {
	var output bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&output)
	result := map[string]any{"daemon_id": machineDaemon, "agent_id": machineAgent, "completed_steps": []string{"machine_renamed"}}
	err := machinePrintResult(cmd, result, errors.New("failed with fixture-user-token"), "fixture-user-token")
	var decoded map[string]any
	if err == nil || json.Unmarshal(output.Bytes(), &decoded) != nil || decoded["success"] != false || decoded["agent_id"] != machineAgent {
		t.Fatal("missing structured failure and retained IDs")
	}
	if strings.Contains(output.String()+err.Error(), "fixture-user-token") {
		t.Fatal("credential exposed in result or error")
	}
}

func TestMachineNamespaceDoesNotShadowUpstreamCommands(t *testing.T) {
	cmd := newMachineCommand()
	if cmd.Name() != "tmesh-machine" || len(cmd.Aliases) != 0 {
		t.Fatal("Team Mesh commands must use their dedicated namespace")
	}
}

func TestMachineMissingConfigurationStillEmitsFailureJSON(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, key := range []string{"MULTICA_SERVER_URL", "MULTICA_AGENT_ID", "MULTICA_TASK_ID", "MULTICA_DAEMON_PORT", cli.TaskConfigRootEnv} {
		t.Setenv(key, "")
	}
	for _, args := range [][]string{{"rename", "--machine-name", "Fixture"}, {"rename", "--unknown"}, {"rename", "unexpected"}} {
		var output bytes.Buffer
		cmd := newMachineCommand()
		cmd.PersistentFlags().String("profile", "fixture", "")
		cmd.PersistentFlags().String("server-url", "", "")
		cmd.PersistentFlags().String("workspace-id", machineWS, "")
		cmd.SetOut(&output)
		cmd.SetArgs(args)
		err := cmd.Execute()
		var result map[string]any
		if err == nil || json.Unmarshal(output.Bytes(), &result) != nil || result["success"] != false {
			t.Fatalf("missing configuration/invalid arguments bypassed JSON error: %v %s", err, output.String())
		}
	}
}

func TestMachineAgentConfigurationCompatibilityAndExplicitOverrides(t *testing.T) {
	runtime := map[string]any{"id": machineRT, "provider": "claude"}
	existing := map[string]any{"model": "gpt-6-sol", "thinking_level": "invalid effort", "service_tier": "priority"}
	body, err := machineAgentPayload(existing, runtime, machineDefaults())
	if err != nil {
		t.Fatal(err)
	}
	if body["model"] != "" || body["thinking_level"] != "" || body["service_tier"] != "" {
		t.Fatalf("incompatible fields were retained: %v", body)
	}
	model, thinking := "claude-sonnet-4-6", "high"
	options := machineDefaults()
	options.Model = &model
	options.Thinking = &thinking
	body, err = machineAgentPayload(existing, runtime, options)
	if err != nil {
		t.Fatal(err)
	}
	if body["model"] != model || body["thinking_level"] != thinking {
		t.Fatal("explicit overrides not used")
	}
	thinking = "invalid-effort"
	if _, err = machineAgentPayload(existing, runtime, options); err == nil {
		t.Fatal("invalid thinking accepted")
	}
}

func TestMachineInvalidOverridesFailBeforeCancellingTasks(t *testing.T) {
	f := newMachineFixture(t)
	f.existing()
	options := machineDefaults()
	value := "invalid-thinking"
	options.Thinking = &value
	if _, err := enrollMachine(context.Background(), f.client, machineDaemon, options); err == nil {
		t.Fatal("invalid override accepted")
	}
	if machineHasMutation(f.calls) {
		t.Fatal("tasks cancelled for an invalid override")
	}
}

func TestMachineDynamicModelCatalogProtectsExistingTasks(t *testing.T) {
	for _, kind := range []string{"implicit-reset", "explicit-rejected", "temporary-query-failure"} {
		t.Run(kind, func(t *testing.T) {
			f := newMachineFixture(t)
			f.existing()
			f.runtimes[0]["provider"] = "codex"
			f.profiles[0]["command_name"] = "ducx"
			f.current["model"] = "gpt-6-sol"
			f.current["thinking_level"] = "max"
			f.modelCatalog = machineModelCatalog{Status: "completed", Supported: true, Models: []agent.Model{{ID: "gpt-6-sol",
				Thinking: &agent.ModelThinking{SupportedLevels: []agent.ThinkingLevel{{Value: "high"}}}}}}
			options := machineDefaults()
			options.Runtime = "ducx"
			if kind == "explicit-rejected" {
				value := "max"
				options.Thinking = &value
			}
			if kind == "temporary-query-failure" {
				f.modelCatalog.Status = "failed"
			}
			_, err := enrollMachine(context.Background(), f.client, machineDaemon, options)
			if kind == "implicit-reset" {
				if err != nil {
					t.Fatal(err)
				}
				if f.current["model"] != "gpt-6-sol" || f.current["thinking_level"] != "" {
					t.Fatal("dynamic compatibility was not applied")
				}
			} else {
				if err == nil {
					t.Fatal("invalid/unknown compatibility accepted")
				}
				if machineHasMutation(f.calls) {
					t.Fatal("existing tasks/configuration changed before compatibility was confirmed")
				}
			}
		})
	}
}

func TestMachinePreparePreservesExistingWorkspaceAndUnknownConfiguration(t *testing.T) {
	for _, existing := range []string{"", machineAgent} {
		t.Run(existing, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("MULTICA_AGENT_ID", "")
			t.Setenv("MULTICA_TASK_ID", "")
			t.Setenv("MULTICA_DAEMON_PORT", "")
			t.Setenv(cli.TaskConfigRootEnv, "")
			path, err := cli.CLIConfigPathForProfile("fixture")
			if err != nil {
				t.Fatal(err)
			}
			if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			content := map[string]any{"workspace_id": existing, "token": "fixture-user-token", "custom_setting": "preserved"}
			data, _ := json.Marshal(content)
			if err = os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			command := newMachineCommand()
			command.PersistentFlags().String("profile", "fixture", "")
			command.PersistentFlags().String("workspace-id", machineWS, "")
			command.SetArgs([]string{"prepare", "--workspace-id", machineWS})
			if err = command.Execute(); err != nil {
				t.Fatal(err)
			}
			data, err = os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var saved map[string]any
			_ = json.Unmarshal(data, &saved)
			want := existing
			if want == "" {
				want = machineWS
			}
			if saved["workspace_id"] != want || saved["token"] != "fixture-user-token" || saved["custom_setting"] != "preserved" {
				t.Fatalf("config not preserved: %v", saved)
			}
		})
	}
}
