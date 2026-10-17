package daemon

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

// Agent memory write-back (multica-team overlay).
//
// When the claim carries a write-back policy, the daemon reports after a
// completed run how the agent changed its memory:
//
//   - edits, new files and deletions under .multica/memory/ (origin "agent"),
//     compared with the set delivered at claim time;
//   - for Claude-family runtimes in a daemon-managed workdir, the runtime's
//     own auto-memory for this task (~/.claude/projects/<workdir>/memory/),
//     mapped to "auto/<file>" (origin "runtime"). The directory is seeded
//     with the previously accepted auto/ files before launch, so the runtime
//     keeps extending one memory instead of starting from scratch per issue.
//
// The server turns each change into a proposal; nothing here decides
// whether it is applied.

const (
	memoryWritebackAutoPrefix = "auto/"
	memoryWritebackMaxFiles   = 64
	memoryWritebackMaxBytes   = 256 << 10
	memoryWritebackTimeout    = 20 * time.Second
)

type agentMemoryChange struct {
	Path        string  `json:"path"`
	Action      string  `json:"action"`
	Content     string  `json:"content"`
	BaseContent *string `json:"base_content"`
	Origin      string  `json:"origin"`
}

type memoryWriteback struct {
	workDir   string
	delivered map[string]string
	nativeDir string // "" when runtime memory is not captured
	// What was on disk when the agent launched. Changes are diffed against
	// this, not against the delivered set, so a file the run did not touch
	// is never reported — whatever stale or user-owned bytes it holds.
	launchCopies map[string]string
	launchNative map[string]string
	logger       *slog.Logger
}

var claudeProjectKeyUnsafe = regexp.MustCompile(`[^a-zA-Z0-9]`)

// claudeProjectMemoryDir is where Claude Code keeps auto-memory for a
// working directory: <config dir>/projects/<path with every non-alphanumeric
// character replaced by '-'>/memory.
func claudeProjectMemoryDir(workDir string) string {
	base := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".claude")
	}
	abs, err := filepath.Abs(workDir)
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return filepath.Join(base, "projects", claudeProjectKeyUnsafe.ReplaceAllString(abs, "-"), "memory")
}

// newMemoryWriteback prepares write-back for a task, or returns nil when
// the claim did not ask for it. managedWorkDir is false for local-directory
// and worktree tasks, whose runtime memory belongs to the user's own project.
func newMemoryWriteback(task Task, provider, workDir string, managedWorkDir bool, logger *slog.Logger) *memoryWriteback {
	if task.Agent == nil || task.Agent.MemoryWriteback == nil || task.Agent.MemoryWriteback.Mode == "" || task.Agent.MemoryWriteback.Mode == "off" {
		return nil
	}
	wb := &memoryWriteback{workDir: workDir, delivered: map[string]string{}, logger: logger}
	for _, f := range task.Agent.Memory {
		wb.delivered[f.Path] = f.Content
	}
	if task.Agent.MemoryWriteback.CaptureRuntime && managedWorkDir && provider == "claude" {
		wb.nativeDir = claudeProjectMemoryDir(workDir)
		wb.seedNative()
	}
	wb.launchCopies = readMemoryTree(wb.copiesDir(), 7)
	if wb.nativeDir != "" {
		wb.launchNative = readMemoryTree(wb.nativeDir, 2)
	}
	return wb
}

func (wb *memoryWriteback) copiesDir() string {
	return filepath.Join(wb.workDir, filepath.FromSlash(execenv.AgentMemoryRelDir))
}

// seedNative makes the runtime's memory dir hold exactly the accepted auto/
// files. The dir is keyed by the task workdir, which Multica owns and reuses
// for every run of the same issue, so it is reset first: left alone, the
// previous run's (possibly rejected or since-edited) memory would outrank
// what the owner accepted.
func (wb *memoryWriteback) seedNative() {
	if wb.nativeDir == "" {
		return
	}
	if err := os.RemoveAll(wb.nativeDir); err != nil {
		wb.logger.Warn("memory write-back: reset runtime memory dir failed; not capturing runtime memory", "error", err)
		wb.nativeDir = ""
		return
	}
	for p, content := range wb.delivered {
		rel, ok := strings.CutPrefix(p, memoryWritebackAutoPrefix)
		if !ok || rel == "" {
			continue
		}
		target := filepath.Join(wb.nativeDir, filepath.FromSlash(rel))
		if _, err := os.Lstat(target); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			wb.logger.Warn("memory write-back: seed runtime memory dir failed", "error", err)
			return
		}
		if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
			wb.logger.Warn("memory write-back: seed runtime memory file failed", "path", rel, "error", err)
		}
	}
}

// agentMemoryWritebackMode is the brief's view of the write-back policy.
func agentMemoryWritebackMode(agent *AgentData) string {
	if agent == nil || agent.MemoryWriteback == nil {
		return ""
	}
	switch agent.MemoryWriteback.Mode {
	case "review", "auto":
		return agent.MemoryWriteback.Mode
	}
	return ""
}

// readMemoryTree returns the text files under root (slash-separated relative
// paths), skipping symlinks, dot-named entries, binaries and oversized files.
func readMemoryTree(root string, maxDepth int) map[string]string {
	out := map[string]string{}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || len(out) >= memoryWritebackMaxFiles {
			if d != nil && d.IsDir() && p == root {
				return fs.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil || rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if strings.Count(rel, "/") >= maxDepth {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil || info.Size() > memoryWritebackMaxBytes {
			return nil
		}
		data, rerr2 := os.ReadFile(p)
		if rerr2 != nil || !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
			return nil
		}
		out[rel] = string(data)
		return nil
	})
	return out
}

func (wb *memoryWriteback) base(p string) *string {
	if c, ok := wb.delivered[p]; ok {
		return &c
	}
	return nil
}

// collect diffs the run's memory against what was on disk at launch. The
// base sent with each change is the delivered (platform) content, which the
// server compares with the stored file to detect concurrent edits.
func (wb *memoryWriteback) collect() []agentMemoryChange {
	byPath := map[string]agentMemoryChange{}
	copies := readMemoryTree(wb.copiesDir(), 7)
	for p, content := range copies {
		if prev, ok := wb.launchCopies[p]; ok && prev == content {
			continue
		}
		byPath[p] = agentMemoryChange{Path: p, Action: "upsert", Content: content, BaseContent: wb.base(p), Origin: "agent"}
	}
	for p := range wb.launchCopies {
		if _, ok := copies[p]; !ok {
			if _, delivered := wb.delivered[p]; !delivered {
				continue // a file Multica did not deliver is not memory to delete
			}
			byPath[p] = agentMemoryChange{Path: p, Action: "delete", BaseContent: wb.base(p), Origin: "agent"}
		}
	}
	if wb.nativeDir != "" {
		for rel, content := range readMemoryTree(wb.nativeDir, 2) {
			p := memoryWritebackAutoPrefix + rel
			if prev, ok := wb.launchNative[rel]; ok && prev == content {
				continue
			}
			if strings.TrimSpace(content) == "" {
				continue
			}
			byPath[p] = agentMemoryChange{Path: p, Action: "upsert", Content: content, BaseContent: wb.base(p), Origin: "runtime"}
		}
	}
	out := make([]agentMemoryChange, 0, len(byPath))
	for _, c := range byPath {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	if len(out) > 2*memoryWritebackMaxFiles {
		out = out[:2*memoryWritebackMaxFiles]
	}
	return out
}

// report sends the changes of a completed run. Best effort: a failure is
// logged and never affects the task result.
func (wb *memoryWriteback) report(ctx context.Context, client *Client, taskID string) {
	if wb == nil {
		return
	}
	changes := wb.collect()
	if len(changes) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, memoryWritebackTimeout)
	defer cancel()
	var resp struct {
		Mode     string `json:"mode"`
		Proposed int    `json:"proposed"`
		Applied  int    `json:"applied"`
		Skipped  int    `json:"skipped"`
	}
	if err := client.ReportAgentMemoryChanges(ctx, taskID, changes, &resp); err != nil {
		wb.logger.Warn("memory write-back: report failed", "changes", len(changes), "error", err)
		return
	}
	wb.logger.Info("memory write-back reported", "changes", len(changes), "mode", resp.Mode,
		"proposed", resp.Proposed, "applied", resp.Applied, "skipped", resp.Skipped)
}

// ReportAgentMemoryChanges posts a run's memory changes for review.
func (c *Client) ReportAgentMemoryChanges(ctx context.Context, taskID string, changes []agentMemoryChange, resp any) error {
	return c.postJSON(ctx, fmt.Sprintf("/api/daemon/tasks/%s/memory-changes", taskID), map[string]any{"changes": changes}, resp)
}

// memoryCopiesAtLaunch records which entries under .multica/memory/ existed
// when the agent launched (Multica's copies plus anything the user had).
func memoryCopiesAtLaunch(workDir string) map[string]bool {
	root := filepath.Join(workDir, filepath.FromSlash(execenv.AgentMemoryRelDir))
	out := map[string]bool{}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if rel, rerr := filepath.Rel(root, p); rerr == nil && rel != "." {
			out[filepath.ToSlash(rel)] = true
		}
		return nil
	})
	return out
}

// removeAgentCreatedMemory deletes the files and folders the run created
// under .multica/memory/ in a user's own directory (local_directory or
// worktree). Multica's own copies are removed by the sidecar cleanup; these
// are the agent's additions, which would otherwise stay in the user's tree
// and, in worktree mode, be committed into the delivered branch. Entries that
// existed at launch are left alone.
func removeAgentCreatedMemory(workDir string, atLaunch map[string]bool) error {
	if atLaunch == nil {
		return nil
	}
	root := filepath.Join(workDir, filepath.FromSlash(execenv.AgentMemoryRelDir))
	var created []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil || rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if atLaunch[rel] {
			return nil
		}
		created = append(created, p)
		if d.IsDir() {
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		return err
	}
	var firstErr error
	for _, p := range created {
		if err := os.RemoveAll(p); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
