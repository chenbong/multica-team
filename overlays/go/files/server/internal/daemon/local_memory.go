package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Runtime-local agent memory discovery (multica-team overlay).
//
// The server asks, through the heartbeat, for either the list of well-known
// memory / instruction files on this machine or the contents of some of
// them. Discovery is a fixed allow-list of locations used by the agent CLIs
// Multica drives; a read request only ever resolves keys produced by a fresh
// discovery pass, so the server cannot make the daemon read arbitrary paths.

const (
	maxLocalMemoryFileSize       int64 = 256 << 10 // matches the server's per-file memory limit
	maxLocalMemoryEntries              = 500
	maxLocalMemoryProjects             = 200
	maxLocalMemoryFilesPerDir          = 100
	maxLocalMemoryReadTotalBytes       = 4 << 20
)

type runtimeLocalMemorySummary struct {
	Key           string `json:"key"`
	Tool          string `json:"tool"`
	Scope         string `json:"scope"`
	Label         string `json:"label,omitempty"`
	SourcePath    string `json:"source_path"`
	SuggestedPath string `json:"suggested_path"`
	SizeBytes     int64  `json:"size_bytes"`
	ModifiedAt    string `json:"modified_at,omitempty"`
	TooLarge      bool   `json:"too_large,omitempty"`
}

type runtimeLocalMemoryFile struct {
	Key           string `json:"key"`
	SourcePath    string `json:"source_path"`
	SuggestedPath string `json:"suggested_path"`
	Content       string `json:"content"`
	SizeBytes     int    `json:"size_bytes"`
}

type runtimeLocalMemoryReadError struct {
	Key   string `json:"key"`
	Error string `json:"error"`
}

type localMemoryCandidate struct {
	summary runtimeLocalMemorySummary
	abs     string
}

// localMemoryToolForProvider maps a runtime provider to the memory tool whose
// files should be listed first.
func localMemoryToolForProvider(provider string) string {
	switch provider {
	case "claude", "codebuddy", "codex", "gemini", "qwen", "opencode", "kiro", "hermes":
		return provider
	case "antigravity":
		return "gemini"
	default:
		return ""
	}
}

// memorySegment turns an arbitrary directory name into a path segment the
// server accepts (no leading dot, no Windows-reserved characters).
func memorySegment(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	s := strings.Trim(b.String(), "-.")
	if len(s) > 60 {
		s = strings.Trim(s[len(s)-60:], "-.")
	}
	if s == "" {
		s = "project"
	}
	return s
}

func isMemoryTextFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".mdc", ".markdown", ".txt":
		return !strings.HasPrefix(name, ".")
	default:
		return false
	}
}

type localMemoryCollector struct {
	seen  map[string]bool
	items []localMemoryCandidate
}

func (c *localMemoryCollector) full() bool { return len(c.items) >= maxLocalMemoryEntries }

func (c *localMemoryCollector) addFile(abs, tool, scope, label, suggested string) {
	if c.full() {
		return
	}
	info, err := os.Stat(abs)
	if err != nil || !info.Mode().IsRegular() {
		return
	}
	key := relativizeHomePath(abs)
	if c.seen[key] {
		return
	}
	c.seen[key] = true
	c.items = append(c.items, localMemoryCandidate{
		abs: abs,
		summary: runtimeLocalMemorySummary{
			Key:           key,
			Tool:          tool,
			Scope:         scope,
			Label:         label,
			SourcePath:    key,
			SuggestedPath: suggested,
			SizeBytes:     info.Size(),
			ModifiedAt:    info.ModTime().UTC().Format(time.RFC3339),
			TooLarge:      info.Size() > maxLocalMemoryFileSize,
		},
	})
}

// addDir lists text files directly inside dir and, when depth > 0, inside
// its subdirectories.
func (c *localMemoryCollector) addDir(dir, tool, scope, label, suggestedPrefix string, depth int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	count := 0
	for _, e := range entries {
		if c.full() || count >= maxLocalMemoryFilesPerDir {
			return
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		abs := filepath.Join(dir, name)
		if e.IsDir() {
			if depth > 0 {
				c.addDir(abs, tool, scope, label, suggestedPrefix+"/"+memorySegment(name), depth-1)
			}
			continue
		}
		if !isMemoryTextFile(name) {
			continue
		}
		count++
		c.addFile(abs, tool, scope, label, suggestedPrefix+"/"+name)
	}
}

// discoverLocalMemory returns every memory candidate on this machine, the
// runtime's own tool first.
func discoverLocalMemory(provider string) ([]localMemoryCandidate, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve user home: %w", err)
	}
	c := &localMemoryCollector{seen: map[string]bool{}}

	// Claude Code (and Claude-family builds such as ducc): global CLAUDE.md
	// plus the per-project auto-memory directories.
	claudeHome := envOrDefault("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	c.addFile(filepath.Join(claudeHome, "CLAUDE.md"), "claude", "global", "", "claude/CLAUDE.md")
	if projects, err := os.ReadDir(filepath.Join(claudeHome, "projects")); err == nil {
		n := 0
		for _, p := range projects {
			if !p.IsDir() || n >= maxLocalMemoryProjects {
				continue
			}
			memDir := filepath.Join(claudeHome, "projects", p.Name(), "memory")
			if info, err := os.Stat(memDir); err != nil || !info.IsDir() {
				continue
			}
			n++
			c.addDir(memDir, "claude", "project", p.Name(), "claude/projects/"+memorySegment(p.Name()), 1)
		}
	}

	c.addFile(filepath.Join(home, ".codebuddy", "CODEBUDDY.md"), "codebuddy", "global", "", "codebuddy/CODEBUDDY.md")

	codexHome := envOrDefault("CODEX_HOME", filepath.Join(home, ".codex"))
	for _, name := range []string{"AGENTS.md", "AGENTS.override.md", "instructions.md"} {
		c.addFile(filepath.Join(codexHome, name), "codex", "global", "", "codex/"+name)
	}
	c.addDir(filepath.Join(codexHome, "memories"), "codex", "memory", "", "codex/memories", 0)

	c.addFile(filepath.Join(home, ".gemini", "GEMINI.md"), "gemini", "global", "", "gemini/GEMINI.md")
	c.addFile(filepath.Join(envOrDefault("QWEN_HOME", filepath.Join(home, ".qwen")), "QWEN.md"), "qwen", "global", "", "qwen/QWEN.md")
	c.addFile(filepath.Join(home, ".config", "opencode", "AGENTS.md"), "opencode", "global", "", "opencode/AGENTS.md")
	c.addDir(filepath.Join(home, ".kiro", "steering"), "kiro", "global", "", "kiro/steering", 0)
	if res := execenv.ResolveHermesProfile("", "", false, false); res.Err == nil && res.SourceHome != "" {
		c.addDir(filepath.Join(res.SourceHome, "memories"), "hermes", "memory", "", "hermes/memories", 0)
	}

	preferred := localMemoryToolForProvider(provider)
	sort.SliceStable(c.items, func(i, j int) bool {
		pi, pj := c.items[i].summary.Tool == preferred, c.items[j].summary.Tool == preferred
		return pi && !pj
	})
	return c.items, nil
}

func listRuntimeLocalMemory(provider string) ([]runtimeLocalMemorySummary, error) {
	items, err := discoverLocalMemory(provider)
	if err != nil {
		return nil, err
	}
	out := make([]runtimeLocalMemorySummary, 0, len(items))
	for _, it := range items {
		out = append(out, it.summary)
	}
	return out, nil
}

func readRuntimeLocalMemory(provider string, keys []string) ([]runtimeLocalMemoryFile, []runtimeLocalMemoryReadError, error) {
	items, err := discoverLocalMemory(provider)
	if err != nil {
		return nil, nil, err
	}
	byKey := make(map[string]localMemoryCandidate, len(items))
	for _, it := range items {
		byKey[it.summary.Key] = it
	}
	var files []runtimeLocalMemoryFile
	var errs []runtimeLocalMemoryReadError
	total := 0
	for _, key := range keys {
		it, ok := byKey[key]
		if !ok {
			errs = append(errs, runtimeLocalMemoryReadError{Key: key, Error: "file was not found on the runtime machine"})
			continue
		}
		info, err := os.Stat(it.abs)
		if err != nil || !info.Mode().IsRegular() {
			errs = append(errs, runtimeLocalMemoryReadError{Key: key, Error: "file is no longer readable"})
			continue
		}
		if info.Size() > maxLocalMemoryFileSize {
			errs = append(errs, runtimeLocalMemoryReadError{Key: key, Error: fmt.Sprintf("file is larger than %d KiB", maxLocalMemoryFileSize>>10)})
			continue
		}
		if total+int(info.Size()) > maxLocalMemoryReadTotalBytes {
			errs = append(errs, runtimeLocalMemoryReadError{Key: key, Error: "selection is too large to import at once"})
			continue
		}
		data, err := os.ReadFile(it.abs)
		if err != nil {
			errs = append(errs, runtimeLocalMemoryReadError{Key: key, Error: "file could not be read"})
			continue
		}
		if int64(len(data)) > maxLocalMemoryFileSize || !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
			errs = append(errs, runtimeLocalMemoryReadError{Key: key, Error: "file is not UTF-8 text or is too large"})
			continue
		}
		total += len(data)
		files = append(files, runtimeLocalMemoryFile{
			Key:           key,
			SourcePath:    it.summary.SourcePath,
			SuggestedPath: it.summary.SuggestedPath,
			Content:       string(data),
			SizeBytes:     len(data),
		})
	}
	return files, errs, nil
}

// ReportLocalMemoryResult sends a memory list/read result back to the server.
func (c *Client) ReportLocalMemoryResult(ctx context.Context, runtimeID, requestID string, result map[string]any) error {
	return c.postJSON(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/local-memory/%s/result", runtimeID, requestID), result, nil)
}

func (d *Daemon) handleLocalMemory(ctx context.Context, rt Runtime, pending PendingLocalMemory) {
	d.logger.Info("runtime local memory requested", "runtime_id", rt.ID, "request_id", pending.ID, "kind", pending.Kind, "keys", len(pending.Keys))
	var payload map[string]any
	switch pending.Kind {
	case "list":
		entries, err := listRuntimeLocalMemory(rt.Provider)
		if err != nil {
			payload = map[string]any{"status": "failed", "error": err.Error()}
		} else {
			payload = map[string]any{"status": "completed", "entries": entries}
		}
	case "read":
		files, errs, err := readRuntimeLocalMemory(rt.Provider, pending.Keys)
		if err != nil {
			payload = map[string]any{"status": "failed", "error": err.Error()}
		} else {
			payload = map[string]any{"status": "completed", "files": files, "errors": errs}
		}
	default:
		payload = map[string]any{"status": "failed", "error": fmt.Sprintf("unsupported memory request kind %q", pending.Kind)}
	}
	d.reportRuntimeResultWithRetry(ctx, "local_memory", rt.ID, pending.ID, func(ctx context.Context) error {
		return d.client.ReportLocalMemoryResult(ctx, rt.ID, pending.ID, payload)
	})
}

// PendingLocalMemory aliases the wire type like the other heartbeat actions.
type PendingLocalMemory = protocol.DaemonHeartbeatPendingLocalMemory

func convertAgentMemoryForEnv(files []AgentMemoryFileData) []execenv.MemoryFileContextForEnv {
	if len(files) == 0 {
		return nil
	}
	out := make([]execenv.MemoryFileContextForEnv, 0, len(files))
	for _, f := range files {
		out = append(out, execenv.MemoryFileContextForEnv{Path: f.Path, Content: f.Content})
	}
	return out
}
