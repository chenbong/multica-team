package handler

import (
	"encoding/json"
	"testing"
)

func TestResourceWebPanelAgentCRUD(t *testing.T) {
	f := newPanelRunFixture(t)
	var item resourceWebPanel
	w := f.call(t, "POST", "/api/agent/issue-web-panels", f.token, map[string]string{"url": "https://example.test/first"}, 201)
	if err := json.Unmarshal(w.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	f.call(t, "GET", "/api/agent/issue-web-panels", f.token, nil, 200)
	var changed resourceWebPanel
	w = f.call(t, "PATCH", "/api/agent/issue-web-panels/"+item.ID, f.token, map[string]string{"url": "https://example.test/second#result"}, 200)
	if err := json.Unmarshal(w.Body.Bytes(), &changed); err != nil {
		t.Fatal(err)
	}
	if changed.Name != changed.URL || changed.URL != "https://example.test/second#result" {
		t.Fatal("URL/name did not update")
	}
	f.call(t, "PATCH", "/api/agent/issue-web-panels/"+item.ID, f.token, map[string]string{"url": "javascript:alert(1)"}, 400)
	other := f.f.Issue(t, "Other panel issue")
	f.f.Exec(t, `UPDATE resource_web_panel SET issue_id=$2 WHERE id=$1`, item.ID, other)
	f.f.Cleanup(t, `DELETE FROM resource_web_panel WHERE id=$1`, item.ID)
	f.call(t, "PATCH", "/api/agent/issue-web-panels/"+item.ID, f.token, map[string]string{"name": "no"}, 404)
	f.call(t, "DELETE", "/api/agent/issue-web-panels/"+item.ID, f.token, nil, 404)
	f.f.Exec(t, `UPDATE resource_web_panel SET issue_id=$2 WHERE id=$1`, item.ID, f.issue)
	f.call(t, "DELETE", "/api/agent/issue-web-panels/"+item.ID, f.token, nil, 204)
	f.call(t, "PATCH", "/api/agent/issue-web-panels/"+item.ID, f.token, map[string]string{"name": "missing"}, 404)
	f.f.Exec(t, `UPDATE agent_task_queue SET status='completed' WHERE id=$1`, f.task)
	f.call(t, "GET", "/api/agent/issue-web-panels", f.token, nil, 403)
}
