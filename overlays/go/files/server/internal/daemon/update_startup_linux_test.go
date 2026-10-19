//go:build linux

package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
)

func TestUpdateStartupCommitReleasesTaskBarrier(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "multica")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	lease, err := cli.OpenDaemonInstallationLease(executable)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	const target = "0.6.1-overlay.2"
	if err := lease.Begin(target); err != nil {
		t.Fatal(err)
	}
	candidate := []byte(fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' '{\"protocol\":1,\"version\":\"%s\",\"os\":\"linux\",\"arch\":\"%s\"}'\n", target, runtime.GOARCH))
	if err := lease.Install(target, candidate); err != nil {
		t.Fatal(err)
	}
	control, writer, _ := os.Pipe()
	reader, ready, _ := os.Pipe()
	defer control.Close()
	defer writer.Close()
	defer reader.Close()
	probation := &cli.UpdateProbation{Control: control, Ready: ready, Done: make(chan struct{})}
	d := New(Config{WorkspacesRoot: t.TempDir(), CLIVersion: target, UpdateLease: lease, UpdateProbation: probation}, slog.Default())
	finished := make(chan error, 1)
	go func() { finished <- d.confirmUpdateStartup(context.Background()) }()
	var identity map[string]any
	if err := json.NewDecoder(reader).Decode(&identity); err != nil {
		t.Fatal(err)
	}
	if d.tryEnterClaim() {
		t.Fatal("startup readiness released tasks before guardian commit")
	}
	if err := lease.Confirm(); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteString("commit\n"); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if !d.tryEnterClaim() {
		t.Fatal("committed successor cannot claim tasks")
	}
	d.exitClaim()
}

func TestTeamMeshPeriodicPollingWaitsForIdle(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "multica")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	lease, err := cli.OpenDaemonInstallationLease(executable)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	oldDelay := autoUpdateInitialDelay
	autoUpdateInitialDelay = 5 * time.Millisecond
	t.Cleanup(func() { autoUpdateInitialDelay = oldDelay })
	var checks atomic.Int32
	oldFetch := fetchLatestRelease
	fetchLatestRelease = func() (*cli.GitHubRelease, error) { checks.Add(1); return nil, nil }
	t.Cleanup(func() { fetchLatestRelease = oldFetch })
	d := &Daemon{cfg: Config{CLIVersion: "0.6.1-overlay.1", AutoUpdateEnabled: true, AutoUpdateCheckInterval: 10 * time.Millisecond, UpdateLease: lease}, logger: slog.Default()}
	d.activeTasks.Store(1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.autoUpdateLoop(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	time.Sleep(30 * time.Millisecond)
	if checks.Load() != 0 {
		t.Fatal("busy periodic updater queried a release")
	}
	d.activeTasks.Store(0)
	deadline := time.Now().Add(time.Second)
	for checks.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if checks.Load() < 2 {
		t.Fatal("idle daemon did not poll periodically")
	}
}
