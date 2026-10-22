package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func workspaceFixture(t *testing.T) remoteWorkspaceRoot {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{"main.go": "package main\nfunc main() {}\n", ".env": "SECRET=hidden", "binary.dat": "\x00\xff", "huge.txt": strings.Repeat("x", workspaceFileLimit+1)} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	return remoteWorkspaceRoot{Path: dir, WorkspaceID: "ws", RuntimeID: "rt", Binding: "binding"}
}
func TestRemoteWorkspaceReadAndContext(t *testing.T) {
	root := workspaceFixture(t)
	result := readWorkspaceRoot(root, protocol.WorkspaceRequest{Operation: "read", Binding: root.Binding, Path: "main.go"})
	if result.Error != "" || result.Revision == "" || !strings.Contains(result.Content, "package main") {
		t.Fatalf("%+v", result)
	}
	context := readWorkspaceRoot(root, protocol.WorkspaceRequest{Operation: "context", Binding: root.Binding, References: []protocol.WorkspaceReference{{Path: "main.go", Kind: "file", Revision: result.Revision, StartLine: 2, EndLine: 2}, {Path: "src", Kind: "directory"}}})
	if context.Error != "" || !strings.Contains(context.Context, "func main() {}") || strings.Contains(context.Context, "package main") {
		t.Fatalf("%+v", context)
	}
	os.WriteFile(filepath.Join(root.Path, "main.go"), []byte("changed"), 0600)
	changed := readWorkspaceRoot(root, protocol.WorkspaceRequest{Operation: "context", Binding: root.Binding, References: []protocol.WorkspaceReference{{Path: "main.go", Kind: "file", Revision: result.Revision}}})
	if changed.Error != "file_changed" {
		t.Fatalf("%+v", changed)
	}
}
func TestRemoteWorkspaceRejectsUnsafePaths(t *testing.T) {
	root := workspaceFixture(t)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("do not disclose"), 0600)
	if err := os.Symlink(outside, filepath.Join(root.Path, "escape")); err != nil {
		t.Fatal(err)
	}
	os.Symlink(filepath.Join(root.Path, "main.go"), filepath.Join(root.Path, "inside-link"))
	for _, name := range []string{"../secret", "/etc/passwd", "src/../../secret", "src/../main.go", "C:\\secret", "file:stream", ".env", ".env.local", ".git/config", "escape/secret", "inside-link", "src\nmain.go"} {
		t.Run(name, func(t *testing.T) {
			r := readWorkspaceRoot(root, protocol.WorkspaceRequest{Operation: "read", Binding: root.Binding, Path: name})
			if r.Error == "" || r.Content != "" {
				t.Fatalf("unsafe read: %+v", r)
			}
		})
	}
	for name, want := range map[string]string{"binary.dat": "not_text_file", "huge.txt": "file_too_large", "missing": "file_unavailable", "src": "not_text_file"} {
		r := readWorkspaceRoot(root, protocol.WorkspaceRequest{Operation: "read", Binding: root.Binding, Path: name})
		if r.Error != want {
			t.Errorf("%s: %s", name, r.Error)
		}
	}
}
func TestRemoteWorkspacePaginationAndFiltering(t *testing.T) {
	root := workspaceFixture(t)
	for i := 0; i < 210; i++ {
		os.WriteFile(filepath.Join(root.Path, fmt.Sprintf("file-%03d", i)), nil, 0600)
	}
	req := protocol.WorkspaceRequest{Operation: "list", Path: "."}
	first := readWorkspaceRoot(root, req)
	if len(first.Entries) != 200 || first.NextOffset == nil || first.Entries[0].Kind != "directory" {
		t.Fatalf("%+v", first)
	}
	req.Offset = *first.NextOffset
	second := readWorkspaceRoot(root, req)
	seen := map[string]bool{}
	for _, entry := range append(first.Entries, second.Entries...) {
		if seen[entry.Path] || entry.Path == ".env" {
			t.Fatal(entry)
		}
		seen[entry.Path] = true
	}
	if len(seen) != 214 || second.NextOffset != nil {
		t.Fatalf("entries %d", len(seen))
	}
}
func TestRemoteWorkspaceBindingAndIsolation(t *testing.T) {
	root := workspaceFixture(t)
	d := &Daemon{}
	task := Task{ChatSessionID: "session", WorkspaceID: "ws", RuntimeID: "rt"}
	d.registerRemoteWorkspace(task, root.Path)
	req := protocol.WorkspaceRequest{SessionID: "session", WorkspaceID: "ws", RuntimeID: "rt", Operation: "list"}
	first := d.readRemoteWorkspace(req)
	if first.Error != "" {
		t.Fatal(first)
	}
	req.WorkspaceID = "other"
	if d.readRemoteWorkspace(req).Error != "workspace_not_ready" {
		t.Fatal("cross workspace access")
	}
	req.WorkspaceID = "ws"
	req.RuntimeID = "other"
	if d.readRemoteWorkspace(req).Error != "workspace_not_ready" {
		t.Fatal("cross runtime access")
	}
	req.RuntimeID = "rt"
	req.Binding = first.Binding
	d.registerRemoteWorkspace(task, t.TempDir())
	if d.readRemoteWorkspace(req).Error != "workspace_changed" {
		t.Fatal("stale binding accepted")
	}
}
func TestRemoteWorkspaceContextLimits(t *testing.T) {
	root := workspaceFixture(t)
	tests := []struct {
		refs []protocol.WorkspaceReference
		want string
	}{
		{nil, "too_many_references"},
		{make([]protocol.WorkspaceReference, 13), "too_many_references"},
		{[]protocol.WorkspaceReference{{Path: "main.go", Kind: "file", StartLine: 0, EndLine: 1}}, "invalid_line_range"},
		{[]protocol.WorkspaceReference{{Path: "main.go", Kind: "file", StartLine: 1, EndLine: 999}}, "invalid_line_range"},
		{[]protocol.WorkspaceReference{{Path: "main.go", Kind: "directory"}}, "not_directory"},
		{[]protocol.WorkspaceReference{{Path: "main.go", Kind: "other"}}, "invalid_reference"},
	}
	for _, test := range tests {
		r := readWorkspaceRoot(root, protocol.WorkspaceRequest{Operation: "context", Binding: root.Binding, References: test.refs})
		if r.Error != test.want {
			t.Errorf("%+v want %s", r, test.want)
		}
	}
	os.WriteFile(filepath.Join(root.Path, "large.txt"), []byte(strings.Repeat("a", workspaceFileLimit)), 0600)
	ref := protocol.WorkspaceReference{Path: "large.txt", Kind: "file"}
	r := readWorkspaceRoot(root, protocol.WorkspaceRequest{Operation: "context", Binding: root.Binding, References: []protocol.WorkspaceReference{ref, ref, ref}})
	if r.Error != "context_too_large" {
		t.Fatal(r.Error)
	}
}

func TestRemoteWorkspaceRootReplacement(t *testing.T) {
	root := workspaceFixture(t)
	d := &Daemon{}
	task := Task{ChatSessionID: "s", WorkspaceID: "ws", RuntimeID: "rt"}
	d.registerRemoteWorkspace(task, root.Path)
	if err := os.Rename(root.Path, root.Path+"-old"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root.Path + "-old") })
	os.Mkdir(root.Path, 0700)
	os.WriteFile(filepath.Join(root.Path, "secret"), []byte("new root"), 0600)
	result := d.readRemoteWorkspace(protocol.WorkspaceRequest{SessionID: "s", WorkspaceID: "ws", RuntimeID: "rt", Operation: "list"})
	if result.Error != "workspace_changed" {
		t.Fatal("replacement root was trusted", result)
	}
}
