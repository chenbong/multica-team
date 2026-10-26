package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
)

type panelRunFixture struct {
	h                                                       *Handler
	f                                                       *testutil.Fixture
	owner, originator, workspace, agent, issue, task, token string
	router                                                  http.Handler
}

func newPanelRunFixture(t *testing.T) panelRunFixture {
	t.Helper()
	webPanelDB(t)
	owner := dbfx.User(t, "Panel agent owner", uuid.NewString()+"@example.test")
	originator := dbfx.User(t, "Panel requesting member", uuid.NewString()+"@example.test")
	workspace := dbfx.Workspace(t, "Panel agent scope", "panels-"+uuid.NewString())
	f := testutil.New(testPool, workspace, owner)
	f.Member(t, workspace, owner, "owner")
	f.Member(t, workspace, originator, "member")
	runtime := f.Runtime(t, "Offline panel fixture", testutil.Cols{"status": "offline"})
	agent := f.Agent(t, "Panel fixture agent", runtime)
	issue := f.Issue(t, "Current panel issue")
	task := f.Task(t, agent, testutil.Cols{"issue_id": issue, "runtime_id": runtime, "originator_user_id": originator, "accountable_user_id": originator, "status": "running"})
	token := "mat_" + uuid.NewString()
	f.Insert(t, "task_token", testutil.Cols{"id": uuid.NewString(), "task_id": task, "agent_id": agent, "workspace_id": workspace, "user_id": owner, "token_hash": auth.HashToken(token), "expires_at": testutil.Raw("now()+interval '1 hour'")})
	f.Cleanup(t, `DELETE FROM resource_web_panel WHERE issue_id=$1 OR workspace_id=$2`, issue, workspace)
	h := *testHandler
	h.Bus = events.New()
	r := chi.NewRouter()
	r.Use(middleware.Auth(h.Queries, nil, nil, nil))
	r.Use(middleware.RequireWorkspaceMember(h.Queries))
	r.Post("/api/agent/issue-web-panels", h.AddCurrentIssueWebPanel)
	r.Get("/api/agent/issue-web-panels", h.AddCurrentIssueWebPanel)
	r.Patch("/api/agent/issue-web-panels/{panelId}", h.AddCurrentIssueWebPanel)
	r.Delete("/api/agent/issue-web-panels/{panelId}", h.AddCurrentIssueWebPanel)
	r.Get("/api/agent/personal-secrets", h.PersonalSecrets)
	r.Get("/api/agent/personal-secrets/{secretId}", h.PersonalSecrets)
	r.Get("/api/me/secrets", h.PersonalSecrets)
	r.With(RequireHumanActor).Post("/api/issues/{id}/web-panels", h.CreateIssueWebPanel)
	r.With(RequireHumanActor).Patch("/api/issues/{id}/web-panels/{panelId}", h.UpdateIssueWebPanel)
	r.With(RequireHumanActor).Delete("/api/issues/{id}/web-panels/{panelId}", h.DeleteIssueWebPanel)
	r.With(RequireHumanActor).Post("/api/workspaces/{id}/web-panels", h.CreateWorkspaceWebPanel)
	r.With(RequireHumanActor).Patch("/api/workspaces/{id}/web-panels/{panelId}", h.UpdateWorkspaceWebPanel)
	r.With(RequireHumanActor).Delete("/api/workspaces/{id}/web-panels/{panelId}", h.DeleteWorkspaceWebPanel)
	return panelRunFixture{&h, f, owner, originator, workspace, agent, issue, task, token, r}
}

func (f panelRunFixture) call(t *testing.T, method, path, token string, body any, want int, extra ...string) *httptest.ResponseRecorder {
	t.Helper()
	r := testutil.JSONRequest(method, path, body)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("X-Workspace-ID", f.workspace)
	r = testutil.WithHeaders(r, extra...)
	return testutil.Call(t, f.router.ServeHTTP, r).Want(want).ResponseRecorder
}

func TestResourceWebPanelAgentCreatesOnlyCurrentIssueAndRecordsSource(t *testing.T) {
	f := newPanelRunFixture(t)
	var changes []events.Event
	f.h.Bus.Subscribe("resource_web_panel:changed", func(e events.Event) { changes = append(changes, e) })
	otherIssue := f.f.Issue(t, "Must not receive the link")
	body := map[string]string{"name": "Agent dashboard", "url": "https://example.test/?view=team#/result"}
	w := f.call(t, "POST", "/api/agent/issue-web-panels", f.token, body, 201, "X-Task-ID", uuid.NewString(), "X-Agent-ID", uuid.NewString(), "X-Workspace-ID", uuid.NewString())
	var created struct {
		resourceWebPanel
		AlreadyExists bool `json:"already_exists"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ResourceID != f.issue || created.WorkspaceID != f.workspace || created.CreatedBy != f.originator || created.CreatedByAgentID == nil || *created.CreatedByAgentID != f.agent || created.SourceTaskID == nil || *created.SourceTaskID != f.task || created.CanManage || created.AlreadyExists {
		t.Fatalf("incorrect current-run provenance: %+v", created)
	}
	w = f.call(t, "POST", "/api/agent/issue-web-panels", f.token, map[string]string{"name": "Do not rename it", "url": body["url"]}, 200)
	var repeated struct {
		resourceWebPanel
		AlreadyExists bool `json:"already_exists"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &repeated); err != nil {
		t.Fatal(err)
	}
	if repeated.ID != created.ID || repeated.Name != body["name"] || !repeated.AlreadyExists {
		t.Fatal("retry did not reuse the unchanged item")
	}
	if len(changes) != 1 || changes[0].ActorType != "agent" || changes[0].ActorID != f.agent || changes[0].WorkspaceID != f.workspace {
		t.Fatal("expected one agent-authored realtime notification")
	}
	if n := f.f.Count(t, `SELECT count(*) FROM resource_web_panel WHERE issue_id=$1 OR workspace_id=$2`, otherIssue, f.workspace); n != 0 {
		t.Fatal("agent wrote outside its current issue")
	}
	r := testutil.WithURLParams(newRequest("GET", "/", nil), "id", f.issue)
	r.Header.Set("X-Workspace-ID", f.workspace)
	r.Header.Set("X-User-ID", f.originator)
	var list struct {
		Panels []resourceWebPanel `json:"panels"`
	}
	testutil.Call(t, f.h.ListIssueWebPanels, r).Want(200).JSON(&list)
	if len(list.Panels) != 1 || !list.Panels[0].CanManage {
		t.Fatal("the requesting member cannot manage the new item")
	}
}

func TestResourceWebPanelAgentOptionalName(t *testing.T) {
	f := newPanelRunFixture(t)
	href := "https://example.test/" + strings.Repeat("x", 180) + "?a=1#result"
	w := f.call(t, "POST", "/api/agent/issue-web-panels", f.token, map[string]string{"url": href}, http.StatusCreated)
	var panel resourceWebPanel
	if err := json.Unmarshal(w.Body.Bytes(), &panel); err != nil {
		t.Fatal(err)
	}
	if panel.Name != href || panel.URL != href || panel.ResourceID != f.issue || panel.CanManage {
		t.Fatal("optional name changed current-issue scope, full URL, or management rights")
	}
}

func TestResourceWebPanelAgentCannotOverrideTargetOrManagePanels(t *testing.T) {
	f := newPanelRunFixture(t)
	for _, field := range []string{"issue_id", "workspace_id", "scope", "created_by", "created_by_agent_id", "source_task_id"} {
		f.call(t, "POST", "/api/agent/issue-web-panels", f.token, map[string]string{"name": "Panel", "url": "https://example.test/", field: uuid.NewString()}, 400)
	}
	for _, target := range []string{"/api/issues/" + f.issue + "/web-panels", "/api/workspaces/" + f.workspace + "/web-panels"} {
		f.call(t, "POST", target, f.token, map[string]string{"name": "Forbidden", "url": "https://example.test/"}, 403)
		for _, method := range []string{"PATCH", "DELETE"} {
			f.call(t, method, target+"/"+uuid.NewString(), f.token, map[string]string{"name": "Forbidden"}, 403)
		}
	}
	pat := "mul_" + uuid.NewString()
	f.f.Insert(t, "personal_access_token", testutil.Cols{"id": uuid.NewString(), "user_id": f.owner, "name": "fixture", "token_hash": auth.HashToken(pat), "token_prefix": pat[:12]})
	f.call(t, "POST", "/api/agent/issue-web-panels", pat, map[string]string{"name": "Spoofed", "url": "https://example.test/"}, 403, "X-Actor-Source", "task_token", "X-Agent-ID", f.agent, "X-Task-ID", f.task)
	for _, source := range []string{"", "cloud_pat", "unknown"} {
		r := testutil.JSONRequest("POST", "/", map[string]string{"name": "Forbidden", "url": "https://example.test/"})
		r.Header.Set("X-Actor-Source", source)
		testutil.Call(t, f.h.AddCurrentIssueWebPanel, r).Want(403)
	}
}

func TestResourceWebPanelAgentRejectsInactiveAndUnscopedRuns(t *testing.T) {
	f := newPanelRunFixture(t)
	body := map[string]string{"name": "Panel", "url": "https://example.test/"}
	for _, status := range []string{"queued", "dispatched", "waiting_local_directory", "deferred", "completed", "failed", "cancelled"} {
		f.f.Exec(t, `UPDATE agent_task_queue SET status=$2 WHERE id=$1`, f.task, status)
		f.call(t, "POST", "/api/agent/issue-web-panels", f.token, body, 403)
	}
	f.f.Exec(t, `UPDATE agent_task_queue SET status='running',issue_id=NULL WHERE id=$1`, f.task)
	f.call(t, "POST", "/api/agent/issue-web-panels", f.token, body, 403)
	f.f.Exec(t, `UPDATE agent_task_queue SET issue_id=$2,originator_user_id=NULL WHERE id=$1`, f.task, f.issue)
	f.call(t, "POST", "/api/agent/issue-web-panels", f.token, body, 403)
	f.f.Exec(t, `UPDATE agent_task_queue SET originator_user_id=$2 WHERE id=$1`, f.task, f.originator)
	f.f.Exec(t, `UPDATE task_token SET expires_at=now()-interval '1 second' WHERE task_id=$1`, f.task)
	f.call(t, "POST", "/api/agent/issue-web-panels", f.token, body, 401)
}

func TestResourceWebPanelAgentRejectsRevocationAndIssueTransfer(t *testing.T) {
	for _, revoked := range []string{"owner", "originator", "agent", "transfer"} {
		t.Run(revoked, func(t *testing.T) {
			f := newPanelRunFixture(t)
			switch revoked {
			case "owner":
				f.f.Exec(t, `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, f.workspace, f.owner)
			case "originator":
				f.f.Exec(t, `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, f.workspace, f.originator)
			case "agent":
				f.f.Exec(t, `UPDATE agent SET archived_at=now() WHERE id=$1`, f.agent)
			case "transfer":
				other := dbfx.Workspace(t, "Panel destination", "destination-"+uuid.NewString())
				f.f.Exec(t, `UPDATE issue SET workspace_id=$2 WHERE id=$1`, f.issue, other)
			}
			want := http.StatusForbidden
			if revoked == "owner" {
				want = http.StatusNotFound
			}
			f.call(t, "POST", "/api/agent/issue-web-panels", f.token, map[string]string{"name": "Forbidden", "url": "https://example.test/"}, want)
			if revoked == "owner" {
				// Even if an upstream membership cache were stale, the handler
				// independently checks the owning member under the transaction.
				r := testutil.WithHeaders(testutil.JSONRequest("POST", "/", map[string]string{"name": "Forbidden", "url": "https://example.test/"}), "X-Actor-Source", "task_token", "X-User-ID", f.owner, "X-Agent-ID", f.agent, "X-Task-ID", f.task, "X-Workspace-ID", f.workspace)
				testutil.Call(t, f.h.AddCurrentIssueWebPanel, r).Want(403)
			}
			if n := f.f.Count(t, `SELECT count(*) FROM resource_web_panel WHERE issue_id=$1`, f.issue); n != 0 {
				t.Fatal("revoked actor created a link")
			}
		})
	}
}

func TestResourceWebPanelAgentConcurrentRetryAndLimit(t *testing.T) {
	f := newPanelRunFixture(t)
	var wg sync.WaitGroup
	codes := make(chan int, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := testutil.JSONRequest("POST", "/api/agent/issue-web-panels", map[string]string{"name": "Same panel", "url": "https://example.test/#/same"})
			r.Header.Set("Authorization", "Bearer "+f.token)
			w := httptest.NewRecorder()
			f.router.ServeHTTP(w, r)
			codes <- w.Code
		}()
	}
	wg.Wait()
	close(codes)
	created := 0
	for code := range codes {
		if code == 201 {
			created++
		} else if code != 200 {
			t.Fatalf("retry returned HTTP %d", code)
		}
	}
	if created != 1 {
		t.Fatalf("created %d duplicate items", created)
	}
	for range resourceWebPanelLimit - 1 {
		f.f.Insert(t, "resource_web_panel", testutil.Cols{"id": uuid.NewString(), "issue_id": f.issue, "created_by": f.originator, "name": "Panel", "url": "https://example.test/" + uuid.NewString()})
	}
	f.call(t, "POST", "/api/agent/issue-web-panels", f.token, map[string]string{"name": "Retry at limit", "url": "https://example.test/#/same"}, 200)
	f.call(t, "POST", "/api/agent/issue-web-panels", f.token, map[string]string{"name": "Over limit", "url": "https://example.test/new"}, 409)
	f.call(t, "POST", "/api/agent/issue-web-panels", f.token, map[string]string{"name": "Bad URL", "url": "javascript:alert(1)"}, 400)
	if n := f.f.Count(t, `SELECT count(*) FROM resource_web_panel WHERE issue_id=$1`, f.issue); n != resourceWebPanelLimit {
		t.Fatal("limit was bypassed")
	}
}

func TestResourceWebPanelAgentBusyRunAndExistingHumanOwnership(t *testing.T) {
	f := newPanelRunFixture(t)
	tx, err := testPool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err = tx.Exec(t.Context(), `SELECT id FROM agent_task_queue WHERE id=$1 FOR UPDATE`, f.task); err != nil {
		t.Fatal(err)
	}
	f.call(t, "POST", "/api/agent/issue-web-panels", f.token, map[string]string{"name": "Wait for run", "url": "https://example.test/busy"}, 409)
	if err = tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	humanPanel := f.f.Insert(t, "resource_web_panel", testutil.Cols{"id": uuid.NewString(), "issue_id": f.issue, "created_by": f.owner, "name": "Human-owned panel", "url": "https://example.test/human"})
	w := f.call(t, "POST", "/api/agent/issue-web-panels", f.token, map[string]string{"name": "Do not overwrite", "url": "https://example.test/human"}, 200)
	var panel resourceWebPanel
	if err = json.Unmarshal(w.Body.Bytes(), &panel); err != nil {
		t.Fatal(err)
	}
	if panel.ID != humanPanel || panel.Name != "Human-owned panel" || panel.CreatedBy != f.owner || panel.CreatedByAgentID != nil || panel.SourceTaskID != nil {
		t.Fatal("duplicate request changed a human-owned panel")
	}
}

func TestResourceWebPanelAgentBusyMembershipDoesNotWaitOnRunLock(t *testing.T) {
	f := newPanelRunFixture(t)
	tx, err := testPool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err = tx.Exec(t.Context(), `SELECT id FROM member WHERE workspace_id=$1 AND user_id=$2 FOR UPDATE`, f.workspace, f.owner); err != nil {
		t.Fatal(err)
	}
	f.call(t, "POST", "/api/agent/issue-web-panels", f.token, map[string]string{"name": "Busy member", "url": "https://example.test/member"}, 409)
}
