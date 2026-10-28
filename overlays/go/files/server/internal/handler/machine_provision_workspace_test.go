package handler

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func provisionWorkspaceBody(username, workspace, machine string) string {
	body := map[string]string{"username": username, "workspace_name": workspace}
	if machine != "" {
		body["machine_name"] = machine
	}
	raw, _ := json.Marshal(body)
	return string(raw)
}

func TestMachineProvisionJoinsCustomExistingWorkspace(t *testing.T) {
	username := machineTestName()
	h := machineTestConfig(t, username)
	name := "研发 Team ' " + uuid.NewString()
	owner := dbfx.User(t, "Existing target owner", "target-"+uuid.NewString()+"@example.test")
	target := dbfx.Workspace(t, name, "target-"+uuid.NewString())
	dbfx.Member(t, target, owner, "owner")
	response := machineCall(t, h, provisionWorkspaceBody(username, name, "developer"), provisionServiceToken)
	if response.Code != 200 {
		t.Fatalf("custom target status %d", response.Code)
	}
	body := response.Body.String()
	for _, expected := range []string{"tmesh-workspace ensure --name " + machineShellQuote(name), "tmesh-machine rename --workspace-name " + machineShellQuote(name), "tmesh-machine agent --workspace-name " + machineShellQuote(name)} {
		if !strings.Contains(body, expected) {
			t.Fatal("generated command did not use the selected target")
		}
	}
	user, err := h.Queries.GetUserByEmail(context.Background(), username+"@example.test")
	if err != nil {
		t.Fatal(err)
	}
	workspaces, err := h.Queries.ListWorkspaces(context.Background(), user.ID)
	if err != nil || len(workspaces) != 1 || uuidToString(workspaces[0].ID) != target {
		t.Fatal("provision joined an unintended workspace")
	}
	var role string
	dbfx.QueryRow(t, `SELECT role FROM member WHERE workspace_id=$1 AND user_id=$2`, target, user.ID).Scan(&role)
	if role != "member" {
		t.Fatal("provisioned user was not a regular member")
	}
	// The existing CLI ensure must reuse this workspace instead of creating a copy.
	ensured := machineWorkspaceCall(h, uuidToString(user.ID), name)
	var result map[string]any
	if ensured.Code != 200 || json.Unmarshal(ensured.Body.Bytes(), &result) != nil || result["workspace_created"] != false || result["workspace_id"] != target {
		t.Fatal("CLI ensure did not reuse the joined workspace")
	}
	var ownerRole string
	dbfx.QueryRow(t, `SELECT role FROM member WHERE workspace_id=$1 AND user_id=$2`, target, owner).Scan(&ownerRole)
	if ownerRole != "owner" {
		t.Fatal("existing owner was modified")
	}
}

func TestMachineProvisionPreservesExistingTargetRoles(t *testing.T) {
	for _, role := range []string{"owner", "admin", "member"} {
		t.Run(role, func(t *testing.T) {
			username := machineTestName()
			h := machineTestConfig(t, username)
			user := dbfx.User(t, "Existing member", username+"@example.test")
			var target string
			dbfx.QueryRow(t, `SELECT id::text FROM workspace WHERE name=$1`, defaultMachineProvisionWorkspace).Scan(&target)
			dbfx.Member(t, target, user, role)
			for i := 0; i < 2; i++ {
				if response := machineCall(t, h, machineBody(username), provisionServiceToken); response.Code != 200 {
					t.Fatalf("status %d", response.Code)
				}
			}
			var got string
			dbfx.QueryRow(t, `SELECT role FROM member WHERE workspace_id=$1 AND user_id=$2`, target, user).Scan(&got)
			if got != role {
				t.Fatalf("role changed from %s to %s", role, got)
			}
			if n := dbfx.Count(t, `SELECT count(*) FROM member WHERE workspace_id=$1 AND user_id=$2`, target, user); n != 1 {
				t.Fatal("duplicate membership")
			}
		})
	}
}

func TestMachineProvisionRejectsMissingAndAmbiguousWorkspaceAtomically(t *testing.T) {
	for _, kind := range []string{"missing", "ambiguous"} {
		t.Run(kind, func(t *testing.T) {
			username := machineTestName()
			h := machineTestConfig(t, username)
			target := defaultMachineProvisionWorkspace
			want := 409
			if kind == "missing" {
				target = "missing-" + uuid.NewString()
				want = 404
			} else {
				dbfx.Workspace(t, target, "duplicate-"+uuid.NewString())
			}
			response := machineCall(t, h, provisionWorkspaceBody(username, target, ""), provisionServiceToken)
			if response.Code != want {
				t.Fatalf("status %d want %d", response.Code, want)
			}
			if _, err := h.Queries.GetUserByEmail(context.Background(), username+"@example.test"); !errors.Is(err, pgx.ErrNoRows) {
				t.Fatal("failed target lookup created an account")
			}
			if strings.Contains(response.Body.String(), "mul_") {
				t.Fatal("failure exposed a credential")
			}
		})
	}
}

func TestMachineProvisionWorkspaceValidationAndDefault(t *testing.T) {
	username := machineTestName()
	h := machineTestConfig(t, username)
	for _, name := range []string{strings.Repeat("x", 129), "bad\nname"} {
		if response := machineCall(t, h, provisionWorkspaceBody(username, name, ""), provisionServiceToken); response.Code != 400 {
			t.Fatalf("invalid workspace status %d", response.Code)
		}
	}
	response := machineCall(t, h, provisionWorkspaceBody(username, "  ", ""), provisionServiceToken)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "tmesh-workspace ensure --name 'pdc-loop'") {
		t.Fatal("empty optional workspace did not use the default")
	}
	var target string
	dbfx.QueryRow(t, `SELECT id::text FROM workspace WHERE name=$1`, defaultMachineProvisionWorkspace).Scan(&target)
	user, err := h.Queries.GetUserByEmail(context.Background(), username+"@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM member WHERE workspace_id=$1 AND user_id=$2 AND role='member'`, target, user.ID); n != 1 {
		t.Fatal("default membership missing")
	}
	// Malformed target input cannot change the existing membership.
	if n := dbfx.Count(t, `SELECT count(*) FROM member WHERE user_id=$1`, user.ID); n != 1 {
		t.Fatal("unexpected extra membership")
	}
}
