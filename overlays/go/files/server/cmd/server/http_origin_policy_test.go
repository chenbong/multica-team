package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/realtime"
)

func TestRouterCORSAllOriginsAndAttachmentFrames(t *testing.T) {
	t.Setenv("MULTICA_CORS_ALLOW_ALL_ORIGINS", "true")
	t.Setenv("MULTICA_ATTACHMENT_ALLOW_ALL_FRAME_ANCESTORS", "true")
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://old.example.test")
	router := NewRouter(nil, realtime.NewHub(), events.New(), analytics.NoopClient{}, nil)
	for _, origin := range []string{"http://new.example.test:8004", "https://arbitrary.example.test"} {
		r := httptest.NewRequest("OPTIONS", "/api/config", nil)
		r.Header.Set("Origin", origin)
		r.Header.Set("Access-Control-Request-Method", "POST")
		r.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type, X-Workspace-ID")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != 200 || w.Header().Get("Access-Control-Allow-Origin") != origin || w.Header().Get("Access-Control-Allow-Credentials") != "true" {
			t.Fatal("router CORS wiring missing", w.Code, w.Header())
		}
		r = httptest.NewRequest("GET", "/api/workspaces", nil)
		r.Header.Set("Origin", origin)
		w = httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatal("unauthenticated protected request admitted", w.Code)
		}
		if w.Header().Get("Access-Control-Allow-Origin") != origin {
			t.Fatal("CORS headers absent on auth rejection")
		}
	}
	r := httptest.NewRequest("GET", "/api/attachments/00000000-0000-0000-0000-000000000000/download", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors *;") {
		t.Fatal("attachment auth or frame policy changed", w.Code, w.Header())
	}
	r = httptest.NewRequest("GET", "/api/config", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none';") {
		t.Fatal("non-attachment frame policy changed")
	}
}
