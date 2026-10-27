package handler

import (
	"encoding/json"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"testing"
)

func TestProjectWorkingDirectoryRequiresMatchingMachine(t *testing.T) {
	rt := db.AgentRuntime{DaemonID: pgtype.Text{String: "machine-a", Valid: true}}
	resources := []ProjectResourceData{{ResourceType: "local_directory", ResourceRef: json.RawMessage(`{"local_path":"/home/user/project","daemon_id":"machine-b","execution_mode":"in_place"}`)}}
	if projectDirectoryClaimBlockReason(resources, rt, true, false) == "" {
		t.Fatal("unconfigured machine silently fell back")
	}
	rt.DaemonID.String = "machine-b"
	if projectDirectoryClaimBlockReason(resources, rt, false, false) != "" {
		t.Fatal("native in_place was blocked")
	}
	rt.DaemonID = pgtype.Text{}
	if projectDirectoryClaimBlockReason(resources, rt, true, false) == "" {
		t.Fatal("ownerless machine bypassed guard")
	}
	if projectDirectoryClaimBlockReason(nil, rt, false, false) != "" {
		t.Fatal("ordinary projects changed")
	}
	if projectDirectoryClaimBlockReason(projectDirectoryResourcesForRun(resources, true), rt, false, false) != "" {
		t.Fatal("native coordinator exemption changed")
	}
}

func TestProjectWorkingDirectoryCapabilityGate(t *testing.T) {
	rt := db.AgentRuntime{DaemonID: pgtype.Text{String: "machine", Valid: true}}
	res := []ProjectResourceData{{ResourceType: "working_directory", ResourceRef: json.RawMessage(`{"local_path":"/srv/project"}`)}}
	if projectDirectoryClaimBlockReason(res, rt, true, false) == "" {
		t.Fatal("old daemon can silently ignore project directory")
	}
	if reason := projectDirectoryClaimBlockReason(res, rt, false, true); reason != "" {
		t.Fatal(reason)
	}
	if reason := projectDirectoryClaimBlockReason(projectDirectoryResourcesForRun(res, true), rt, false, false); reason != "" {
		t.Fatal("coordinator gate changed")
	}
	res = append(res, ProjectResourceData{ResourceType: "local_directory", ResourceRef: json.RawMessage(`{"local_path":"/legacy","daemon_id":"other","execution_mode":"worktree"}`)})
	if reason := projectDirectoryClaimBlockReason(res, rt, false, true); reason != "" {
		t.Fatal("legacy settings override project-wide preference: " + reason)
	}
}

func TestProjectWorkingDirectoryRefValidation(t *testing.T) {
	for _, raw := range []string{`{"local_path":"/srv/project"}`, `{"local_path":" C:\\project "}`} {
		if _, err := validateAndNormalizeResourceRef("working_directory", json.RawMessage(raw)); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{`null`, `{}`, `{"local_path":"relative"}`, `{"local_path":"/srv/project","daemon_id":"machine"}`, `{"local_path":"/srv/project","execution_mode":"worktree"}`, `{"local_path":"/srv/\u0000bad"}`, `{"local_path":5}`, `{"local_path":"/srv"} {}`} {
		if _, err := validateAndNormalizeResourceRef("working_directory", json.RawMessage(raw)); err == nil {
			t.Fatalf("invalid ref accepted: %s", raw)
		}
	}
}
