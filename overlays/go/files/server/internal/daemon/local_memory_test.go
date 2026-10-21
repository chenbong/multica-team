package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeMemoryFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverAndReadRuntimeLocalMemory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("QWEN_HOME", "")
	t.Setenv("HERMES_HOME", filepath.Join(home, "no-hermes"))

	writeMemoryFixture(t, filepath.Join(home, ".claude", "CLAUDE.md"), "global rules")
	writeMemoryFixture(t, filepath.Join(home, ".claude", "projects", "-home-me-repo", "memory", "MEMORY.md"), "index")
	writeMemoryFixture(t, filepath.Join(home, ".claude", "projects", "-home-me-repo", "memory", "topic", "build.md"), "pnpm")
	writeMemoryFixture(t, filepath.Join(home, ".claude", "projects", "-home-me-repo", "session.jsonl"), "{}")
	writeMemoryFixture(t, filepath.Join(home, ".codex", "AGENTS.md"), "codex rules")
	writeMemoryFixture(t, filepath.Join(home, ".codex", "memories", "raw.md"), "codex memory")
	writeMemoryFixture(t, filepath.Join(home, ".gemini", "GEMINI.md"), strings.Repeat("g", int(maxLocalMemoryFileSize)+1))
	writeMemoryFixture(t, filepath.Join(home, ".ssh", "id_rsa"), "secret")

	entries, err := listRuntimeLocalMemory("codex")
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]runtimeLocalMemorySummary{}
	for _, e := range entries {
		byKey[e.Key] = e
		if strings.Contains(e.Key, ".ssh") || strings.HasSuffix(e.Key, ".jsonl") {
			t.Fatalf("discovery leaked a non-memory file: %s", e.Key)
		}
	}
	if len(entries) == 0 || entries[0].Tool != "codex" {
		t.Fatalf("runtime's own tool should sort first: %+v", entries)
	}
	want := map[string]string{
		"~/.claude/CLAUDE.md":                                    "claude/CLAUDE.md",
		"~/.claude/projects/-home-me-repo/memory/MEMORY.md":      "claude/projects/home-me-repo/MEMORY.md",
		"~/.claude/projects/-home-me-repo/memory/topic/build.md": "claude/projects/home-me-repo/topic/build.md",
		"~/.codex/AGENTS.md":                                     "codex/AGENTS.md",
		"~/.codex/memories/raw.md":                               "codex/memories/raw.md",
		"~/.gemini/GEMINI.md":                                    "gemini/GEMINI.md",
	}
	for key, suggested := range want {
		e, ok := byKey[key]
		if !ok {
			t.Fatalf("missing %s in %+v", key, entries)
		}
		if e.SuggestedPath != suggested {
			t.Errorf("%s suggested %q, want %q", key, e.SuggestedPath, suggested)
		}
	}
	if !byKey["~/.gemini/GEMINI.md"].TooLarge {
		t.Error("oversized file should be flagged too_large")
	}

	files, errs, err := readRuntimeLocalMemory("claude", []string{
		"~/.claude/CLAUDE.md",
		"~/.ssh/id_rsa",
		"~/.gemini/GEMINI.md",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Content != "global rules" || files[0].SuggestedPath != "claude/CLAUDE.md" {
		t.Fatalf("files = %+v", files)
	}
	if len(errs) != 2 {
		t.Fatalf("expected errors for the undiscovered and oversized keys, got %+v", errs)
	}
}

func TestHandleLocalMemoryReportsReadResult(t *testing.T) {
	withFastLocalSkillReportBackoffs(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HERMES_HOME", filepath.Join(home, "no-hermes"))
	writeMemoryFixture(t, filepath.Join(home, ".claude", "CLAUDE.md"), "be concise")

	var gotPath string
	var got struct {
		Status string                        `json:"status"`
		Files  []runtimeLocalMemoryFile      `json:"files"`
		Errors []runtimeLocalMemoryReadError `json:"errors"`
	}
	d, _ := localSkillReportDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode report: %v", err)
		}
		w.Write([]byte(`{"status":"ok"}`))
	})

	d.handleLocalMemory(context.Background(), Runtime{ID: "rt-1", Provider: "claude"}, PendingLocalMemory{
		ID: "req-1", Kind: "read", Keys: []string{"~/.claude/CLAUDE.md", "~/.bashrc"},
	})

	if gotPath != "/api/daemon/runtimes/rt-1/local-memory/req-1/result" {
		t.Fatalf("report path = %q", gotPath)
	}
	if got.Status != "completed" || len(got.Files) != 1 || got.Files[0].Content != "be concise" {
		t.Fatalf("report = %+v", got)
	}
	if len(got.Errors) != 1 || got.Errors[0].Key != "~/.bashrc" {
		t.Fatalf("undiscovered key must be refused, got %+v", got.Errors)
	}
}

func TestMemorySegment(t *testing.T) {
	cases := map[string]string{
		"-home-me-repo":          "home-me-repo",
		".hidden":                "hidden",
		"a:b*c":                  "a-b-c",
		"":                       "project",
		"中文":                     "project",
		strings.Repeat("ab", 50): strings.Repeat("ab", 30),
	}
	for in, want := range cases {
		if got := memorySegment(in); got != want {
			t.Errorf("memorySegment(%q) = %q, want %q", in, got, want)
		}
	}
}
