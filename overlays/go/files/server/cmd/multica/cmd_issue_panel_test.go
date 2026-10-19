package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIssuePanelCLIUsesOnlyCurrentRunAndPreservesURL(t *testing.T) {
	t.Chdir(t.TempDir())
	const token = "mat_fixture_only"
	const href = "https://example.test/report?a=1&b=%20#/results?view=team"
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/api/agent/issue-web-panels" || r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("wrong current-run request")
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if len(payload) != 2 || payload["name"] != "性能看板" || payload["url"] != href {
			t.Errorf("unexpected payload: %+v", payload)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "panel", "name": "性能看板", "url": href, "already_exists": calls > 1})
	}))
	defer srv.Close()
	t.Setenv("MULTICA_SERVER_URL", srv.URL)
	t.Setenv("MULTICA_WORKSPACE_ID", "workspace-fixture")
	t.Setenv("MULTICA_TASK_CONFIG_ROOT", t.TempDir())
	t.Setenv("MULTICA_AGENT_ID", "agent-fixture")
	t.Setenv("MULTICA_TASK_ID", "run-fixture")
	t.Setenv("MULTICA_TOKEN", token)
	for _, duplicate := range []bool{false, true} {
		cmd := newIssuePanelCommand()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs([]string{"add", "--name", "性能看板", "--url", href})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		var result map[string]any
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result["already_exists"] != duplicate || strings.Contains(out.String(), token) {
			t.Fatal("duplicate flag or credential redaction failed")
		}
	}
}

func TestIssuePanelCLIRejectsTargetOverrideAndWrongCredentials(t *testing.T) {
	t.Chdir(t.TempDir())
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(403) }))
	defer srv.Close()
	t.Setenv("MULTICA_SERVER_URL", srv.URL)
	t.Setenv("MULTICA_TASK_CONFIG_ROOT", t.TempDir())
	t.Setenv("MULTICA_AGENT_ID", "agent-fixture")
	t.Setenv("MULTICA_TASK_ID", "run-fixture")
	for _, args := range [][]string{{"add", "OTHER-1", "--name", "Panel", "--url", "https://example.test/"}, {"add", "--issue-id", "OTHER-1", "--name", "Panel", "--url", "https://example.test/"}, {"add", "--name", "Panel"}} {
		t.Setenv("MULTICA_TOKEN", "mat_fixture")
		cmd := newIssuePanelCommand()
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Fatal("invalid arguments were accepted")
		}
	}
	for _, token := range []string{"", "mul_human_fixture", "mcn_node_fixture"} {
		t.Setenv("MULTICA_TOKEN", token)
		cmd := newIssuePanelCommand()
		cmd.SetArgs([]string{"add", "--name", "Panel", "--url", "https://example.test/"})
		if err := cmd.Execute(); err == nil {
			t.Fatal("non-task credential was accepted")
		}
	}
	if calls != 0 {
		t.Fatal("invalid CLI request reached the network")
	}
}

func TestIssuePanelCLIDoesNotClaimSuccessOrRetryOnRejection(t *testing.T) {
	t.Chdir(t.TempDir())
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"error":"not a current issue run"}`))
	}))
	defer srv.Close()
	t.Setenv("MULTICA_SERVER_URL", srv.URL)
	t.Setenv("MULTICA_WORKSPACE_ID", "workspace-fixture")
	t.Setenv("MULTICA_TASK_CONFIG_ROOT", t.TempDir())
	t.Setenv("MULTICA_AGENT_ID", "agent-fixture")
	t.Setenv("MULTICA_TASK_ID", "run-fixture")
	t.Setenv("MULTICA_TOKEN", "mat_fixture")
	cmd := newIssuePanelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"add", "--name", "Panel", "--url", "https://example.test/"})
	if err := cmd.Execute(); err == nil || calls != 1 || strings.Contains(out.String(), `"id"`) {
		t.Fatal("rejection was retried or reported as success")
	}
}
