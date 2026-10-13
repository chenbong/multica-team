package daemon

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryWritebackCollectsCopiesAndRuntimeMemory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	workDir := t.TempDir()
	task := Task{Agent: &AgentData{
		Memory: []AgentMemoryFileData{
			{Path: "keep.md", Content: "same"},
			{Path: "edit.md", Content: "old"},
			{Path: "drop.md", Content: "bye"},
			{Path: "auto/MEMORY.md", Content: "accepted native"},
		},
		MemoryWriteback: &AgentMemoryWritebackData{Mode: "review", CaptureRuntime: true},
	}}
	mem := filepath.Join(workDir, ".multica", "memory")
	writeTestFile(t, filepath.Join(mem, "keep.md"), "same")
	writeTestFile(t, filepath.Join(mem, "edit.md"), "new")
	writeTestFile(t, filepath.Join(mem, "auto", "MEMORY.md"), "accepted native")
	writeTestFile(t, filepath.Join(mem, "notes", "added.md"), "added")
	writeTestFile(t, filepath.Join(mem, "bin.md"), "\x00binary")

	wb := newMemoryWriteback(task, "claude", workDir, true, slog.Default())
	if wb == nil || wb.nativeDir == "" {
		t.Fatalf("write-back not prepared: %+v", wb)
	}
	// The accepted auto/ file was seeded into the runtime's own memory dir.
	seeded, err := os.ReadFile(filepath.Join(wb.nativeDir, "MEMORY.md"))
	if err != nil || string(seeded) != "accepted native" {
		t.Fatalf("seeded native memory = %q, %v", seeded, err)
	}
	// The runtime extends it and adds a topic file during the run.
	writeTestFile(t, filepath.Join(wb.nativeDir, "MEMORY.md"), "accepted native\n- learned more")
	writeTestFile(t, filepath.Join(wb.nativeDir, "build.md"), "use pnpm")

	got := map[string]agentMemoryChange{}
	for _, c := range wb.collect() {
		got[c.Path] = c
	}
	want := map[string]string{
		"edit.md":        "upsert/agent",
		"notes/added.md": "upsert/agent",
		"drop.md":        "delete/agent",
		"auto/MEMORY.md": "upsert/runtime",
		"auto/build.md":  "upsert/runtime",
	}
	if len(got) != len(want) {
		t.Fatalf("changes = %+v", got)
	}
	for p, kind := range want {
		c, ok := got[p]
		if !ok || c.Action+"/"+c.Origin != kind {
			t.Fatalf("change %s = %+v, want %s", p, c, kind)
		}
	}
	if c := got["edit.md"]; c.BaseContent == nil || *c.BaseContent != "old" || c.Content != "new" {
		t.Fatalf("edit.md base/content = %+v", c)
	}
	if c := got["notes/added.md"]; c.BaseContent != nil {
		t.Fatalf("new file must have no base: %+v", c)
	}
	if c := got["auto/MEMORY.md"]; c.BaseContent == nil || *c.BaseContent != "accepted native" {
		t.Fatalf("native base = %+v", c)
	}
}

func TestMemoryWritebackDisabledOrUnmanaged(t *testing.T) {
	if wb := newMemoryWriteback(Task{Agent: &AgentData{}}, "claude", t.TempDir(), true, slog.Default()); wb != nil {
		t.Fatal("no policy must disable write-back")
	}
	off := Task{Agent: &AgentData{MemoryWriteback: &AgentMemoryWritebackData{Mode: "off"}}}
	if wb := newMemoryWriteback(off, "claude", t.TempDir(), true, slog.Default()); wb != nil {
		t.Fatal("off must disable write-back")
	}
	review := Task{Agent: &AgentData{MemoryWriteback: &AgentMemoryWritebackData{Mode: "review", CaptureRuntime: true}}}
	if wb := newMemoryWriteback(review, "claude", t.TempDir(), false, slog.Default()); wb == nil || wb.nativeDir != "" {
		t.Fatalf("local-directory tasks must not capture runtime memory: %+v", wb)
	}
	if wb := newMemoryWriteback(review, "codex", t.TempDir(), true, slog.Default()); wb == nil || wb.nativeDir != "" {
		t.Fatalf("non-Claude runtimes have no captured memory dir: %+v", wb)
	}
}

func TestClaudeProjectMemoryDir(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/cfg")
	dir := t.TempDir()
	got := claudeProjectMemoryDir(dir)
	resolved, _ := filepath.EvalSymlinks(dir)
	want := filepath.Join("/cfg", "projects", claudeProjectKeyUnsafe.ReplaceAllString(resolved, "-"), "memory")
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}
