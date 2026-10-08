package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestForkReleaseByTagUsesOnlyForkRepository(t *testing.T) {
	const tag = "v0.6.1-overlay.1"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/chenbong/multica-team/releases/tags/"+tag {
			t.Errorf("unexpected release path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(GitHubRelease{TagName: tag})
	}))
	defer server.Close()
	t.Setenv(releaseAPIBaseURLEnv, server.URL)
	release, err := fetchReleaseByTag(tag)
	if err != nil || release.TagName != tag {
		t.Fatalf("fork release lookup: release=%v error=%v", release, err)
	}
}

func TestForkReleaseFailureDoesNotFallBackToUpstream(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/repos/chenbong/multica-team/releases/latest" {
			t.Errorf("unexpected fallback path: %s", r.URL.Path)
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	t.Setenv(releaseAPIBaseURLEnv, server.URL)
	if _, err := FetchLatestRelease(); err == nil {
		t.Fatal("missing fork release must fail")
	}
	if requests != 1 {
		t.Fatalf("got %d requests, want exactly one fork request", requests)
	}
}

func TestForkRefusesOfficialHomebrewUpdate(t *testing.T) {
	// Never resolve or execute the user's package manager, even on regression.
	t.Setenv("PATH", t.TempDir())
	output, err := UpdateViaBrew()
	if err == nil || !strings.Contains(err.Error(), "chenbong/multica-team/releases") || output != "" {
		t.Fatalf("expected a safe fork installation error, got output=%q error=%v", output, err)
	}
}
