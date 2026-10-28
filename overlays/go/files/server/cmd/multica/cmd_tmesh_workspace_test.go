package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/daemon"
	"github.com/spf13/cobra"
)

func TestTmeshWorkspaceEnsureUsesExactNameAndUserScope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/tmesh/workspaces/ensure" || r.Header.Get("X-Workspace-ID") != "" || r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Error("wrong workspace ensure request")
		}
		var input map[string]string
		_ = json.NewDecoder(r.Body).Decode(&input)
		if input["name"] != "Team Name" {
			t.Errorf("name = %q", input["name"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"workspace_id": machineWS, "workspace_created": true})
	}))
	defer server.Close()
	client := cli.NewAPIClient(server.URL, machineWS, "fixture-token")
	result, err := machineEnsureWorkspace(context.Background(), client, "Team Name")
	if err != nil || result["workspace_id"] != machineWS || client.WorkspaceID != machineWS {
		t.Fatalf("unexpected result: %v %v", result, err)
	}
}

func TestTmeshMachineExposesIndependentCommands(t *testing.T) {
	cmd := newMachineCommand()
	found := map[string]bool{}
	for _, sub := range cmd.Commands() {
		found[sub.Name()] = true
		if sub.Name() == "agent" || sub.Name() == "rename" {
			if sub.Flags().Lookup("workspace-name") == nil {
				t.Fatal("missing workspace name flag")
			}
		}
	}
	if !found["agent"] || !found["rename"] || found["enroll"] {
		t.Fatalf("unexpected machine commands: %v", found)
	}
}

func TestTmeshAgentUsesWorkspaceNameWithoutChangingDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MULTICA_TOKEN", "fixture-user-token")
	for _, key := range []string{"MULTICA_WORKSPACE_ID", "MULTICA_SERVER_URL", "MULTICA_AGENT_ID", "MULTICA_TASK_ID", "MULTICA_DAEMON_PORT"} {
		t.Setenv(key, "")
	}
	f := newMachineFixture(t)
	oldWorkspace := "77777777-7777-4777-8777-777777777777"
	if err := cli.SaveCLIConfigForProfile(cli.CLIConfig{ServerURL: f.client.BaseURL, WorkspaceID: oldWorkspace}, "fixture"); err != nil {
		t.Fatal(err)
	}
	id, err := daemon.EnsureDaemonID("fixture")
	if err != nil {
		t.Fatal(err)
	}
	f.runtimes[0]["daemon_id"] = id
	root := &cobra.Command{Use: "multica", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().String("profile", "fixture", "")
	root.PersistentFlags().String("server-url", "", "")
	root.PersistentFlags().String("workspace-id", "", "")
	root.AddCommand(newMachineCommand())
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"tmesh-machine", "agent", "--workspace-name", "Fixture", "--agent-name", "My agent", "--runtime", "ducc"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if json.Unmarshal(out.Bytes(), &result) != nil || result["success"] != true || result["workspace_id"] != machineWS {
		t.Fatalf("workspace name was not used: %s", out.String())
	}
	cfg, err := cli.LoadCLIConfigForProfile("fixture")
	if err != nil || cfg.WorkspaceID != oldWorkspace {
		t.Fatal("profile default changed")
	}
}

func TestTmeshNoninteractiveLoginPreservesProfileWorkspace(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, key := range []string{"MULTICA_TOKEN", "MULTICA_SERVER_URL", "MULTICA_AGENT_ID", "MULTICA_TASK_ID", "MULTICA_DAEMON_PORT"} {
		t.Setenv(key, "")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/me" {
			t.Errorf("unexpected interactive discovery: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"name": "Fixture", "email": "fixture@example.test"})
	}))
	defer server.Close()
	if err := cli.SaveCLIConfigForProfile(cli.CLIConfig{ServerURL: server.URL, WorkspaceID: machineWS}, "fixture"); err != nil {
		t.Fatal(err)
	}
	cmd := &cobra.Command{}
	cmd.Flags().String("profile", "fixture", "")
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("token", "", "")
	cmd.Flags().Bool("tmesh-noninteractive", true, "")
	if err := cmd.Flags().Set("token", "mul_fixture-token"); err != nil {
		t.Fatal(err)
	}
	if err := runLogin(cmd, nil); err != nil {
		t.Fatal(err)
	}
	cfg, err := cli.LoadCLIConfigForProfile("fixture")
	if err != nil || cfg.WorkspaceID != machineWS || cfg.Token != "mul_fixture-token" {
		t.Fatalf("profile workspace not preserved: %v", err)
	}
}

func TestTmeshAgentLockRejectsParallelClientAndReleases(t *testing.T) {
	occupied := make(chan struct{}, 1)
	released := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case occupied <- struct{}{}:
			_, _ = w.Write([]byte("{\"locked\":true}\n"))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			<-occupied
			released <- struct{}{}
		default:
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte("{\"error\":\"another machine is preparing this agent\"}"))
		}
	}))
	defer server.Close()
	client := cli.NewAPIClient(server.URL, machineWS, "fixture-token")
	_, release, err := machineAcquireAgentLock(context.Background(), client, "Fixture")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = machineAcquireAgentLock(context.Background(), client, "Fixture")
	if err == nil || !strings.Contains(err.Error(), "another machine") {
		t.Fatalf("parallel operation was not rejected: %v", err)
	}
	release()
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("lock not released")
	}
}

func TestTmeshAgentLockLossCancelsClientContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{\"locked\":true}\n"))
		w.(http.Flusher).Flush()
	}))
	defer server.Close()
	client := cli.NewAPIClient(server.URL, machineWS, "fixture-token")
	ctx, release, err := machineAcquireAgentLock(context.Background(), client, "Fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("lost lock did not cancel operation")
	}
}
