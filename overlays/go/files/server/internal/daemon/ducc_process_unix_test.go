//go:build !windows

package daemon

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDuccTimeoutKillsInstallerChildren(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "child-survived")
	ready := filepath.Join(dir, "ready")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", `(sleep 1; touch "$1") & touch "$2"; wait`, "fixture", marker, ready)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	configureDuccProcess(cmd)
	var messages []string
	go func() {
		for i := 0; i < 100; i++ {
			if _, e := os.Stat(ready); e == nil {
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()
	start := time.Now()
	if err := runDuccWithProgress(ctx, cmd, func(s string) { messages = append(messages, s) }); err == nil {
		t.Fatal("expected cancellation")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("installer timeout did not return promptly")
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("installer child survived cancellation")
	}
	if len(messages) == 0 || !strings.Contains(messages[0], "正在安装") {
		t.Fatal("initial installation progress missing")
	}
}

func TestDuccInstallerEmitsPeriodicProgress(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", "sleep 11")
	configureDuccProcess(cmd)
	var messages []string
	if err := runDuccWithProgress(ctx, cmd, func(s string) { messages = append(messages, s) }); err != nil {
		t.Fatal(err)
	}
	if len(messages) < 2 || !strings.Contains(messages[1], "已等待") {
		t.Fatal("periodic progress missing")
	}
}
