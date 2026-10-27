package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIssueLinkOriginsPublicConfiguration(t *testing.T) {
	t.Setenv("TEAM_MESH_ISSUE_LINK_ORIGINS", "http://mesh.example:8004/,https://user:pass@invalid.example,https://invalid.example/path,javascript:alert(1),https://second.example")
	w := httptest.NewRecorder()
	testHandler.GetConfig(w, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	var cfg AppConfig
	if err := json.Unmarshal(w.Body.Bytes(), &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.IssueLinkOrigins) != 2 || cfg.IssueLinkOrigins[0] != "http://mesh.example:8004" || cfg.IssueLinkOrigins[1] != "https://second.example" {
		t.Fatalf("unexpected origins: %#v", cfg.IssueLinkOrigins)
	}
}
