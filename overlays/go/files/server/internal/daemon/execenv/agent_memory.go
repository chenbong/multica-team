package execenv

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// Agent memory (multica-team overlay).
//
// The server sends an agent's memory files with every claimed task. They are
// materialized under {workDir}/.multica/memory/ (through the sidecar manifest,
// so local_directory teardown and Reuse roll them back like every other
// Multica-owned file) and rendered into the runtime brief's
// "## Agent Memory" section so every provider — CLAUDE.md for Claude-family
// runtimes, AGENTS.md for Codex and most others — starts the run with them.
// Small sets are inlined whole; once the inline budget is spent the remaining
// files are only listed and the agent reads them from disk when relevant.

// MemoryFileContextForEnv is one memory file to deliver into the task env.
type MemoryFileContextForEnv struct {
	Path    string
	Content string
}

// AgentMemoryRelDir is the workdir-relative directory memory files land in.
const AgentMemoryRelDir = ".multica/memory"

// agentMemoryInlineBudget bounds how much memory text is inlined into the
// brief. It is a whole-file budget: a file is either inlined completely or
// only listed, never truncated mid-way.
const agentMemoryInlineBudget = 48 << 10

// cleanAgentMemoryPath re-validates a server-provided memory path before it
// touches the filesystem. The server already normalizes paths; this keeps the
// daemon safe against an older or misbehaving server.
func cleanAgentMemoryPath(raw string) (string, bool) {
	p := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\x00:") {
		return "", false
	}
	cleaned := path.Clean(p)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	for _, seg := range strings.Split(cleaned, "/") {
		if seg == "" || seg == "." || seg == ".." || strings.HasPrefix(seg, ".") {
			return "", false
		}
	}
	return cleaned, true
}

// writeAgentMemoryFiles writes each memory file under .multica/memory/.
// A path that already exists (user content in a local_directory workdir) is
// left untouched; the brief still carries the inlined copy.
func writeAgentMemoryFiles(workDir string, files []MemoryFileContextForEnv, manifest *sidecarManifest) error {
	if len(files) == 0 {
		return nil
	}
	root := filepath.Join(workDir, filepath.FromSlash(AgentMemoryRelDir))
	for _, f := range files {
		rel, ok := cleanAgentMemoryPath(f.Path)
		if !ok {
			continue
		}
		target := filepath.Join(root, filepath.FromSlash(rel))
		if within, err := filepath.Rel(root, target); err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
			continue
		}
		if err := recordMkdirAll(filepath.Dir(target), 0o755, manifest); err != nil {
			return fmt.Errorf("create memory dir for %s: %w", rel, err)
		}
		if err := recordWriteFile(target, []byte(f.Content), 0o600, manifest); err != nil {
			if errors.Is(err, errPathPreExists) {
				continue
			}
			return fmt.Errorf("write memory file %s: %w", rel, err)
		}
	}
	return nil
}

// neutralizeRuntimeMarkers keeps memory text from closing or reopening the
// managed brief block it is rendered into.
func neutralizeRuntimeMarkers(s string) string {
	s = strings.ReplaceAll(s, runtimeMarkerBegin, "<!-- (memory) BEGIN MULTICA-RUNTIME -->")
	return strings.ReplaceAll(s, runtimeMarkerEnd, "<!-- (memory) END MULTICA-RUNTIME -->")
}

// memoryFence returns a backtick fence longer than any backtick run inside
// content, so the fenced block can never be terminated early.
func memoryFence(content string) string {
	longest, run := 0, 0
	for _, r := range content {
		if r == '`' {
			run++
			if run > longest {
				longest = run
			}
			continue
		}
		run = 0
	}
	if longest < 3 {
		return "```"
	}
	return strings.Repeat("`", longest+1)
}

// writeAgentMemory emits the "## Agent Memory" brief section.
func writeAgentMemory(b *strings.Builder, ctx TaskContextForEnv) {
	type entry struct {
		path    string
		content string
	}
	var entries []entry
	for _, f := range ctx.AgentMemory {
		if p, ok := cleanAgentMemoryPath(f.Path); ok {
			entries = append(entries, entry{path: p, content: neutralizeRuntimeMarkers(f.Content)})
		}
	}
	if len(entries) == 0 {
		return
	}

	b.WriteString("## Agent Memory\n\n")
	b.WriteString("Your owner configured the memory below for you: durable notes, preferences and context carried across runs. Use it as background knowledge; the current task's instructions win when they conflict. ")
	fmt.Fprintf(b, "A copy of every file is in `%s/` in your working directory. Edits to those copies are not saved — when something should be remembered for future runs, say so in your final reply so the owner can update the agent's Memory tab.\n\n", AgentMemoryRelDir)

	used := 0
	var listed []entry
	for _, e := range entries {
		if used+len(e.content) > agentMemoryInlineBudget {
			listed = append(listed, e)
			continue
		}
		used += len(e.content)
		fence := memoryFence(e.content)
		fmt.Fprintf(b, "### `%s`\n\n%s\n%s", e.path, fence, e.content)
		if !strings.HasSuffix(e.content, "\n") {
			b.WriteString("\n")
		}
		b.WriteString(fence)
		b.WriteString("\n\n")
	}
	if len(listed) > 0 {
		b.WriteString("Not inlined (read when relevant):\n\n")
		for _, e := range listed {
			fmt.Fprintf(b, "- `%s/%s` (%d bytes)\n", AgentMemoryRelDir, e.path, len(e.content))
		}
		b.WriteString("\n")
	}
}
