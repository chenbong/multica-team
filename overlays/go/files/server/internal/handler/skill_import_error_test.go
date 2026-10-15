package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGitHubSkillImport_FetchFailureClassification(t *testing.T) {
	t.Setenv(skillImportProxyEnv, "")
	for _, source := range []string{"https://github.com/fixture/repo", "https://github.com/fixture/repo/tree/main/skill"} {
		t.Run(source, func(t *testing.T) {
			for _, tc := range []struct {
				name, message      string
				upstream, response int
				cause              error
				headers            http.Header
				body               io.ReadCloser
			}{
				{name: "not_found", upstream: 404, response: 502, message: "SKILL.md not found"},
				{name: "network", cause: errors.New("proxyconnect tcp: http://fixture-user:fixture-secret@proxy.invalid"), response: 502, message: "network request failed"},
				{name: "canceled", cause: context.Canceled, response: 502, message: "canceled"},
				{name: "deadline", cause: context.DeadlineExceeded, response: 504, message: "timed out"},
				{name: "client_timeout", cause: &net.DNSError{Name: "proxy.invalid", IsTimeout: true}, response: 504, message: "timed out"},
				{name: "unauthorized", upstream: 401, response: 502, message: "GITHUB_TOKEN"},
				{name: "forbidden", upstream: 403, response: 502, message: "repository permissions"},
				{name: "rate_limit", upstream: 429, response: 429, message: "rate limit"},
				{name: "rate_limit_403", upstream: 403, headers: http.Header{"X-Ratelimit-Remaining": {"0"}}, response: 429, message: "rate limit"},
				{name: "unavailable", upstream: 503, response: 503, message: "retry later"},
				{name: "body_network", upstream: 200, body: skillImportFailingBody{err: errors.New("fixture-secret@proxy.invalid")}, response: 502, message: "network request failed"},
				{name: "file_cap", upstream: 200, body: io.NopCloser(strings.NewReader(strings.Repeat("x", maxImportFileSize+1))), response: 413, message: "byte limit"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					client := &http.Client{Transport: skillImportFixtureRoundTripper(func(r *http.Request) (*http.Response, error) {
						if r.URL.Hostname() == "api.github.com" {
							if strings.Contains(r.URL.Path, "/commits/") {
								if strings.HasSuffix(r.URL.Path, "/commits/main") {
									return skillImportFixtureResponse(r, 200, "fixture-sha"), nil
								}
								return skillImportFixtureResponse(r, 404, ""), nil
							}
							return skillImportFixtureResponse(r, 200, `{"default_branch":"main"}`), nil
						}
						if tc.cause != nil {
							return nil, tc.cause
						}
						resp := skillImportFixtureResponse(r, tc.upstream, "")
						resp.Header = tc.headers
						if tc.body != nil {
							resp.Body = tc.body
						}
						return resp, nil
					})}
					_, err := fetchFromGitHub(t.Context(), client, source)
					if err == nil {
						t.Fatal("expected a fetch failure")
					}
					if got := strings.Contains(err.Error(), "SKILL.md not found"); got != (tc.upstream == 404) {
						t.Fatalf("only HTTP 404 may be reported as missing SKILL.md: %v", err)
					}
					status, msg := importFetchErrorResponse(t.Context(), err)
					if status != tc.response || !strings.Contains(msg, tc.message) {
						t.Fatalf("response = %d %q, want %d containing %q", status, msg, tc.response, tc.message)
					}
					for _, secret := range []string{"fixture-user", "fixture-secret", "proxy.invalid"} {
						if strings.Contains(msg, secret) {
							t.Fatalf("error leaked proxy details: %s", msg)
						}
					}
					if tc.cause != nil && !errors.Is(err, tc.cause) {
						t.Fatal("underlying error identity was lost")
					}
					if tc.name == "file_cap" && !isCapError(err) {
						t.Fatal("file cap identity was lost")
					}
				})
			}
		})
	}
}

func TestGitHubSkillImport_InvalidProxyCoversImportAndRefreshFetches(t *testing.T) {
	t.Setenv(skillImportProxyEnv, "http://fixture-user:fixture-secret@proxy.invalid:bad")
	for _, origin := range []skillOriginRef{
		{Type: "github", SourceURL: "https://github.com/fixture/repo"},
		{Type: "skills_sh", SourceURL: "https://skills.sh/fixture/repo/skill"},
	} {
		t.Run(origin.Type, func(t *testing.T) {
			// Refresh's client is constructed outside skill.go; both fetch paths
			// must still pick up the import-only proxy without a global change.
			_, err := fetchImportedSkillFromOrigin(t.Context(), &http.Client{}, origin)
			if !errors.Is(err, errSkillImportProxyConfig) {
				t.Fatalf("proxy config error = %v", err)
			}
			status, msg := importFetchErrorResponse(t.Context(), err)
			if status != 500 || !strings.Contains(msg, skillImportProxyEnv) || strings.Contains(msg, "fixture-secret") || strings.Contains(msg, "proxy.invalid") {
				t.Fatalf("proxy config response = %d %q", status, msg)
			}
		})
	}
}

func TestGitHubSkillImport_ProxyFetchesGitHubAndSkillsShOrigins(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "api.github.com" {
			if strings.Contains(r.URL.Path, "/git/trees/") {
				writeJSON(w, http.StatusOK, githubTreeResponse{Tree: []githubTreeEntry{{Path: "skills/demo/SKILL.md", Type: "blob"}}})
				return
			}
			writeJSON(w, http.StatusOK, githubRepoInfo{DefaultBranch: "main"})
			return
		}
		if r.Host == "raw.githubusercontent.com" {
			w.Write([]byte("---\nname: demo\n---\nfixture body"))
			return
		}
		t.Errorf("unexpected fake origin host: %s", r.Host)
		http.NotFound(w, r)
	}))
	t.Cleanup(origin.Close)
	proxy := newSkillImportConnectProxy(t, origin.Listener.Addr().String())
	t.Setenv(skillImportProxyEnv, proxy.URL)
	t.Setenv("GITHUB_TOKEN", "")
	base := origin.Client().Transport.(*http.Transport).Clone()
	base.TLSClientConfig.ServerName, _, _ = net.SplitHostPort(origin.Listener.Addr().String())
	for _, ref := range []skillOriginRef{
		{Type: "github", SourceURL: "https://github.com/fixture/repo"},
		{Type: "skills_sh", SourceURL: "https://skills.sh/fixture/repo/demo"},
	} {
		t.Run(ref.Type, func(t *testing.T) {
			client := &http.Client{Transport: base, Timeout: 2 * time.Second}
			var imported *importedSkill
			var err error
			if ref.Type == "github" {
				imported, err = fetchFromGitHub(t.Context(), client, ref.SourceURL)
			} else {
				imported, err = fetchFromSkillsSh(t.Context(), client, ref.SourceURL)
			}
			if err != nil || imported.name != "demo" {
				t.Fatalf("import proxy fetch = %+v, %v", imported, err)
			}
			refreshed, err := fetchImportedSkillFromOrigin(t.Context(), client, ref)
			if err != nil || refreshed.content != imported.content {
				t.Fatalf("refresh proxy fetch = %+v, %v", refreshed, err)
			}
		})
	}
}

func TestGitHubSkillImport_AuthOnlyReachesExistingAllowedHosts(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "fixture-github-token")
	for _, tc := range []struct {
		host string
		auth bool
	}{
		{"api.github.com", true}, {"raw.githubusercontent.com", true}, {"API.GITHUB.COM", true},
		{"github.com", false}, {"codeload.github.com", false}, {"clawhub.ai", false},
		{"service.internal.invalid", false}, {"api.github.com.evil.invalid", false}, {"child.raw.githubusercontent.com", false},
	} {
		t.Run(tc.host, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, "https://"+tc.host+"/fixture", nil)
			addGitHubAuthHeader(req)
			want := ""
			if tc.auth {
				want = "Bearer fixture-github-token"
			}
			if req.Header.Get("Authorization") != want {
				t.Fatalf("unexpected auth for %s", tc.host)
			}
			client := &http.Client{Transport: skillImportFixtureRoundTripper(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Authorization") != want {
					t.Errorf("API request leaked auth to %s", tc.host)
				}
				return skillImportFixtureResponse(r, 200, ""), nil
			})}
			resp, err := doGitHubAPIGet(t.Context(), client, req.URL.String())
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
		})
	}
}

func TestGitHubSkillImport_APITransportErrorDoesNotLeakProxy(t *testing.T) {
	t.Setenv(skillImportProxyEnv, "")
	cause := errors.New("http://fixture-user:fixture-secret@proxy.invalid")
	client := &http.Client{Transport: skillImportFixtureRoundTripper(func(r *http.Request) (*http.Response, error) { return nil, cause })}
	_, _, err := fetchGitHubTree(t.Context(), client, "fixture", "repo", "main")
	if !errors.Is(err, cause) || strings.Contains(err.Error(), "fixture-secret") {
		t.Fatalf("API error = %v", err)
	}
	_, err = fetchFromGitHub(context.Background(), client, "https://github.com/fixture/repo/tree/main/skill")
	if err == nil || strings.Contains(err.Error(), "fixture-secret") || strings.Contains(err.Error(), "not found") {
		t.Fatalf("ref error = %v", err)
	}
}

type skillImportFixtureRoundTripper func(*http.Request) (*http.Response, error)

func (f skillImportFixtureRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func skillImportFixtureResponse(r *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Status: fmt.Sprint(status), Request: r, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
