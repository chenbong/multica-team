package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func projectDirectoryTask(t *testing.T, path string) Task {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"local_path": path})
	if err != nil {
		t.Fatal(err)
	}
	return Task{ProjectResources: []ProjectResourceData{{ID: "project-dir", ResourceType: "working_directory", ResourceRef: raw}}}
}

func TestProjectDirectoryResolution(t *testing.T) {
	existing := t.TempDir()
	missing := filepath.Join(t.TempDir(), "absent")
	for _, machine := range []string{"machine-a", "machine-b"} {
		t.Run(machine, func(t *testing.T) {
			task := projectDirectoryTask(t, existing)
			raw := string(task.ProjectResources[0].ResourceRef)
			resolved, err := resolveProjectWorkingDirectory(task, machine)
			if err != nil {
				t.Fatal(err)
			}
			assignment, err := localDirectoryAssignmentForTask(resolved, machine)
			if err != nil || assignment == nil || assignment.AbsPath != existing || assignment.UsesWorktree() {
				t.Fatalf("assignment=%+v err=%v", assignment, err)
			}
			if string(task.ProjectResources[0].ResourceRef) != raw || task.ProjectResources[0].ResourceType != "working_directory" {
				t.Fatal("mutated shared task resources")
			}
		})
	}
	t.Run("missing uses native default even with legacy resource", func(t *testing.T) {
		task := projectDirectoryTask(t, missing)
		task.ProjectResources = append(task.ProjectResources, ProjectResourceData{ResourceType: "local_directory", ResourceRef: json.RawMessage(`{"local_path":"/legacy","daemon_id":"machine"}`)}, ProjectResourceData{ResourceType: "github_repo", ResourceRef: json.RawMessage(`{"url":"https://example.test/repo.git"}`)})
		got, err := resolveProjectWorkingDirectory(task, "machine")
		if err != nil {
			t.Fatal(err)
		}
		if len(got.ProjectResources) != 1 || got.ProjectResources[0].ResourceType != "github_repo" {
			t.Fatalf("default context not preserved: %+v", got.ProjectResources)
		}
		assignment, err := localDirectoryAssignmentForTask(got, "machine")
		if err != nil || assignment != nil {
			t.Fatal("missing did not become native default")
		}
		if _, err = os.Stat(missing); !os.IsNotExist(err) {
			t.Fatal("created the configured directory")
		}
		if err = os.Mkdir(missing, 0700); err != nil {
			t.Fatal(err)
		}
		assignment, err = localDirectoryAssignmentForTask(got, "machine")
		if err != nil || assignment != nil {
			t.Fatal("choice changed after resolution")
		}
	})
	t.Run("ordinary and legacy tasks unchanged", func(t *testing.T) {
		for _, task := range []Task{{}, {ProjectResources: []ProjectResourceData{{ResourceType: "local_directory", ResourceRef: json.RawMessage(`{"local_path":"/legacy","daemon_id":"machine"}`)}}}} {
			got, err := resolveProjectWorkingDirectory(task, "machine")
			if err != nil || !reflect.DeepEqual(got, task) {
				t.Fatalf("legacy changed: %v", err)
			}
		}
	})
	t.Run("coordinator exemption unchanged", func(t *testing.T) {
		task := projectDirectoryTask(t, existing)
		task.IsLeaderTask = true
		got, err := resolveProjectWorkingDirectory(task, "machine")
		if err != nil {
			t.Fatal(err)
		}
		a, err := localDirectoryAssignmentForTask(got, "machine")
		if err != nil || a != nil {
			t.Fatal("coordinator entered worker directory")
		}
	})
}

func TestProjectDirectoryRejectsInvalidPaths(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", "relative/path", file, string(filepath.Separator), "/invalid\x00path"} {
		t.Run(path, func(t *testing.T) {
			if _, err := resolveProjectWorkingDirectory(projectDirectoryTask(t, path), "machine"); err == nil {
				t.Fatal("invalid existing or unsafe path accepted")
			}
		})
	}
	task := projectDirectoryTask(t, t.TempDir())
	task.ProjectResources = append(task.ProjectResources, task.ProjectResources[0])
	if _, err := resolveProjectWorkingDirectory(task, "machine"); err == nil {
		t.Fatal("duplicate accepted")
	}
	task = projectDirectoryTask(t, t.TempDir())
	task.ProjectResources[0].ResourceRef = json.RawMessage(`{"local_path":5}`)
	if _, err := resolveProjectWorkingDirectory(task, "machine"); err == nil {
		t.Fatal("malformed ref accepted")
	}
	if _, err := resolveProjectWorkingDirectory(projectDirectoryTask(t, t.TempDir()), ""); err == nil {
		t.Fatal("missing identity accepted")
	}
	if os.Geteuid() != 0 {
		path := t.TempDir()
		if err := os.Chmod(path, 0000); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(path, 0700)
		if _, err := resolveProjectWorkingDirectory(projectDirectoryTask(t, path), "machine"); err == nil {
			t.Fatal("permission denied fell back")
		}
	}
}

func TestProjectDirectoryCapabilityOnBothTransports(t *testing.T) {
	for _, capabilities := range []string{daemonClientCapabilities(), daemonHTTPClientCapabilities()} {
		if !strings.Contains(","+capabilities+",", ","+protocol.DaemonCapabilityProjectWorkingDirectoryV1+",") {
			t.Fatal("missing capability")
		}
	}
}

// Exercise real environment preparation for both runtimes, without launching
// model clients, and compare fallback with the unconfigured native result.
func TestProjectDirectoryExecutionEnvironment(t *testing.T) {
	for _, provider := range []string{"claude", "codex"} {
		t.Run(provider, func(t *testing.T) {
			t.Setenv("CODEX_HOME", t.TempDir())
			root := t.TempDir()
			existing := t.TempDir()
			missing := filepath.Join(t.TempDir(), "missing")
			if err := os.WriteFile(filepath.Join(existing, "AGENTS.md"), []byte("project rules"), 0600); err != nil {
				t.Fatal(err)
			}
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			params := execenv.PrepareParams{WorkspacesRoot: root, WorkspaceID: "ws", TaskID: "11111111-1111-1111-1111-111111111111", AgentName: "Test", Provider: provider}
			for _, path := range []string{existing, missing} {
				resolved, err := resolveProjectWorkingDirectory(projectDirectoryTask(t, path), "machine")
				if err != nil {
					t.Fatal(err)
				}
				a, err := localDirectoryAssignmentForTask(resolved, "machine")
				if err != nil {
					t.Fatal(err)
				}
				params.LocalWorkDir = ""
				if a != nil {
					params.LocalWorkDir = a.AbsPath
				}
				env, err := execenv.Prepare(params, logger)
				if err != nil {
					t.Fatal(err)
				}
				if path == existing && env.WorkDir != existing {
					t.Fatalf("workdir=%q", env.WorkDir)
				}
				fallbackWorkDir := env.WorkDir
				if err = env.Cleanup(true); err != nil {
					t.Fatal(err)
				}
				if path == missing {
					baseline, err := execenv.Prepare(params, logger)
					if err != nil {
						t.Fatal(err)
					}
					if baseline.WorkDir != fallbackWorkDir {
						t.Fatal("fallback changed native default")
					}
					_ = baseline.Cleanup(true)
				}
			}
			data, err := os.ReadFile(filepath.Join(existing, "AGENTS.md"))
			if err != nil || string(data) != "project rules" {
				t.Fatal("user project rules changed")
			}
		})
	}
}

func TestProjectDirectoryHandleTaskResolvesBeforeRunner(t *testing.T) {
	for _, present := range []bool{true, false} {
		t.Run(fmt.Sprint(present), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "project")
			if present {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
			defer srv.Close()
			d := &Daemon{localPathLocks: NewLocalPathLocker(), cfg: Config{DaemonID: "machine"}, client: NewClient(srv.URL), logger: slog.New(slog.NewTextHandler(io.Discard, nil)), workspaces: make(map[string]*workspaceState), runtimeIndex: map[string]Runtime{"runtime": {ID: "runtime", Provider: "claude"}}, activeEnvRoots: make(map[string]int), cancelPollInterval: time.Hour}
			called := false
			d.runner = taskRunnerFunc(func(_ context.Context, task Task, _ string, _ int, _ *slog.Logger) (TaskResult, error) {
				called = true
				a, err := localDirectoryAssignmentForTask(task, "machine")
				if err != nil {
					t.Fatal(err)
				}
				if (a != nil) != present {
					t.Fatalf("runner assignment %+v", a)
				}
				return TaskResult{Status: "completed"}, nil
			})
			task := projectDirectoryTask(t, path)
			task.ID = "task"
			task.RuntimeID = "runtime"
			task.WorkspaceID = "ws"
			task.IssueID = "issue"
			task.Agent = &AgentData{Name: "test"}
			d.handleTask(context.Background(), task, 0)
			if !called {
				t.Fatal("runner was not called")
			}
		})
	}
}
