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
	// The copies Prepare wrote.
	mem := filepath.Join(workDir, ".multica", "memory")
	for _, f := range task.Agent.Memory {
		writeTestFile(t, filepath.Join(mem, filepath.FromSlash(f.Path)), f.Content)
	}
	// The runtime's memory dir still holds a previous run's content.
	nativeDir := claudeProjectMemoryDir(workDir)
	writeTestFile(t, filepath.Join(nativeDir, "MEMORY.md"), "stale from the last run")
	writeTestFile(t, filepath.Join(nativeDir, "rejected.md"), "rejected earlier")

	wb := newMemoryWriteback(task, "claude", workDir, true, slog.Default())
	if wb == nil || wb.nativeDir != nativeDir {
		t.Fatalf("write-back not prepared: %+v", wb)
	}
	// The runtime dir was reset to exactly the accepted auto/ files.
	if seeded, err := os.ReadFile(filepath.Join(nativeDir, "MEMORY.md")); err != nil || string(seeded) != "accepted native" {
		t.Fatalf("seeded native memory = %q, %v", seeded, err)
	}
	if _, err := os.Stat(filepath.Join(nativeDir, "rejected.md")); !os.IsNotExist(err) {
		t.Fatalf("stale runtime memory survived the reset: %v", err)
	}
	if got := wb.collect(); len(got) != 0 {
		t.Fatalf("nothing changed yet, got %+v", got)
	}

	// The run: edits, adds, deletes; the runtime extends its memory.
	writeTestFile(t, filepath.Join(mem, "edit.md"), "new")
	writeTestFile(t, filepath.Join(mem, "notes", "added.md"), "added")
	writeTestFile(t, filepath.Join(mem, "bin.md"), "\x00binary")
	if err := os.Remove(filepath.Join(mem, "drop.md")); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(nativeDir, "MEMORY.md"), "accepted native\n- learned more")
	writeTestFile(t, filepath.Join(nativeDir, "build.md"), "use pnpm")

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

// Files the run did not touch are never reported, whatever they hold: a
// user's own file in a local directory, or a copy Prepare could not refresh.
func TestMemoryWritebackIgnoresUntouchedFiles(t *testing.T) {
	workDir := t.TempDir()
	mem := filepath.Join(workDir, ".multica", "memory")
	writeTestFile(t, filepath.Join(mem, "a.md"), "user's own bytes")
	writeTestFile(t, filepath.Join(mem, "mine.md"), "not delivered")
	task := Task{Agent: &AgentData{
		Memory:          []AgentMemoryFileData{{Path: "a.md", Content: "platform"}},
		MemoryWriteback: &AgentMemoryWritebackData{Mode: "auto"},
	}}
	wb := newMemoryWriteback(task, "codex", workDir, false, slog.Default())
	if got := wb.collect(); len(got) != 0 {
		t.Fatalf("untouched files reported: %+v", got)
	}
	if err := os.Remove(filepath.Join(mem, "mine.md")); err != nil {
		t.Fatal(err)
	}
	if got := wb.collect(); len(got) != 0 {
		t.Fatalf("removing an undelivered file is not a memory deletion: %+v", got)
	}
}

func TestRemoveAgentCreatedMemory(t *testing.T) {
	workDir := t.TempDir()
	mem := filepath.Join(workDir, ".multica", "memory")
	writeTestFile(t, filepath.Join(mem, "delivered.md"), "copy")
	writeTestFile(t, filepath.Join(mem, "user", "own.md"), "user")
	atLaunch := memoryCopiesAtLaunch(workDir)
	writeTestFile(t, filepath.Join(mem, "learned.md"), "agent")
	writeTestFile(t, filepath.Join(mem, "notes", "deep.md"), "agent")
	writeTestFile(t, filepath.Join(mem, "user", "added.md"), "agent")
	if err := removeAgentCreatedMemory(workDir, atLaunch); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"delivered.md", "user/own.md"} {
		if _, err := os.Stat(filepath.Join(mem, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("%s must survive: %v", rel, err)
		}
	}
	for _, rel := range []string{"learned.md", "notes", "user/added.md"} {
		if _, err := os.Stat(filepath.Join(mem, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("%s must be removed: %v", rel, err)
		}
	}
	if err := removeAgentCreatedMemory(t.TempDir(), map[string]bool{}); err != nil {
		t.Fatalf("missing memory dir: %v", err)
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
