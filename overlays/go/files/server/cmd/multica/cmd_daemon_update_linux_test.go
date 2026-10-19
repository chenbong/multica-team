//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/daemon"
)

// Only a test binary can enter this fixture probe; production has the real
// hidden command. No installed agent CLI or production account is accessed.
func init() {
	if len(os.Args) == 3 && os.Args[1] == "daemon" && os.Args[2] == "update-capabilities" {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"protocol": 1, "version": "0.6.1-overlay.2", "os": runtime.GOOS, "arch": runtime.GOARCH})
		os.Exit(0)
	}
}

func TestDaemonUpdateChildFixture(t *testing.T) {
	if os.Getenv("TEAM_MESH_UPDATE_TEST_CHILD") != "1" {
		return
	}
	wasHandoff := os.Getenv(cli.DaemonUpdateHandoffEnv) != ""
	mode := os.Getenv("TEAM_MESH_UPDATE_TEST_MODE")
	if wasHandoff && mode == "exit" {
		os.Exit(7)
	}
	if wasHandoff && mode == "timeout" {
		time.Sleep(10 * time.Second)
		os.Exit(8)
	}
	lease, probation, err := cli.OpenDaemonUpdateLease()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	version := "0.6.1-overlay.1"
	if wasHandoff {
		version = "0.6.1-overlay.2"
	}
	listener, err := net.Listen("tcp", "127.0.0.1:"+os.Getenv("TEAM_MESH_UPDATE_TEST_PORT"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	health := daemon.HealthResponse{Status: "running", PID: os.Getpid(), CLIVersion: version, Profile: "update-fixture"}
	if wasHandoff && mode == "wrong-identity" {
		health.PID++
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(health) })}
	go server.Serve(listener)
	defer server.Close()
	marker := os.Getenv("TEAM_MESH_UPDATE_TEST_MARKER")
	if wasHandoff {
		if mode == "crash-after-ready" {
			go func() { time.Sleep(100 * time.Millisecond); os.Exit(9) }()
		}
		if mode == "commit-pipe-fails" {
			_ = json.NewEncoder(probation.Ready).Encode(map[string]any{"protocol": 1, "version": version, "pid": os.Getpid()})
			_ = probation.Ready.Close()
			_ = probation.Control.Close()
		} else {
			if err := probation.Confirm(context.Background(), version); err != nil {
				t.Fatal(err)
			}
			if err := lease.Shared(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(marker, []byte("new-claims"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	} else {
		if err := os.WriteFile(marker, []byte("old-restarted"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	defer signal.Stop(signals)
	<-signals
}

func TestDaemonUpdateGuardedStartup(t *testing.T) {
	// Child installation fixtures inherit a standalone environment even when
	// the host test runner itself is launched by systemd or a supervisor.
	for _, key := range []string{"INVOCATION_ID", "JOURNAL_STREAM", "SUPERVISOR_ENABLED", "MULTICA_LAUNCHED_BY"} {
		t.Setenv(key, "")
	}
	oldTimeout, oldWindow, oldInterval := updateStartupTimeout, updateStabilityWindow, updateHealthInterval
	updateStartupTimeout, updateStabilityWindow, updateHealthInterval = 3*time.Second, 200*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() {
		updateStartupTimeout, updateStabilityWindow, updateHealthInterval = oldTimeout, oldWindow, oldInterval
	})
	// These are installation fixtures, not daemon binaries installed on the host.
	testExecutable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(testExecutable)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"healthy", "exit", "timeout", "wrong-identity", "crash-after-ready", "log-open-fails", "spawn-fails", "commit-pipe-fails"} {
		t.Run(mode, func(t *testing.T) {
			directory := t.TempDir()
			executable := filepath.Join(directory, "multica")
			old := append(bytes.Clone(binary), []byte("\nTEAMMESH-PREVIOUS-EXECUTABLE\n")...)
			if err := os.WriteFile(executable, old, 0o750); err != nil {
				t.Fatal(err)
			}
			// Use an explicit temporary install, never the running test binary.
			lease, err := cli.OpenDaemonInstallationLease(executable)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Close()
			if err := lease.Begin("0.6.1-overlay.2"); err != nil {
				t.Fatal(err)
			}
			if err := lease.Install("0.6.1-overlay.2", binary); err != nil {
				t.Fatal(err)
			}
			if mode == "spawn-fails" {
				if err := os.Chmod(executable, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := listener.Addr().(*net.TCPAddr).Port
			listener.Close()
			marker := filepath.Join(directory, "task-claim-marker")
			t.Setenv("TEAM_MESH_UPDATE_TEST_CHILD", "1")
			t.Setenv("TEAM_MESH_UPDATE_TEST_MODE", mode)
			t.Setenv("TEAM_MESH_UPDATE_TEST_PORT", strconv.Itoa(port))
			t.Setenv("TEAM_MESH_UPDATE_TEST_MARKER", marker)
			pidPath := filepath.Join(directory, "daemon.pid")
			configPath := filepath.Join(directory, "config.json")
			taskPath := filepath.Join(directory, "task.txt")
			for _, path := range []string{configPath, taskPath} {
				if err := os.WriteFile(path, []byte("unchanged-user-content"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			logPath := filepath.Join(directory, "daemon.err.log")
			if mode == "log-open-fails" {
				logPath = filepath.Join(directory, "missing", "daemon.err.log")
				_ = os.WriteFile(filepath.Join(directory, "missing"), []byte("not a directory"), 0o600)
			}
			cfg := daemon.Config{UpdateLease: lease, HealthPort: port, Profile: "update-fixture"}
			handled, err := guardedDaemonHandoff(executable, []string{"-test.run=^TestDaemonUpdateChildFixture$"}, cfg, pidPath, logPath, slog.Default())
			if !handled {
				t.Fatal("transaction handoff not selected")
			}
			data, readErr := os.ReadFile(pidPath)
			if readErr != nil {
				t.Fatalf("no successor/rollback PID: %v / %v", err, readErr)
			}
			pid, _ := strconv.Atoi(string(data))
			if pid <= 1 {
				t.Fatal("invalid owned fixture PID")
			}
			t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGTERM) })
			want := "old-restarted"
			if mode == "healthy" {
				if err != nil {
					t.Fatal(err)
				}
				want = "new-claims"
			} else if err == nil {
				t.Fatal("broken startup reported success")
			}
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				data, _ = os.ReadFile(marker)
				if string(data) == want {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if string(data) != want {
				t.Fatalf("claim/rollback marker=%q, want %q", data, want)
			}
			removeOwnedDaemonPID(pidPath, os.Getpid())
			if _, err := os.Stat(pidPath); err != nil {
				t.Fatal("old guardian removed successor PID")
			}
			for _, path := range []string{configPath, taskPath} {
				data, _ = os.ReadFile(path)
				if string(data) != "unchanged-user-content" {
					t.Fatal("user files changed")
				}
			}
			actual, _ := os.ReadFile(executable)
			wantBinary := old
			if mode == "healthy" {
				wantBinary = binary
			}
			if !bytes.Equal(actual, wantBinary) {
				t.Fatal("wrong executable after guarded handoff")
			}
		})
	}
}

func TestDaemonUpdateHandoffEnvironment(t *testing.T) {
	env := cleanUpdateHandoffEnv([]string{"CONFIG=preserve", cli.DaemonUpdateHandoffEnv + "=1", "TOKEN=do-not-change"})
	if fmt.Sprint(env) != "[CONFIG=preserve TOKEN=do-not-change]" {
		t.Fatal("unrelated environment changed")
	}
}

func TestDaemonUpdateAPIOutageClassification(t *testing.T) {
	for _, status := range []int{200, 503} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/readyz" {
				t.Error("wrong readiness path")
			}
			w.WriteHeader(status)
		}))
		if got := updateAPIUnavailable(server.URL); got != (status == 503) {
			t.Fatalf("API %d unavailable=%t", status, got)
		}
		server.Close()
		if !updateAPIUnavailable(server.URL) {
			t.Fatal("unreachable API classified as a broken release")
		}
	}
}
