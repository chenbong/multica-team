package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/cli"
)

func TestParseTeamMeshIssueLink(t *testing.T) {
	trusted := []string{"http://mesh.example:8004"}
	for _, tc := range []struct {
		url, workspace, issue string
		ok                    bool
	}{
		{"http://mesh.example:8004/cbh/issues/W4A8-1?token=ignored#reply", "cbh", "W4A8-1", true},
		{"http://other.example:8004/cbh/issues/W4A8-1", "", "", false},
		{"http://user:password@mesh.example:8004/cbh/issues/W4A8-1", "", "", false},
		{"http://mesh.example:8004/cbh/issues/not-an-issue", "", "", false},
		{"http://mesh.example:8004/cbh/issues/W4A8-1/extra", "", "", false},
	} {
		workspace, issue, err := parseTeamMeshIssueLink(tc.url, trusted)
		if (err == nil) != tc.ok || workspace != tc.workspace || issue != tc.issue {
			t.Errorf("parse %q = %q, %q, %v", tc.url, workspace, issue, err)
		}
	}
}

func TestResolveIssueLinkUsesConfiguredServerAndWorkspace(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/config" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"issue_link_origins":["http://mesh.example:8004"]}`))
			return
		}
		calls++
		if r.URL.Path != "/api/issues/W4A8-1" || r.URL.Query().Get("team_mesh_workspace") != "cbh" || len(r.URL.Query()) != 1 {
			t.Errorf("unexpected request %s", r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"06fa141a-1019-4ddf-8e1e-97c61459df27","identifier":"W4A8-1"}`))
	}))
	defer server.Close()
	t.Setenv("MULTICA_APP_URL", "")
	client := cli.NewAPIClient(server.URL, "source", "mat_test")
	ref, err := resolveIssueRef(context.Background(), client, "http://mesh.example:8004/cbh/issues/W4A8-1?ignored=secret#x")
	if err != nil || ref.ID != "06fa141a-1019-4ddf-8e1e-97c61459df27" || calls != 1 {
		t.Fatalf("unexpected resolution: %+v, %v, calls=%d", ref, err, calls)
	}
	_, err = resolveIssueRef(context.Background(), client, "http://elsewhere.example/cbh/issues/W4A8-1")
	if err == nil || calls != 1 {
		t.Fatal("untrusted URL reached the configured API")
	}
}
