package execenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteAgentMemoryRendersInlineAndOverflow(t *testing.T) {
	big := strings.Repeat("x", agentMemoryInlineBudget)
	ctx := TaskContextForEnv{
		AgentName: "Memo",
		AgentMemory: []MemoryFileContextForEnv{
			{Path: "CLAUDE.md", Content: "# Rules\nUse ```go fences``` and " + runtimeMarkerEnd + "\n"},
			{Path: "notes/big.md", Content: big},
			{Path: "../escape.md", Content: "nope"},
		},
	}
	var b strings.Builder
	writeAgentMemory(&b, ctx)
	out := b.String()

	if !strings.HasPrefix(out, "## Agent Memory\n\n") {
		t.Fatalf("missing heading:\n%s", out)
	}
	if !strings.Contains(out, "### `CLAUDE.md`\n\n````\n# Rules") {
		t.Fatalf("CLAUDE.md not inlined with a 4-backtick fence:\n%s", out)
	}
	if strings.Contains(out, runtimeMarkerEnd) {
		t.Fatal("runtime end marker leaked into the brief")
	}
	if !strings.Contains(out, "- `.multica/memory/notes/big.md`") {
		t.Fatalf("over-budget file should be listed, not inlined:\n%s", out[:400])
	}
	if strings.Contains(out, big) {
		t.Fatal("over-budget file was inlined")
	}
	if strings.Contains(out, "escape.md") {
		t.Fatal("unsafe path rendered")
	}

	var empty strings.Builder
	writeAgentMemory(&empty, TaskContextForEnv{})
	if empty.Len() != 0 {
		t.Fatalf("empty memory must not render a section, got %q", empty.String())
	}
}

func TestBriefIncludesAgentMemoryAfterIdentity(t *testing.T) {
	brief := buildMetaSkillContent("claude", TaskContextForEnv{
		IssueID:           "issue-1",
		AgentID:           "agent-1",
		AgentName:         "Memo",
		AgentInstructions: "Be careful.",
		AgentMemory:       []MemoryFileContextForEnv{{Path: "CLAUDE.md", Content: "remember the build uses pnpm"}},
	})
	identity := strings.Index(brief, "## Agent Identity")
	memory := strings.Index(brief, "## Agent Memory")
	if identity < 0 || memory < 0 || memory < identity {
		t.Fatalf("expected Agent Memory after Agent Identity (identity=%d memory=%d)", identity, memory)
	}
	if !strings.Contains(brief, "remember the build uses pnpm") {
		t.Fatal("memory content missing from brief")
	}
}

func TestWriteAgentMemoryFilesUsesManifest(t *testing.T) {
	workDir := t.TempDir()
	// A user file that already occupies a memory path must survive.
	userFile := filepath.Join(workDir, ".multica", "memory", "keep.md")
	if err := os.MkdirAll(filepath.Dir(userFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userFile, []byte("user"), 0o644); err != nil {
		t.Fatal(err)
	}

	manifest := &sidecarManifest{}
	err := writeAgentMemoryFiles(workDir, []MemoryFileContextForEnv{
		{Path: "CLAUDE.md", Content: "a"},
		{Path: "projects/x/MEMORY.md", Content: "b"},
		{Path: "keep.md", Content: "overwrite?"},
		{Path: "/abs.md", Content: "no"},
		{Path: "../../escape.md", Content: "no"},
	}, manifest)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	for rel, want := range map[string]string{"CLAUDE.md": "a", "projects/x/MEMORY.md": "b", "keep.md": "user"} {
		got, err := os.ReadFile(filepath.Join(workDir, ".multica", "memory", filepath.FromSlash(rel)))
		if err != nil || string(got) != want {
			t.Fatalf("%s = %q, %v; want %q", rel, got, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(workDir, "escape.md")); err == nil {
		t.Fatal("path traversal wrote outside the memory dir")
	}
	if len(manifest.Files) != 2 {
		t.Fatalf("manifest should record only the two files written, got %v", manifest.Files)
	}
}
