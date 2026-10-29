package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/plugincontract"
)

func governanceRequest(t *testing.T, handler http.HandlerFunc, user, method, id string, body any) *httptest.ResponseRecorder {
	t.Helper()
	r := newRequestAsUser(user, method, "/api/skills/"+id, body)
	if id != "" {
		r = withURLParams(r, "id", id)
	}
	w := httptest.NewRecorder()
	handler(w, r)
	return w
}
func governanceSkill(t *testing.T, name, scope, creator, workspace string) string {
	t.Helper()
	cols := testutil.Cols{"name": name, "scope": scope, "created_by": creator, "description": "original", "content": "# Keep body", "config": "{}"}
	if scope == "team" {
		cols["workspace_id"] = nil
	} else {
		cols["workspace_id"] = workspace
	}
	if scope == "personal" {
		cols["owner_user_id"] = creator
	}
	return dbfx.Insert(t, "skill", cols)
}
func governanceName() string { return "governance-" + uuid.NewString() }

func TestSkillGovernanceGlobalCaseWhitespaceAndPrivateConflict(t *testing.T) {
	creator := dbfx.User(t, "Skill creator", uuid.NewString()+"@example.test")
	name := governanceName()
	id := governanceSkill(t, name, "personal", creator, testWorkspaceID)
	for _, scope := range []string{"team", "workspace", "personal"} {
		response := governanceRequest(t, testHandler.CreateSkill, testUserID, "POST", "", map[string]any{"name": " \t" + strings.ToUpper(name) + "\u00a0", "scope": scope})
		if response.Code != 409 {
			t.Fatalf("scope %s status %d", scope, response.Code)
		}
		var result struct {
			Existing ExistingSkillIdentity `json:"existing_skill"`
		}
		if json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Existing.Scope != "personal" || result.Existing.Creator == nil || result.Existing.Creator.Name != "Skill creator" || result.Existing.ID != "" || strings.Contains(response.Body.String(), "# Keep body") {
			t.Fatalf("bad conflict metadata: %s", response.Body.String())
		}
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM skill WHERE team_mesh_skill_name_key(name)=team_mesh_skill_name_key($1)`, name); n != 1 {
		t.Fatal("duplicate created")
	}
	_ = id
}

func TestSkillGovernanceCreatorOnlyAllScopesAndFormerMembership(t *testing.T) {
	creator := dbfx.User(t, "Former member", uuid.NewString()+"@example.test")
	source := dbfx.Workspace(t, "Source", "source-"+uuid.NewString())
	admin := dbfx.User(t, "Other admin", uuid.NewString()+"@example.test")
	dbfx.Member(t, testWorkspaceID, admin, "admin")
	for _, scope := range []string{"workspace", "team", "personal"} {
		t.Run(scope, func(t *testing.T) {
			id := governanceSkill(t, governanceName(), scope, creator, source)
			for _, h := range []http.HandlerFunc{testHandler.UpdateSkill, testHandler.DeleteSkill, testHandler.RefreshSkill} {
				method := "POST"
				if fmt.Sprintf("%p", h) == fmt.Sprintf("%p", testHandler.UpdateSkill) {
					method = "PUT"
				}
				response := governanceRequest(t, h, admin, method, id, map[string]any{"description": "must not change"})
				if response.Code != 403 && response.Code != 404 {
					t.Fatalf("non-creator admin allowed: %d", response.Code)
				}
			}
			response := governanceRequest(t, testHandler.UpdateSkill, creator, "PUT", id, map[string]any{"description": "creator update"})
			if response.Code != 200 {
				t.Fatalf("creator without origin membership denied %d: %s", response.Code, response.Body.String())
			}
			response = governanceRequest(t, testHandler.DeleteSkill, creator, "DELETE", id, nil)
			if response.Code != 204 {
				t.Fatalf("creator delete %d", response.Code)
			}
		})
	}
}

func TestSkillGovernanceCreatorSummaryDoesNotDependOnMembers(t *testing.T) {
	creator := dbfx.User(t, "Outside creator", uuid.NewString()+"@example.test")
	source := dbfx.Workspace(t, "Other workspace", "source-"+uuid.NewString())
	id := governanceSkill(t, governanceName(), "workspace", creator, source)
	response := governanceRequest(t, testHandler.ListSkills, testUserID, "GET", "", nil)
	var skills []SkillSummaryResponse
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &skills) != nil {
		t.Fatal("list failed")
	}
	found := false
	for _, skill := range skills {
		if skill.ID == id {
			found = skill.Creator != nil && skill.Creator.UserID == creator && skill.Creator.Name == "Outside creator"
		}
	}
	if !found {
		t.Fatal("creator outside current workspace omitted")
	}
	for _, path := range []string{"", "?include=metadata"} {
		r := newRequestAsUser(testUserID, "GET", "/api/skills/"+id+path, nil)
		r = withURLParams(r, "id", id)
		w := httptest.NewRecorder()
		testHandler.GetSkill(w, r)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Outside creator") {
			t.Fatalf("detail creator missing %d", w.Code)
		}
	}
}

func TestSkillGovernanceRefreshNewURLPersistsAndPreservesIdentity(t *testing.T) {
	creator := dbfx.User(t, "Creator", uuid.NewString()+"@example.test")
	name := governanceName()
	id := governanceSkill(t, name, "team", creator, testWorkspaceID)
	data := buildTestZip(t, map[string]string{"bundle/SKILL.md": "---\nname: entirely-different-name\ndescription: new description\n---\n# New body\n", "bundle/helper.txt": "new helper"})
	previous := http.DefaultTransport
	calls := 0
	http.DefaultTransport = oneToolFixtureTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != oneToolPermanentFixture {
			t.Errorf("wrong source URL")
		}
		return oneToolFixtureResponse(200, data), nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	response := governanceRequest(t, testHandler.RefreshSkill, creator, "POST", id, map[string]any{"url": "下载并安装此Skill " + oneToolPermanentFixture})
	if response.Code != 200 {
		t.Fatalf("source update %d: %s", response.Code, response.Body.String())
	}
	var skill SkillWithFilesResponse
	_ = json.Unmarshal(response.Body.Bytes(), &skill)
	if skill.ID != id || skill.Name != name || skill.CreatedBy == nil || *skill.CreatedBy != creator || skill.Scope != "team" || skill.Content != "---\nname: entirely-different-name\ndescription: new description\n---\n# New body\n" {
		t.Fatal("identity or body changed unexpectedly")
	}
	current, err := testHandler.Queries.GetSkill(context.Background(), parseUUID(id))
	if err != nil {
		t.Fatal(err)
	}
	origin, ok := parseSkillOrigin(current.Config)
	if !ok || origin.SourceURL != oneToolPermanentFixture || origin.Type != "onetool" {
		t.Fatal("new source not saved")
	}
	response = governanceRequest(t, testHandler.RefreshSkill, creator, "POST", id, nil)
	if response.Code != 200 || calls != 2 {
		t.Fatalf("default source reuse failed %d calls %d", response.Code, calls)
	}
	http.DefaultTransport = oneToolFixtureTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return oneToolFixtureResponse(503, []byte("unavailable")), nil
	})
	response = governanceRequest(t, testHandler.RefreshSkill, creator, "POST", id, map[string]any{"url": oneToolVersionFixture})
	if response.Code < 400 {
		t.Fatal("failed download accepted")
	}
	after, _ := testHandler.Queries.GetSkill(context.Background(), parseUUID(id))
	if after.Name != current.Name || string(after.Config) != string(current.Config) || after.Content != current.Content {
		t.Fatal("failed update changed data")
	}
}

func TestSkillGovernanceRejectsCrossScopeOverwrite(t *testing.T) {
	name := governanceName()
	id := governanceSkill(t, name, "team", testUserID, testWorkspaceID)
	w := httptest.NewRecorder()
	r := newRequest("POST", "/api/skills/import", nil)
	testHandler.finishSkillImport(w, r, testWorkspaceID, parseUUID(testWorkspaceID), parseUUID(testUserID), testUserID, "overwrite", true, &importedSkill{name: name, content: "must not overwrite"}, "personal")
	if w.Code != 409 {
		t.Fatalf("cross-scope overwrite %d", w.Code)
	}
	after, _ := testHandler.Queries.GetSkill(t.Context(), parseUUID(id))
	if after.Content != "# Keep body" {
		t.Fatal("cross-scope content changed")
	}
}

func TestSkillGovernanceConcurrentCreateHasOneWinner(t *testing.T) {
	name := governanceName()
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM skill WHERE team_mesh_skill_name_key(name)=team_mesh_skill_name_key($1)`, name)
	})
	var wg sync.WaitGroup
	codes := make(chan int, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			value := name
			if i%2 == 0 {
				value = " " + strings.ToUpper(name) + " "
			}
			r := newRequest("POST", "/api/skills", map[string]any{"name": value, "scope": "team"})
			w := httptest.NewRecorder()
			testHandler.CreateSkill(w, r)
			codes <- w.Code
		}(i)
	}
	wg.Wait()
	close(codes)
	won := 0
	for code := range codes {
		if code == 201 {
			won++
		} else if code != 409 {
			t.Fatalf("unexpected concurrent status %d", code)
		}
	}
	if won != 1 {
		t.Fatalf("winners %d", won)
	}
}

func TestSkillGovernancePluginUpdatesRespectCreator(t *testing.T) {
	creator := dbfx.User(t, "Plugin skill creator", uuid.NewString()+"@example.test")
	installation := uuid.NewString()
	id := dbfx.Insert(t, "skill", testutil.Cols{"workspace_id": testWorkspaceID, "created_by": creator, "scope": "workspace", "name": governanceName(), "plugin_installation_id": installation})
	svc := service.PluginService{Queries: testHandler.Queries}
	err := svc.InstallSkillResources(t.Context(), testHandler.Queries, db.PluginInstallation{ID: parseUUID(installation)}, plugincontract.Manifest{}, parseUUID(testUserID))
	if err == nil {
		t.Fatal("a plugin upgrade pruned another creator's skill")
	}
	if dbfx.Count(t, "SELECT count(*) FROM skill WHERE id=$1", id) != 1 {
		t.Fatal("denied upgrade changed skill")
	}
	req := newRequest("DELETE", "/plugin", nil)
	w := httptest.NewRecorder()
	if testHandler.canManagePluginSkills(w, req, parseUUID(installation)) || w.Code != 403 {
		t.Fatal("uninstall could delete another creator's skill")
	}
	if err := svc.InstallSkillResources(t.Context(), testHandler.Queries, db.PluginInstallation{ID: parseUUID(installation)}, plugincontract.Manifest{}, parseUUID(creator)); err != nil {
		t.Fatal(err)
	}
	if dbfx.Count(t, "SELECT count(*) FROM skill WHERE id=$1", id) != 0 {
		t.Fatal("creator plugin prune failed")
	}
}
