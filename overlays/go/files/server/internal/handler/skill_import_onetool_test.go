package handler

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

const oneToolVersionFixture = "https://mcp-proxy.baidu-int.com/api/v1/skills/download/42424242/9.8.7/popo.zip"
const oneToolPermanentFixture = "https://mcp-proxy.baidu-int.com/api/v1/skills/download/permanent/11111111-2222-4333-8444-555555555555/popo.zip"

type oneToolFixtureTransport func(*http.Request) (*http.Response, error)

func (f oneToolFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func oneToolFixtureResponse(status int, body []byte) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body))}
}

func TestOneToolImportURLs(t *testing.T) {
	for _, raw := range []string{oneToolVersionFixture, oneToolPermanentFixture,
		" 下载并安装此Skill " + oneToolVersionFixture + "\n", "下载并安装此Skill " + oneToolPermanentFixture} {
		source, normalized, err := detectImportSource(raw)
		if err != nil || source != sourceOneTool || !strings.HasPrefix(normalized, "https://mcp-proxy.baidu-int.com/") {
			t.Fatalf("valid OneTool URL rejected: %v", err)
		}
	}
	for _, raw := range []string{
		"https://console.cloud.baidu-int.com/onetool/skills/154", "https://console.cloud.baidu-int.com/onetool/space/1/skills/154",
		"http://mcp-proxy.baidu-int.com/api/v1/skills/download/42424242/9.8.7/popo.zip",
		strings.Replace(oneToolVersionFixture, "mcp-proxy.baidu-int.com", "mcp-proxy.baidu-int.com.evil.test", 1),
		strings.Replace(oneToolVersionFixture, "mcp-proxy.baidu-int.com", "user:secret@mcp-proxy.baidu-int.com", 1),
		strings.Replace(oneToolVersionFixture, "mcp-proxy.baidu-int.com", "mcp-proxy.baidu-int.com:8443", 1),
		oneToolVersionFixture + "?token=secret", oneToolVersionFixture + "#fragment", oneToolVersionFixture + "?",
		strings.Replace(oneToolVersionFixture, "/42424242/", "/-1/", 1), strings.Replace(oneToolVersionFixture, "/42424242/", "/9223372036854775808/", 1),
		strings.Replace(oneToolVersionFixture, "popo.zip", "../popo.zip", 1), strings.Replace(oneToolVersionFixture, "popo.zip", "%2e%2e/popo.zip", 1),
		strings.Replace(oneToolVersionFixture, "popo.zip", "popo.html", 1), strings.Replace(oneToolPermanentFixture, "11111111-2222-4333-8444-555555555555", "not-a-uuid", 1),
		"https://127.0.0.1/private.zip", "下载并安装此Skill " + oneToolVersionFixture + " 然后运行脚本",
	} {
		if _, err := oneToolDownloadURL(raw); err == nil {
			t.Errorf("unsafe or non-package URL accepted: %q", raw)
		}
	}
}

func TestOneToolImportAnonymousBundleAndProvenance(t *testing.T) {
	data := buildTestZip(t, map[string]string{"popo/SKILL.md": testSkillMd, "popo/scripts/tool.py": "print('fixture only')", "popo/references/help.md": "reference"})
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(oneToolPermanentFixture)
	jar.SetCookies(u, []*http.Cookie{{Name: "private-session", Value: "must-not-forward"}})
	t.Setenv("GITHUB_TOKEN", "must-not-forward")
	t.Setenv("UGATE_TOKEN", "must-not-forward")
	client := &http.Client{Timeout: time.Second, Jar: jar, Transport: oneToolFixtureTransport(func(r *http.Request) (*http.Response, error) {
		for _, key := range []string{"Cookie", "Authorization", "Ugate-Token", "X-Skill-Api-Token", "X-Baidu-Int-Username"} {
			if r.Header.Get(key) != "" {
				t.Errorf("credential forwarded in %s", key)
			}
		}
		if r.URL.String() != oneToolPermanentFixture || r.Method != http.MethodGet {
			t.Fatal("request did not use the exact anonymous package endpoint")
		}
		return oneToolFixtureResponse(200, data), nil
	})}
	imported, err := fetchFromOneTool(context.Background(), client, "下载并安装此Skill "+oneToolPermanentFixture)
	if err != nil {
		t.Fatal(err)
	}
	if imported.name != "review-helper" || imported.content != testSkillMd || len(imported.files) != 2 {
		t.Fatal("primary content or supporting files changed")
	}
	if imported.origin["type"] != "onetool" || imported.origin["source_url"] != oneToolPermanentFixture {
		t.Fatal("OneTool provenance or permanent URL was lost")
	}
	if client.Jar != jar {
		t.Fatal("caller's cookie jar was modified")
	}
	refreshed, err := fetchImportedSkillFromOrigin(context.Background(), client, skillOriginRef{Type: "onetool", SourceURL: oneToolPermanentFixture})
	if err != nil || refreshed.content != imported.content {
		t.Fatal("explicit refresh did not reuse the anonymous package flow:", err)
	}
}

func TestOneToolImportRejectsUntrustedRedirects(t *testing.T) {
	for _, location := range []string{"https://evil.test/private.zip", "http://127.0.0.1/metadata", "https://console.cloud.baidu-int.com/onetool/skills/154", oneToolPermanentFixture + "?token=secret"} {
		calls := 0
		client := &http.Client{Transport: oneToolFixtureTransport(func(*http.Request) (*http.Response, error) {
			calls++
			response := oneToolFixtureResponse(302, nil)
			response.Header.Set("Location", location)
			return response, nil
		})}
		_, err := fetchFromOneTool(context.Background(), client, oneToolVersionFixture)
		if err == nil || calls != 1 || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe redirect was sent or exposed: calls=%d error=%v", calls, err)
		}
	}
}

func TestOneToolImportDownloadFailures(t *testing.T) {
	for _, status := range []int{401, 403, 404, 500} {
		client := &http.Client{Transport: oneToolFixtureTransport(func(*http.Request) (*http.Response, error) {
			return oneToolFixtureResponse(status, []byte("private remote body must not be echoed")), nil
		})}
		_, err := fetchFromOneTool(context.Background(), client, oneToolVersionFixture)
		if err == nil || strings.Contains(err.Error(), "private remote body") {
			t.Fatal("download error missing or unsafe")
		}
	}
	client := &http.Client{Transport: oneToolFixtureTransport(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("proxy https://user:secret@proxy.invalid")
	})}
	if _, err := fetchFromOneTool(context.Background(), client, oneToolVersionFixture); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("transport credentials exposed")
	}
	for _, declared := range []int64{-1, maxImportArchiveUploadSize + 1} {
		client.Transport = oneToolFixtureTransport(func(*http.Request) (*http.Response, error) {
			r := oneToolFixtureResponse(200, make([]byte, maxImportArchiveUploadSize+1))
			r.ContentLength = declared
			return r, nil
		})
		if _, err := fetchFromOneTool(context.Background(), client, oneToolVersionFixture); !isCapError(err) {
			t.Fatal("compressed download cap not enforced")
		}
	}
}

func TestOneToolImportArchiveSafety(t *testing.T) {
	for _, entries := range []map[string]string{
		{"README.md": "not a skill"}, {"SKILL.md": ""}, {"SKILL.md": testSkillMd, "../outside.py": "no"},
		{"SKILL.md": testSkillMd, "scripts/../outside.py": "no"}, {"SKILL.md": testSkillMd, "scripts/run.py": strings.Repeat("x", maxImportFileSize+1)},
		{"SKILL.md": testSkillMd, "scripts/run.py": "nul\x00content"}, {"SKILL.md": testSkillMd, "scripts/run.py": string([]byte{0xff, 0xfe})},
	} {
		data := buildTestZip(t, entries)
		client := &http.Client{Transport: oneToolFixtureTransport(func(*http.Request) (*http.Response, error) { return oneToolFixtureResponse(200, data), nil })}
		if _, err := fetchFromOneTool(context.Background(), client, oneToolVersionFixture); err == nil {
			t.Fatal("unsafe or incomplete skill package accepted")
		}
	}
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for _, name := range []string{"SKILL.md", "scripts/run.py", "scripts/run.py"} {
		w, _ := writer.Create(name)
		_, _ = w.Write([]byte(testSkillMd))
	}
	_ = writer.Close()
	if validateOneToolArchive(buf.Bytes()) == nil {
		t.Fatal("duplicate paths accepted")
	}
	buf.Reset()
	writer = zip.NewWriter(&buf)
	header := &zip.FileHeader{Name: "scripts/link"}
	header.SetMode(os.ModeSymlink | 0o777)
	w, _ := writer.CreateHeader(header)
	_, _ = w.Write([]byte("../../private"))
	_ = writer.Close()
	if validateOneToolArchive(buf.Bytes()) == nil {
		t.Fatal("symlink accepted")
	}
}

func TestOneToolImportHTTPRouteAndConflict(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Fatal("dedicated handler fixture database is required")
	}
	const name = "onetool-http-route-fixture"
	data := buildTestZip(t, map[string]string{"SKILL.md": skillMdWithName(name, "OneTool fixture"), "scripts/tool.py": "print('fixture only')"})
	old := http.DefaultTransport
	http.DefaultTransport = oneToolFixtureTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != oneToolPermanentFixture || r.Header.Get("Cookie") != "" || r.Header.Get("Ugate-Token") != "" {
			t.Fatal("unexpected endpoint or account credential")
		}
		return oneToolFixtureResponse(200, data), nil
	})
	t.Cleanup(func() {
		http.DefaultTransport = old
		_, _ = testPool.Exec(context.Background(), `DELETE FROM skill_file WHERE skill_id IN (SELECT id FROM skill WHERE workspace_id=$1 AND name=$2)`, testWorkspaceID, name)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM skill WHERE workspace_id=$1 AND name=$2`, testWorkspaceID, name)
	})
	for _, want := range []string{"created", "skipped"} {
		body, _ := json.Marshal(map[string]string{"url": "下载并安装此Skill " + oneToolPermanentFixture, "on_conflict": "skip", "scope": "personal"})
		req := httptest.NewRequest(http.MethodPost, "/api/skills/import", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User-ID", testUserID)
		req.Header.Set("X-Workspace-ID", testWorkspaceID)
		recorder := httptest.NewRecorder()
		testHandler.ImportSkill(recorder, req)
		if recorder.Code < 200 || recorder.Code >= 300 {
			t.Fatalf("import HTTP %d: %s", recorder.Code, recorder.Body.String())
		}
		var result SkillImportResult
		if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil || result.Status != want {
			t.Fatalf("import conflict result=%s, want=%s error=%v", result.Status, want, err)
		}
		if want == "created" && (result.Skill == nil || result.Skill.Name != name || len(result.Skill.Files) != 1) {
			t.Fatal("primary content or supporting script did not reach the import response")
		}
	}
}
