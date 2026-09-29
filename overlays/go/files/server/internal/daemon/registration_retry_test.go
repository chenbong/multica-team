package daemon

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRegistrationRetryBackoff(t *testing.T) {
	d := &Daemon{}
	now := time.Unix(1000, 0)
	for _, delay := range []time.Duration{15, 30, 60, 120, 120} {
		if !d.registrationRetryAllowed("ws", "same", now) {
			t.Fatal("due retry denied")
		}
		d.recordRegistrationResult("ws", "same", false, now)
		if d.registrationRetryAllowed("ws", "same", now.Add(delay*time.Second-time.Nanosecond)) {
			t.Fatal("failed registration can flood server")
		}
		if !d.registrationRetryAllowed("ws", "changed", now) {
			t.Fatal("changed executable set must retry immediately")
		}
		now = now.Add(delay * time.Second)
	}
	d.recordRegistrationResult("ws", "same", true, now)
	if !d.registrationRetryAllowed("ws", "same", now) {
		t.Fatal("success did not clear backoff")
	}
}

func TestRegistrationFindsInstalledDuccWithoutParentPATH(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MULTICA_DAEMON_RUNTIME_SHIMS", "")
	stubLookPath(t, map[string]string{})
	t.Cleanup(stubAgentVersion(t))
	profiles := []RuntimeProfile{{ID: "ducc-profile", WorkspaceID: "ws-1", DisplayName: "ducc", ProtocolFamily: "claude", CommandName: "ducc", Enabled: true}}
	fx := newProfileRegisterFixture(t, profiles, http.StatusOK)
	fx.daemon.cfg.Agents = map[string]AgentEntry{}
	first, _, err := fx.daemon.registerRuntimesForWorkspaceBatchLocked(context.Background(), "ws-1", nil)
	if err != nil || len(first.Runtimes) != 0 {
		t.Fatalf("expected failed profile placeholder: %v", err)
	}
	if _, _, err = fx.daemon.registerRuntimesForWorkspaceBatchLocked(context.Background(), "ws-1", nil); !errors.Is(err, errRegistrationCoolingDown) {
		t.Fatalf("expected cooldown, got %v", err)
	}
	path := filepath.Join(home, ".baidu-cc/baidu-cc/bin/ducc")
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("#!/bin/sh\necho 2.1.258.6\n"), 0700); err != nil {
		t.Fatal(err)
	}
	next, _, err := fx.daemon.registerRuntimesForWorkspaceBatchLocked(context.Background(), "ws-1", nil)
	if err != nil || len(next.Runtimes) != 1 {
		t.Fatalf("new ducc not detected without PATH: %v", err)
	}
	if len(fx.sentFailures) != 0 || len(fx.sentRuntimes) != 1 {
		t.Fatal("ducc still registered as failed")
	}
}
