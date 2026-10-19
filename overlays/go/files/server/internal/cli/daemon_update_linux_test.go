//go:build linux

package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func updateTestLease(t *testing.T) (*DaemonUpdateLease, []byte) {
	t.Helper()
	executable := filepath.Join(t.TempDir(), "multica")
	old := []byte("#!/bin/sh\nexit 0\n")
	if err := os.WriteFile(executable, old, 0o750); err != nil {
		t.Fatal(err)
	}
	lease, err := openDaemonLease(executable, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lease.Close() })
	return lease, old
}

func updateTestCandidate(version string) []byte {
	return []byte(fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' '{\"protocol\":1,\"version\":\"%s\",\"os\":\"linux\",\"arch\":\"%s\"}'\n", version, runtime.GOARCH))
}

func mustReadUpdate(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestDaemonUpdateTransactionRollbackPreservesUserFiles(t *testing.T) {
	lease, old := updateTestLease(t)
	config := filepath.Join(filepath.Dir(lease.Executable), "config.json")
	task := filepath.Join(filepath.Dir(lease.Executable), "task-result.txt")
	for _, path := range []string{config, task} {
		if err := os.WriteFile(path, []byte("user-owned content"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const target = "0.6.1-overlay.5"
	if err := lease.Begin(target); err != nil {
		t.Fatal(err)
	}
	candidate := updateTestCandidate(target)
	if err := lease.Install(target, candidate); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(mustReadUpdate(t, lease.Executable), candidate) {
		t.Fatal("candidate not installed")
	}
	if record, err := lease.Pending(); err != nil || record == nil || record.Target != target {
		t.Fatalf("pending record: %v %v", record, err)
	}
	if err := lease.Rollback(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(mustReadUpdate(t, lease.Executable), old) {
		t.Fatal("previous executable not restored")
	}
	for _, path := range []string{config, task} {
		if string(mustReadUpdate(t, path)) != "user-owned content" {
			t.Fatal("user data changed")
		}
	}
	info, _ := os.Stat(lease.Executable)
	if info.Mode().Perm() != 0o750 {
		t.Fatal("executable permissions changed")
	}
	_ = lease.Shared()
	if err := lease.Begin(target); err == nil || !strings.Contains(err.Error(), "previously failed") {
		t.Fatalf("bad release was not quarantined: %v", err)
	}
	if err := lease.Begin("0.6.1-overlay.6"); err != nil {
		t.Fatal("later release blocked:", err)
	}
}

func TestDaemonUpdateCandidateProtocolAndBackupIntegrity(t *testing.T) {
	for _, mode := range []string{"unsupported", "wrong-version", "backup-corrupt", "outside-replacement", "confirmed-but-not-released"} {
		t.Run(mode, func(t *testing.T) {
			lease, old := updateTestLease(t)
			const target = "0.6.1-overlay.5"
			if err := lease.Begin(target); err != nil {
				t.Fatal(err)
			}
			candidate := updateTestCandidate(target)
			if mode == "unsupported" {
				candidate = []byte("#!/bin/sh\nexit 2\n")
			}
			if mode == "wrong-version" {
				candidate = updateTestCandidate("0.6.1-overlay.4")
			}
			err := lease.Install(target, candidate)
			if mode == "unsupported" || mode == "wrong-version" {
				if err == nil || !bytes.Equal(mustReadUpdate(t, lease.Executable), old) {
					t.Fatal("unsupported candidate replaced the running binary")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "backup-corrupt" {
				_ = os.WriteFile(filepath.Join(lease.stateDir(), "previous"), []byte("corrupted"), 0o700)
				if lease.Rollback() == nil {
					t.Fatal("corrupted backup accepted")
				}
			} else if mode == "outside-replacement" {
				_ = os.WriteFile(lease.Executable, []byte("external install"), 0o700)
				if lease.Rollback() == nil || string(mustReadUpdate(t, lease.Executable)) != "external install" {
					t.Fatal("unrelated replacement overwritten")
				}
			} else {
				if err := lease.Confirm(); err != nil {
					t.Fatal(err)
				}
				// A failed guardian commit pipe must still be recoverable before
				// the successor is allowed to claim any tasks.
				if err := lease.Rollback(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestDaemonUpdateSharedInstallationAndLaunchModes(t *testing.T) {
	lease, _ := updateTestLease(t)
	other, err := openDaemonLease(lease.Executable, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Begin("0.6.1-overlay.2"); err == nil {
		t.Fatal("shared installation upgraded")
	}
	other.Close()
	if err := lease.Begin("0.6.1-overlay.2"); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		pid int
		env map[string]string
	}{
		{1, nil}, {42, map[string]string{"INVOCATION_ID": "service"}},
		{42, map[string]string{"SUPERVISOR_ENABLED": "1"}}, {42, map[string]string{"MULTICA_LAUNCHED_BY": "desktop"}},
	} {
		if daemonUpdateLaunchModeError(test.pid, func(k string) string { return test.env[k] }) == nil {
			t.Fatal("unsupported launcher accepted")
		}
	}
	if err := daemonUpdateLaunchModeError(42, func(string) string { return "" }); err != nil {
		t.Fatal("ordinary daemon rejected")
	}
}

func TestDaemonUpdateDownloadForkOnlyAndChecksum(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprint(corrupt), func(t *testing.T) {
			lease, old := updateTestLease(t)
			const target = "v0.6.1-overlay.5"
			asset := releaseAssetCandidates(target, runtime.GOOS, runtime.GOARCH)[0]
			var archive bytes.Buffer
			zip := gzip.NewWriter(&archive)
			tarball := tar.NewWriter(zip)
			candidate := updateTestCandidate(target)
			_ = tarball.WriteHeader(&tar.Header{Name: "multica", Mode: 0o755, Size: int64(len(candidate))})
			_, _ = tarball.Write(candidate)
			_ = tarball.Close()
			_ = zip.Close()
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/repos/chenbong/multica-team/releases/tags/" + target:
					_ = json.NewEncoder(w).Encode(GitHubRelease{TagName: target, Assets: []GitHubReleaseAsset{{Name: asset, BrowserDownloadURL: server.URL + "/archive"}, {Name: "checksums.txt", BrowserDownloadURL: server.URL + "/checksums"}}})
				case "/archive":
					_, _ = w.Write(archive.Bytes())
				case "/checksums":
					sum := digest(archive.Bytes())
					if corrupt {
						sum = strings.Repeat("0", 64)
					}
					_, _ = fmt.Fprintf(w, "%s  %s\n", sum, asset)
				default:
					t.Errorf("unexpected repository/path: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			t.Setenv(releaseAPIBaseURLEnv, server.URL)
			t.Setenv(releaseDownloadBaseURLEnv, "")
			_, err := UpdateDaemonViaDownload(target, lease)
			if corrupt {
				if err == nil || !bytes.Equal(mustReadUpdate(t, lease.Executable), old) {
					t.Fatal("bad checksum replaced executable")
				}
			} else if err != nil {
				t.Fatal(err)
			} else if lease.Rollback() != nil {
				t.Fatal("downloaded candidate did not roll back")
			}
		})
	}
}

func TestDaemonUpdateProbationRejectsMissingGuardian(t *testing.T) {
	control, writer, _ := os.Pipe()
	reader, ready, _ := os.Pipe()
	defer control.Close()
	defer reader.Close()
	_ = writer.Close()
	probation := &UpdateProbation{Control: control, Ready: ready, Done: make(chan struct{})}
	if err := probation.Confirm(context.Background(), "0.6.1-overlay.5"); err == nil {
		t.Fatal("missing guardian accepted")
	}
	select {
	case <-probation.Done:
		t.Fatal("unconfirmed startup released the task barrier")
	default:
	}
}

func TestDaemonUpdateActualPID1(t *testing.T) {
	if os.Getpid() != 1 {
		t.Skip("run separately as the isolated container entrypoint")
	}
	lease, old := updateTestLease(t)
	if err := lease.Begin("0.6.1-overlay.5"); err == nil || !strings.Contains(err.Error(), "PID 1") {
		t.Fatalf("PID 1 update was not rejected: %v", err)
	}
	if !bytes.Equal(mustReadUpdate(t, lease.Executable), old) {
		t.Fatal("PID 1 executable changed")
	}
	if _, err := os.Stat(lease.stateDir()); !os.IsNotExist(err) {
		t.Fatal("unsupported launcher created update state")
	}
}

func TestDaemonUpdateOfflineStartupCanRetry(t *testing.T) {
	lease, old := updateTestLease(t)
	const target = "0.6.1-overlay.5"
	if err := lease.Begin(target); err != nil {
		t.Fatal(err)
	}
	if err := lease.Install(target, updateTestCandidate(target)); err != nil {
		t.Fatal(err)
	}
	if err := lease.RollbackStartup(false); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(mustReadUpdate(t, lease.Executable), old) {
		t.Fatal("offline rollback failed")
	}
	_ = lease.Shared()
	if err := lease.Begin(target); err != nil {
		t.Fatal("network failure permanently blocked the release:", err)
	}
}
