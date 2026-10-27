package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestProjectWorkingDirectoryResourceLifecycle(t *testing.T) {
	w := httptest.NewRecorder()
	testHandler.CreateProject(w, newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{"title": "Optional directory test"}))
	if w.Code != 201 {
		t.Fatalf("create project: %d %s", w.Code, w.Body.String())
	}
	var p ProjectResponse
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	defer func() {
		req := withURLParam(newRequest("DELETE", "/api/projects/"+p.ID, nil), "id", p.ID)
		testHandler.DeleteProject(httptest.NewRecorder(), req)
	}()
	request := func(method string, body any, id string) *httptest.ResponseRecorder {
		req := withURLParam(newRequest(method, "/api/projects/"+p.ID+"/resources", body), "id", p.ID)
		if id != "" {
			req = withURLParams(req, "id", p.ID, "resourceId", id)
		}
		out := httptest.NewRecorder()
		switch method {
		case "POST":
			testHandler.CreateProjectResource(out, req)
		case "PUT":
			testHandler.UpdateProjectResource(out, req)
		case "GET":
			testHandler.ListProjectResources(out, req)
		case "DELETE":
			testHandler.DeleteProjectResource(out, req)
		}
		return out
	}
	body := func(path string) map[string]any {
		return map[string]any{"resource_type": "working_directory", "resource_ref": map[string]string{"local_path": path}}
	}
	created := request("POST", body("/srv/project"), "")
	if created.Code != 201 {
		t.Fatalf("create directory: %d %s", created.Code, created.Body.String())
	}
	var resource ProjectResourceResponse
	if err := json.Unmarshal(created.Body.Bytes(), &resource); err != nil {
		t.Fatal(err)
	}
	if resource.ResourceType != "working_directory" {
		t.Fatal("wrong resource type")
	}
	if got := request("POST", body("/srv/other"), ""); got.Code != http.StatusConflict {
		t.Fatalf("second directory accepted: %d %s", got.Code, got.Body.String())
	}
	updated := request("PUT", map[string]any{"resource_ref": map[string]string{"local_path": "/srv/new"}}, resource.ID)
	if updated.Code != 200 {
		t.Fatalf("update: %d %s", updated.Code, updated.Body.String())
	}
	var updatedResource ProjectResourceResponse
	_ = json.Unmarshal(updated.Body.Bytes(), &updatedResource)
	if updatedResource.ID != resource.ID || string(updatedResource.ResourceRef) != `{"local_path":"/srv/new"}` {
		t.Fatalf("update did not retain identity: %s", updated.Body.String())
	}
	if got := request("GET", nil, ""); got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	if got := request("DELETE", nil, resource.ID); got.Code != 204 && got.Code != 200 {
		t.Fatalf("delete: %d %s", got.Code, got.Body.String())
	}
	// Two simultaneous saves of different paths must still leave at most one
	// project preference; the partial unique index closes the check/write race.
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for _, path := range []string{"/srv/a", "/srv/b"} {
		wg.Add(1)
		go func(path string) { defer wg.Done(); codes <- request("POST", body(path), "").Code }(path)
	}
	wg.Wait()
	close(codes)
	success, conflict := 0, 0
	for code := range codes {
		if code == 201 {
			success++
		} else if code == 409 {
			conflict++
		} else {
			t.Fatalf("unexpected concurrent result %d", code)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("concurrent saves %d success %d conflict", success, conflict)
	}
}

func TestCreateProjectRejectsDuplicateWorkingDirectory(t *testing.T) {
	resource := map[string]any{"resource_type": "working_directory", "resource_ref": map[string]string{"local_path": "/srv/project"}}
	w := httptest.NewRecorder()
	testHandler.CreateProject(w, newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{"title": "Invalid directory batch", "resources": []any{resource, resource}}))
	if w.Code != 400 {
		t.Fatalf("duplicate batch: %d %s", w.Code, w.Body.String())
	}
}
