package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func transferRequest(t *testing.T, user, id, target string) *httptest.ResponseRecorder {
	return governanceRequest(t, testHandler.TransferSkill, user, "POST", id, map[string]any{"recipient_user_id": target})
}
func TestSkillTransferPreservesIdentityAndMovesAuthority(t *testing.T) {
	for _, scope := range []string{"team", "workspace"} {
		t.Run(scope, func(t *testing.T) {
			old := dbfx.User(t, "Old creator", uuid.NewString()+"@example.test")
			next := dbfx.User(t, "New creator", uuid.NewString()+"@example.test")
			id := governanceSkill(t, governanceName(), scope, old, testWorkspaceID)
			agent := createHandlerTestAgent(t, "Bound skill agent", nil)
			if _, err := testHandler.DB.Exec(t.Context(), "INSERT INTO agent_skill(agent_id,skill_id) VALUES($1,$2)", parseUUID(agent), parseUUID(id)); err != nil {
				t.Fatal(err)
			}
			before, _ := testHandler.Queries.GetSkill(t.Context(), parseUUID(id))
			w := transferRequest(t, old, id, next)
			if w.Code != 200 {
				t.Fatalf("transfer: %d %s", w.Code, w.Body.String())
			}
			var response SkillResponse
			json.Unmarshal(w.Body.Bytes(), &response)
			if response.Creator == nil || response.Creator.UserID != next {
				t.Fatal("display creator not updated")
			}
			after, _ := testHandler.Queries.GetSkill(t.Context(), parseUUID(id))
			if before.ID != after.ID || before.Name != after.Name || before.Content != after.Content || before.Scope != after.Scope || before.WorkspaceID != after.WorkspaceID || string(before.Config) != string(after.Config) || before.CreatedAt != after.CreatedAt {
				t.Fatal("non-ownership data changed")
			}
			if dbfx.Count(t, "SELECT count(*) FROM agent_skill WHERE agent_id=$1 AND skill_id=$2", agent, id) != 1 {
				t.Fatal("binding lost")
			}
			if dbfx.Count(t, "SELECT count(*) FROM team_mesh_skill_transfer_audit WHERE skill_id=$1 AND from_user_id=$2 AND to_user_id=$3", id, old, next) != 1 {
				t.Fatal("audit absent")
			}
			if transferRequest(t, old, id, old).Code != 403 {
				t.Fatal("old creator reclaimed")
			}
			for _, h := range []http.HandlerFunc{testHandler.UpdateSkill, testHandler.DeleteSkill, testHandler.RefreshSkill, testHandler.UpsertSkillFile, testHandler.DeleteSkillFile, testHandler.AttachLabelToSkill, testHandler.DetachLabelFromSkill} {
				if w := governanceRequest(t, h, old, "PUT", id, map[string]any{"name": "forbidden"}); w.Code != 403 {
					t.Fatalf("former creator allowed %d", w.Code)
				}
			}
			if w := governanceRequest(t, testHandler.UpdateSkill, next, "PUT", id, map[string]any{"description": "new owner can edit"}); w.Code != 200 {
				t.Fatalf("new owner cannot edit: %d %s", w.Code, w.Body.String())
			}
			if transferRequest(t, next, id, old).Code != 200 {
				t.Fatal("new creator cannot transfer again")
			}
		})
	}
}
func TestSkillTransferRejectsInvalidAndUnauthorized(t *testing.T) {
	target := dbfx.User(t, "Recipient", uuid.NewString()+"@example.test")
	id := governanceSkill(t, governanceName(), "team", testUserID, testWorkspaceID)
	other := dbfx.User(t, "Not creator", uuid.NewString()+"@example.test")
	for _, tc := range []struct {
		user, target string
		code         int
	}{{other, target, 403}, {testUserID, testUserID, 400}, {testUserID, "bad", 400}, {testUserID, uuid.NewString(), 404}} {
		if w := transferRequest(t, tc.user, id, tc.target); w.Code != tc.code {
			t.Fatalf("want%d got%d", tc.code, w.Code)
		}
	}
	personal := governanceSkill(t, governanceName(), "personal", testUserID, testWorkspaceID)
	if transferRequest(t, testUserID, personal, target).Code != 400 {
		t.Fatal("personal transfer accepted")
	}
	for _, body := range []string{`{"recipient_user_id":"` + target + `","extra":true}`, `{"recipient_user_id":"` + target + `"}{}`, strings.Repeat("x", 5000)} {
		r := withURLParams(newRequestAsUser(testUserID, "POST", "/transfer", nil), "id", id)
		r.Body = http.NoBody
		r.Body = io.NopCloser(strings.NewReader(body))
		w := httptest.NewRecorder()
		testHandler.TransferSkill(w, r)
		if w.Code != 400 {
			t.Fatal("malformed request accepted")
		}
	}
	r := withURLParams(newRequestAsUser(testUserID, "POST", "/transfer", map[string]any{"recipient_user_id": target}), "id", id)
	r.Header.Set("X-Actor-Source", "task_token")
	r.Header.Set("X-Agent-ID", uuid.NewString())
	w := httptest.NewRecorder()
	testHandler.TransferSkill(w, r)
	if w.Code != 403 {
		t.Fatal("agent transfer accepted")
	}
	if dbfx.Count(t, "SELECT count(*) FROM team_mesh_skill_transfer_audit WHERE skill_id=$1", id) != 0 {
		t.Fatal("failed transfer wrote audit")
	}
}
func TestSkillTransferConcurrentRequestsHaveOneWinner(t *testing.T) {
	id := governanceSkill(t, governanceName(), "team", testUserID, testWorkspaceID)
	targets := []string{dbfx.User(t, "One", uuid.NewString()+"@example.test"), dbfx.User(t, "Two", uuid.NewString()+"@example.test")}
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for _, target := range targets {
		wg.Add(1)
		go func(target string) { defer wg.Done(); codes <- transferRequest(t, testUserID, id, target).Code }(target)
	}
	wg.Wait()
	close(codes)
	wins := 0
	for code := range codes {
		if code == 200 {
			wins++
		} else if code != 403 {
			t.Fatalf("unexpected%d", code)
		}
	}
	if wins != 1 {
		t.Fatal("multiple winners")
	}
	if dbfx.Count(t, "SELECT count(*) FROM team_mesh_skill_transfer_audit WHERE skill_id=$1", id) != 1 {
		t.Fatal("audit not atomic")
	}
	// A stale mutation rechecks creator after the row lock, not its earlier read.
	tx, ok := testHandler.beginSkillMutation(httptest.NewRecorder(), newRequestAsUser(testUserID, "PUT", "/file", nil), parseUUID(id))
	if ok {
		tx.Rollback(context.Background())
		t.Fatal("stale old-owner write allowed")
	}
}
func TestSkillTransferRecipientSearchOutsideWorkspace(t *testing.T) {
	prefix := "recipient-" + uuid.NewString()
	recipient := dbfx.User(t, "External recipient", prefix+"@example.test")
	id := governanceSkill(t, governanceName(), "workspace", testUserID, testWorkspaceID)
	w := governanceRequest(t, testHandler.SkillTransferRecipients, testUserID, "GET", id, nil)
	if w.Code != 400 {
		t.Fatal("empty directory listing allowed")
	}
	r := withURLParams(newRequestAsUser(testUserID, "GET", fmt.Sprintf("/recipients?q=%s", prefix), nil), "id", id)
	w = httptest.NewRecorder()
	testHandler.SkillTransferRecipients(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), recipient) {
		t.Fatalf("global recipient not found %d", w.Code)
	}
	r = withURLParams(newRequestAsUser(recipient, "GET", "/recipients?q=external", nil), "id", id)
	w = httptest.NewRecorder()
	testHandler.SkillTransferRecipients(w, r)
	if w.Code != 403 {
		t.Fatal("noncreator directory search allowed")
	}
}

func TestSkillTransferDeniesRefreshStartedByFormerCreator(t *testing.T) {
	next := dbfx.User(t, "New creator", uuid.NewString()+"@example.test")
	id := governanceSkill(t, governanceName(), "team", testUserID, testWorkspaceID)
	data := buildTestZip(t, map[string]string{"SKILL.md": "---\nname: replacement\n---\nShould never commit"})
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	http.DefaultTransport = oneToolFixtureTransport(func(r *http.Request) (*http.Response, error) {
		if w := transferRequest(t, testUserID, id, next); w.Code != 200 {
			t.Fatalf("transfer failed%d", w.Code)
		}
		return oneToolFixtureResponse(200, data), nil
	})
	w := governanceRequest(t, testHandler.RefreshSkill, testUserID, "POST", id, map[string]any{"url": oneToolPermanentFixture})
	if w.Code == 200 {
		t.Fatal("former creator's delayed download overwrote transferred skill")
	}
	current, _ := testHandler.Queries.GetSkill(t.Context(), parseUUID(id))
	if current.Content != "# Keep body" || current.CreatedBy != parseUUID(next) {
		t.Fatal("refresh changed transferred skill")
	}
}

func TestSkillTransferPluginPruneRechecksCreator(t *testing.T) {
	next := dbfx.User(t, "Recipient", uuid.NewString()+"@example.test")
	plugin := uuid.NewString()
	id := governanceSkill(t, governanceName(), "team", testUserID, testWorkspaceID)
	if _, err := testHandler.DB.Exec(t.Context(), "UPDATE skill SET plugin_installation_id=$2 WHERE id=$1", parseUUID(id), parseUUID(plugin)); err != nil {
		t.Fatal(err)
	}
	if transferRequest(t, testUserID, id, next).Code != 200 {
		t.Fatal("transfer failed")
	}
	svc := service.PluginService{Queries: testHandler.Queries, TxStarter: testHandler.TxStarter}
	err := svc.Uninstall(service.WithSkillMutationUser(t.Context(), parseUUID(testUserID)), db.PluginInstallation{ID: parseUUID(plugin)})
	if err == nil {
		t.Fatal("former creator uninstalled transferred skill")
	}
	if dbfx.Count(t, "SELECT count(*) FROM skill WHERE id=$1", id) != 1 {
		t.Fatal("plugin deleted transferred skill")
	}
}
