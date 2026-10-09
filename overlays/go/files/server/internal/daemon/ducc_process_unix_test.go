//go:build !windows

package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

func TestDucxSetupCommandBindsIdentityAndHidesFailureOutput(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fixture-ducx")
	script := `#!/bin/sh
test "$1" = --username && test "$2" = alice && test "$3" = doctor && test "$4" = --json || exit 8
printf '%s' '{"checks":{"auth.credentials":{"status":"ok"}}}'
`
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	out, err := runDucxSetupCommand(context.Background(), bin, "alice", []string{"doctor", "--json"})
	if err != nil || !ducxProviderAuthReady(out) {
		t.Fatalf("fixture diagnostic failed: %v", err)
	}
	if _, err := runDucxSetupCommand(context.Background(), bin, "../alice", nil); err == nil {
		t.Fatal("unsafe identity accepted")
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho fixture-private-output\necho fixture-private-output >&2\nexit 9\n"), 0700); err != nil {
		t.Fatal(err)
	}
	out, err = runDucxSetupCommand(context.Background(), bin, "alice", []string{"doctor", "--json"})
	if err == nil || out != nil || strings.Contains(err.Error(), "fixture-private-output") {
		t.Fatal("subprocess failure leaked output")
	}
}

func TestDucxSetupCommandTimeoutKillsChildren(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fixture-ducx")
	marker := filepath.Join(dir, "survived")
	// The marker path is fixture-owned, not a real CLI credential or configuration.
	script := "#!/bin/sh\n(sleep 1; touch '" + marker + "') &\nwait\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := runDucxSetupCommand(ctx, bin, "alice", []string{"doctor", "--json"}); err == nil || err.Error() != "ducx_command_timed_out" {
		t.Fatalf("expected bounded timeout: %v", err)
	}
	if time.Since(started) > 3*time.Second {
		t.Fatal("timeout returned too slowly")
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("diagnostic child survived cancellation")
	}
}

func TestDucxSetupRespectsServerPolicyAndBackgroundSync(t *testing.T) {
	for _, tc := range []struct {
		name                                         string
		enabled, includeDucx, allowInstall, wantDucx bool
	}{
		{"foreground", true, true, true, true},
		{"disabled", false, true, true, false},
		{"background", true, false, true, false},
		{"no installation authority", true, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			bin := filepath.Join(home, "bin")
			if err := os.Mkdir(bin, 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", home)
			t.Setenv("PATH", bin)
			if err := writeDuccCredential(home, "alice", []byte("fixture-own-credential")); err != nil {
				t.Fatal(err)
			}
			writeDucxFixture(t, home, "user.json", `{"model":"keep-existing"}`)
			if err := os.WriteFile(filepath.Join(bin, "ducc"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(home, "ducx-ran")
			cli := "#!/bin/sh\ntest \"$1\" = --username && test \"$2\" = alice && test \"$3\" = doctor || exit 9\nprintf called > '" + marker + "'\nprintf '%s' '" + readyDucxDoctor + "'\n"
			if err := os.WriteFile(filepath.Join(bin, "ducx"), []byte(cli), 0700); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer mul_fixture" {
					t.Error("wrong API identity")
				}
				var request duccSync
				if json.NewDecoder(r.Body).Decode(&request) != nil {
					t.Error("invalid request")
				}
				if request.Credential != "" {
					t.Error("credential included in metadata poll")
				}
				_ = json.NewEncoder(w).Encode(duccReply{Username: "alice", Enabled: tc.enabled, Action: "none"})
			}))
			defer server.Close()
			client := NewClient(server.URL)
			client.SetToken("mul_fixture")
			if err := prepareDuccOnce(context.Background(), client, "not-the-username", "fixture", tc.allowInstall, tc.includeDucx, nil); err != nil {
				t.Fatal(err)
			}
			_, err := os.Stat(marker)
			if (err == nil) != tc.wantDucx {
				t.Fatalf("DUCX preparation policy mismatch: %v", err)
			}
		})
	}
}
