package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestResourceWebPanelURLValidation(t *testing.T) {
	for _, href := range []string{"https://example.test/#/dashboard?team=a", "http://127.0.0.1:3456/?q=one%20two#section", "https://example.test/path?a=1&b=2"} {
		name, got, ok := validResourceWebPanel(" Panel ", href)
		if !ok || got != href || name != "Panel" {
			t.Fatalf("original navigation URL changed: %q => %q", href, got)
		}
	}
	for _, href := range []string{"javascript:alert(1)", "data:text/html,test", "file:///tmp/page", "https://user:password@example.test/", "https:///missing-host", "example.test/page", "https://example.test/\r\nextra", "https://example.test/\x00", strings.Repeat("x", 4097)} {
		if _, _, ok := validResourceWebPanel("Panel", href); ok {
			t.Fatalf("unsafe URL accepted: %q", href)
		}
	}
	for _, name := range []string{"", "   ", strings.Repeat("字", 121), "bad\nname"} {
		if _, _, ok := validResourceWebPanel(name, "https://example.test/"); ok {
			t.Fatal("invalid name accepted")
		}
	}
}

func TestResourceWebPanelMachineActorsCannotManage(t *testing.T) {
	for _, actor := range []string{"task_token", "cloud_pat"} {
		r := httptest.NewRequest(http.MethodGet, "/api/issues/unused/web-panels", nil)
		r.Header.Set("X-Actor-Source", actor)
		w := httptest.NewRecorder()
		(&Handler{}).ListIssueWebPanels(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("machine actor %s: HTTP %d", actor, w.Code)
		}
	}
}

func TestResourceWebPanelRequestRejectsUnknownFields(t *testing.T) {
	for _, body := range []string{`{"name":"Panel","url":"https://example.test/","created_by":"forged"}`, `{"name":"Panel"} {}`} {
		w := httptest.NewRecorder()
		var out struct {
			Name *string `json:"name"`
			URL  *string `json:"url"`
		}
		if decodeResourceWebPanel(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)), &out) || w.Code != http.StatusBadRequest {
			t.Fatal("unknown or trailing request data accepted")
		}
	}
}

func webPanelDB(t *testing.T) {
	t.Helper()
	if testHandler == nil || dbfx == nil {
		t.Skip("isolated test database unavailable")
	}
	var installed bool
	if err := testPool.QueryRow(t.Context(), `SELECT to_regclass('resource_web_panel') IS NOT NULL`).Scan(&installed); err != nil || !installed {
		t.Fatal("apply resource web panel migrations in the isolated test database")
	}
}

func webPanelCall(t *testing.T, method, scope, resource, panel, user string, body any, status int) *httptest.ResponseRecorder {
	t.Helper()
	r := newRequest(method, "/api/web-panels", body)
	r.Header.Set("X-User-ID", user)
	r = testutil.WithURLParams(r, "id", resource, "panelId", panel)
	var fn http.HandlerFunc
	if method == http.MethodGet {
		fn = func(w http.ResponseWriter, r *http.Request) { testHandler.listResourceWebPanels(w, r, scope) }
	} else {
		fn = func(w http.ResponseWriter, r *http.Request) { testHandler.mutateResourceWebPanel(w, r, scope) }
	}
	return testutil.Call(t, fn, r).Want(status).ResponseRecorder
}

func TestResourceWebPanelSharedStorageAndCreatorPermissions(t *testing.T) {
	webPanelDB(t)
	peer := dbfx.User(t, "Panel peer", "panel-peer-"+uuid.NewString()+"@example.test")
	dbfx.Member(t, testWorkspaceID, peer, "member")
	for _, scope := range []string{"workspace", "issue"} {
		t.Run(scope, func(t *testing.T) {
			resource := testWorkspaceID
			if scope == "issue" {
				resource = dbfx.Issue(t, "Panel issue")
			}
			body := map[string]string{"name": "Panel", "url": "https://example.test/" + uuid.NewString() + "#/route"}
			w := webPanelCall(t, http.MethodPost, scope, resource, "", peer, body, http.StatusCreated)
			var created resourceWebPanel
			if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { dbfx.Exec(t, `DELETE FROM resource_web_panel WHERE id=$1`, created.ID) })
			if created.CreatedBy != peer || !created.CanManage || created.URL != body["url"] {
				t.Fatal("creator or original URL not preserved")
			}
			webPanelCall(t, http.MethodPost, scope, resource, "", testUserID, body, http.StatusConflict)
			other := dbfx.User(t, "Panel other", "panel-other-"+uuid.NewString()+"@example.test")
			dbfx.Member(t, testWorkspaceID, other, "member")
			w = webPanelCall(t, http.MethodGet, scope, resource, "", other, nil, http.StatusOK)
			var list struct {
				Panels []resourceWebPanel `json:"panels"`
				CanAdd bool               `json:"can_add"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || !list.CanAdd || len(list.Panels) == 0 || list.Panels[len(list.Panels)-1].CanManage {
				t.Fatal("shared view or member management isolation failed")
			}
			webPanelCall(t, http.MethodPatch, scope, resource, created.ID, other, map[string]string{"name": "Forbidden"}, http.StatusForbidden)
			webPanelCall(t, http.MethodDelete, scope, resource, created.ID, other, nil, http.StatusForbidden)
			webPanelCall(t, http.MethodPatch, scope, resource, created.ID, peer, map[string]string{"name": "Renamed"}, http.StatusOK)
			// The fixture owner moderates any panel, but does not own its origin.
			webPanelCall(t, http.MethodDelete, scope, resource, created.ID, testUserID, nil, http.StatusNoContent)
		})
	}
}

func TestResourceWebPanelForeignResourceAndRevokedMembership(t *testing.T) {
	webPanelDB(t)
	foreign := dbfx.Workspace(t, "Panel foreign", "panel-"+uuid.NewString())
	issue := dbfx.Issue(t, "Foreign panel", testutil.Cols{"workspace_id": foreign})
	webPanelCall(t, http.MethodGet, "workspace", foreign, "", testUserID, nil, http.StatusNotFound)
	webPanelCall(t, http.MethodGet, "issue", issue, "", testUserID, nil, http.StatusNotFound)
	peer := dbfx.User(t, "Revoked panel peer", "revoked-"+uuid.NewString()+"@example.test")
	dbfx.Member(t, testWorkspaceID, peer, "member")
	w := webPanelCall(t, http.MethodPost, "workspace", testWorkspaceID, "", peer, map[string]string{"name": "Private ownership", "url": "https://example.test/" + uuid.NewString()}, http.StatusCreated)
	var panel resourceWebPanel
	_ = json.Unmarshal(w.Body.Bytes(), &panel)
	t.Cleanup(func() { dbfx.Exec(t, `DELETE FROM resource_web_panel WHERE id=$1`, panel.ID) })
	dbfx.Exec(t, `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, testWorkspaceID, peer)
	webPanelCall(t, http.MethodGet, "workspace", testWorkspaceID, "", peer, nil, http.StatusNotFound)
	webPanelCall(t, http.MethodDelete, "workspace", testWorkspaceID, panel.ID, peer, nil, http.StatusNotFound)
}

func TestResourceWebPanelIssueTransferInheritsDestination(t *testing.T) {
	webPanelDB(t)
	issue := dbfx.Issue(t, "Moving panel")
	w := webPanelCall(t, http.MethodPost, "issue", issue, "", testUserID, map[string]string{"name": "Moves with issue", "url": "https://example.test/" + uuid.NewString()}, http.StatusCreated)
	var panel resourceWebPanel
	_ = json.Unmarshal(w.Body.Bytes(), &panel)
	t.Cleanup(func() { dbfx.Exec(t, `DELETE FROM resource_web_panel WHERE id=$1`, panel.ID) })
	foreign := dbfx.Workspace(t, "Panel destination", "panel-destination-"+uuid.NewString())
	dbfx.Exec(t, `UPDATE issue SET workspace_id=$2 WHERE id=$1`, issue, foreign)
	webPanelCall(t, http.MethodGet, "issue", issue, "", testUserID, nil, http.StatusNotFound)
	var persisted bool
	if err := testPool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM resource_web_panel WHERE id=$1 AND issue_id=$2 AND workspace_id IS NULL)`, panel.ID, issue).Scan(&persisted); err != nil || !persisted {
		t.Fatal("transfer detached or duplicated issue panel")
	}
	destinationOwner := dbfx.User(t, "Destination owner", "destination-"+uuid.NewString()+"@example.test")
	dbfx.Member(t, foreign, destinationOwner, "owner")
	r := newRequest(http.MethodGet, "/api/issues/"+issue+"/web-panels", nil)
	r.Header.Set("X-User-ID", destinationOwner)
	r.Header.Set("X-Workspace-ID", foreign)
	r = testutil.WithURLParams(r, "id", issue)
	var visible struct {
		Panels []resourceWebPanel `json:"panels"`
	}
	testutil.Call(t, testHandler.ListIssueWebPanels, r).Want(http.StatusOK).JSON(&visible)
	if len(visible.Panels) != 1 || visible.Panels[0].WorkspaceID != foreign || !visible.Panels[0].CanManage {
		t.Fatal("destination ownership/access did not follow the issue")
	}
}

func TestResourceWebPanelConcurrentDuplicateAndLongURL(t *testing.T) {
	webPanelDB(t)
	issue := dbfx.Issue(t, "Concurrent panels")
	href := "https://example.test/?query=" + strings.Repeat(uuid.NewString(), 100) + "#/route"
	var wg sync.WaitGroup
	statuses := make(chan int, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := testutil.WithURLParams(newRequest(http.MethodPost, "/", map[string]string{"name": "Concurrent", "url": href}), "id", issue)
			w := httptest.NewRecorder()
			testHandler.CreateIssueWebPanel(w, r)
			statuses <- w.Code
		}()
	}
	wg.Wait()
	close(statuses)
	created := 0
	for status := range statuses {
		if status == http.StatusCreated {
			created++
		} else if status != http.StatusConflict {
			t.Fatalf("unexpected concurrent result: %d", status)
		}
	}
	if created != 1 {
		t.Fatalf("expected one new shared link, got %d", created)
	}
	t.Cleanup(func() { dbfx.Exec(t, `DELETE FROM resource_web_panel WHERE issue_id=$1`, issue) })
}

func TestResourceWebPanelResourceIsolationAndApplicationCleanup(t *testing.T) {
	webPanelDB(t)
	first := dbfx.Issue(t, "First panel resource")
	second := dbfx.Issue(t, "Second panel resource")
	w := webPanelCall(t, http.MethodPost, "issue", first, "", testUserID, map[string]string{"name": "Owned panel", "url": "https://example.test/" + uuid.NewString()}, http.StatusCreated)
	var panel resourceWebPanel
	_ = json.Unmarshal(w.Body.Bytes(), &panel)
	t.Cleanup(func() { dbfx.Exec(t, `DELETE FROM resource_web_panel WHERE id=$1`, panel.ID) })
	webPanelCall(t, http.MethodDelete, "issue", second, panel.ID, testUserID, nil, http.StatusNotFound)
	// A tenant-mismatched delete must not clear the panel as a side effect.
	wrong := dbfx.Workspace(t, "Wrong cleanup tenant", "wrong-cleanup-"+uuid.NewString())
	if err := testHandler.Queries.DeleteIssue(t.Context(), db.DeleteIssueParams{ID: parseUUID(first), WorkspaceID: parseUUID(wrong)}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := testPool.QueryRow(t.Context(), `SELECT count(*) FROM resource_web_panel WHERE id=$1`, panel.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("foreign cleanup deleted a panel")
	}
	if err := testHandler.Queries.DeleteIssue(t.Context(), db.DeleteIssueParams{ID: parseUUID(first), WorkspaceID: parseUUID(testWorkspaceID)}); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(t.Context(), `SELECT count(*) FROM resource_web_panel WHERE id=$1`, panel.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("issue deletion orphaned a panel")
	}
}

func TestResourceWebPanelWorkspaceCleanupAndLimit(t *testing.T) {
	webPanelDB(t)
	ws := dbfx.Workspace(t, "Workspace panel cleanup", "panel-cleanup-"+uuid.NewString())
	dbfx.Member(t, ws, testUserID, "owner")
	issue := dbfx.Issue(t, "Workspace child", testutil.Cols{"workspace_id": ws})
	for n := range resourceWebPanelLimit {
		dbfx.Insert(t, "resource_web_panel", testutil.Cols{"id": uuid.NewString(), "workspace_id": ws, "created_by": testUserID, "name": "Panel", "url": "https://example.test/" + uuid.NewString()})
		if n == 0 {
			dbfx.Insert(t, "resource_web_panel", testutil.Cols{"id": uuid.NewString(), "issue_id": issue, "created_by": testUserID, "name": "Issue panel", "url": "https://example.test/child/" + uuid.NewString()})
		}
	}
	webPanelCall(t, http.MethodPost, "workspace", ws, "", testUserID, map[string]string{"name": "Overflow", "url": "https://example.test/overflow"}, http.StatusConflict)
	if err := testHandler.Queries.DeleteWorkspace(t.Context(), parseUUID(ws)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := testPool.QueryRow(t.Context(), `SELECT count(*) FROM resource_web_panel WHERE workspace_id=$1 OR issue_id=$2`, ws, issue).Scan(&count); err != nil || count != 0 {
		t.Fatal("workspace deletion orphaned shared panels")
	}
}
