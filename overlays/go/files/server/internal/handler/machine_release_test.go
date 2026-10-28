package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestMachineReleaseChoosesNewestStableAndCaches(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"tag_name": "v0.6.1-overlay.9", "published_at": "2026-10-27T01:00:00Z"},
			{"tag_name": "v0.6.1-overlay.12", "published_at": "2026-10-28T02:00:00Z", "prerelease": true},
			{"tag_name": "v0.6.1-overlay.11", "published_at": "2026-10-28T03:00:00Z", "draft": true},
			{"tag_name": "v0.6.1-overlay.10", "published_at": "2026-10-28T01:00:00Z"},
			{"tag_name": "v99.0.0", "published_at": "2026-10-29T01:00:00Z"},
		})
	}))
	defer server.Close()
	cache := &machineReleaseCache{}
	results := make(chan string, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			version, err := cache.latest(context.Background(), server.URL)
			if err != nil {
				results <- "error"
				return
			}
			results <- version
		}()
	}
	wg.Wait()
	close(results)
	for result := range results {
		if result != "v0.6.1-overlay.10" {
			t.Fatalf("wrong stable release: %q", result)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("release lookup made %d requests", calls.Load())
	}
}

func TestMachineReleaseEndpointReturnsPlainTagAndFailsClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{"tag_name": "v0.6.1-overlay.10", "published_at": "2026-10-28T01:00:00Z"}})
	}))
	t.Setenv("MULTICA_MACHINE_RELEASE_API_URL", server.URL)
	req := testutil.WithHeaders(testutil.JSONRequest("GET", "/api/tmesh/releases/latest", nil), "X-User-ID", testUserID)
	response := testutil.Call(t, testHandler.LatestMachineRelease, req).ResponseRecorder
	if response.Code != 200 || strings.TrimSpace(response.Body.String()) != "v0.6.1-overlay.10" || !strings.Contains(response.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("wrong release response: %d %s", response.Code, response.Body.String())
	}
	server.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer bad.Close()
	t.Setenv("MULTICA_MACHINE_RELEASE_API_URL", bad.URL)
	req = testutil.WithHeaders(testutil.JSONRequest("GET", "/api/tmesh/releases/latest", nil), "X-User-ID", testUserID)
	response = testutil.Call(t, testHandler.LatestMachineRelease, req).ResponseRecorder
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("failed lookup status %d", response.Code)
	}
}

func TestMachineReleaseFailuresAreBrieflyCached(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	cache := &machineReleaseCache{}
	for i := 0; i < 3; i++ {
		if _, err := cache.latest(context.Background(), server.URL); err == nil {
			t.Fatal("failed lookup unexpectedly succeeded")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("failed lookup made %d requests", calls.Load())
	}
}
