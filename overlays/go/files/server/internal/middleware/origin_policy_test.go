package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/cors"
)

func TestCORSAllOriginsReflectsCredentialedRequestsAndPreservesDefaults(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "enabled"}[enabled], func(t *testing.T) {
			t.Setenv("MULTICA_CORS_ALLOW_ALL_ORIGINS", map[bool]string{false: "false", true: "true"}[enabled])
			options := CORSOriginPolicy(cors.Options{AllowedOrigins: []string{"https://known.example.test"}, AllowedMethods: []string{"GET", "POST", "OPTIONS"}, AllowedHeaders: []string{"Authorization", "Content-Type"}, ExposedHeaders: []string{"Etag"}, AllowCredentials: true, MaxAge: 300})
			for _, origin := range []string{"https://known.example.test", "http://new.example.test:8004", "https://unlisted.example.test", "null"} {
				called := false
				h := cors.Handler(options)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called = true; w.WriteHeader(http.StatusUnauthorized) }))
				for _, method := range []string{http.MethodOptions, http.MethodGet} {
					r := httptest.NewRequest(method, "http://api.internal/api/private", nil)
					r.Header.Set("Origin", origin)
					if method == http.MethodOptions {
						r.Header.Set("Access-Control-Request-Method", "POST")
						r.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type")
					}
					w := httptest.NewRecorder()
					h.ServeHTTP(w, r)
					want := ""
					if enabled || origin == "https://known.example.test" {
						want = origin
					}
					if w.Header().Get("Access-Control-Allow-Origin") != want {
						t.Fatalf("origin %q: headers %v", origin, w.Header())
					}
					if want != "" && w.Header().Get("Access-Control-Allow-Credentials") != "true" {
						t.Fatal("credentialed CORS missing")
					}
					if method == http.MethodGet && (!called || w.Code != http.StatusUnauthorized) {
						t.Fatal("origin policy bypassed authentication")
					}
					if !strings.Contains(strings.Join(w.Header().Values("Vary"), ","), "Origin") {
						t.Fatal("responses must vary by Origin")
					}
				}
			}
			r := httptest.NewRequest("OPTIONS", "http://api.internal/api/private", nil)
			r.Header.Set("Origin", "https://unlisted.example.test")
			r.Header.Set("Access-Control-Request-Method", "TRACE")
			w := httptest.NewRecorder()
			cors.Handler(options)(http.NotFoundHandler()).ServeHTTP(w, r)
			if w.Header().Get("Access-Control-Allow-Methods") != "" {
				t.Fatal("unapproved method allowed")
			}
		})
	}
}

func TestAttachmentWildcardOnlyChangesAttachmentFrameAncestors(t *testing.T) {
	t.Setenv("MULTICA_ATTACHMENT_ALLOW_ALL_FRAME_ANCESTORS", "true")
	for _, path := range []string{"/api/attachments/fixture/download", "/api/attachments/fixture/content", "/api/config"} {
		r := httptest.NewRequest("GET", path, nil)
		header := contentSecurityPolicyForRequest(r)
		if strings.HasPrefix(path, "/api/attachments/") {
			if !strings.Contains(header, "frame-ancestors *;") {
				t.Fatal("attachment policy not unrestricted", header)
			}
		} else if !strings.Contains(header, "frame-ancestors 'none';") {
			t.Fatal("unrelated route policy weakened", header)
		}
		for _, directive := range []string{"object-src 'none'", "base-uri 'self'", "form-action 'self'"} {
			if !strings.Contains(header, directive) {
				t.Fatal("non-frame directive changed", header)
			}
		}
	}
}
