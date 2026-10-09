package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const readyDucxDoctor = `{"overallStatus":"warning","checks":{"auth.credentials":{"status":"ok"},"network.provider_reachability":{"status":"warning"}}}`

func writeDucxFixture(t *testing.T, home, name, content string) {
	t.Helper()
	dir := filepath.Join(home, ".baidu-cx")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func ducXFixtureOps(t *testing.T, home string) (ducxSetupOps, *[][]string, *int) {
	t.Helper()
	runs := [][]string{}
	installs := 0
	ops := ducxSetupOps{
		find: func(h string) string { return installedBaiduExecutable(h, "ducx") },
		install: func(_ context.Context, h string, _ func(string)) error {
			installs++
			p := filepath.Join(h, ".baidu-cx/baidu-cx/bin/ducx")
			if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
				return err
			}
			return os.WriteFile(p, []byte("fixture only; never executed"), 0700)
		},
		run: func(_ context.Context, executable, username string, args []string) ([]byte, error) {
			if username != "alice" || executable != installedBaiduExecutable(home, "ducx") {
				t.Fatal("wrong identity or executable")
			}
			runs = append(runs, append([]string(nil), args...))
			if reflect.DeepEqual(args, []string{"config", "model", "gpt-6-sol"}) {
				writeDucxFixture(t, home, "user.json", `{"model":"gpt-6-sol"}`)
				return nil, nil
			}
			if !reflect.DeepEqual(args, []string{"doctor", "--json"}) {
				t.Fatal("unexpected command")
			}
			return []byte("wrapper startup notice\n" + readyDucxDoctor + "\n"), nil
		},
	}
	return ops, &runs, &installs
}

func TestDucxSetupInstallsDefaultsAndReusesSharedCredential(t *testing.T) {
	home := t.TempDir()
	if err := writeDuccCredential(home, "alice", []byte("fixture-shared-login")); err != nil {
		t.Fatal(err)
	}
	ops, runs, installs := ducXFixtureOps(t, home)
	var messages []string
	if err := prepareDucxRuntime(context.Background(), home, "alice", func(s string) { messages = append(messages, s) }, ops); err != nil {
		t.Fatal(err)
	}
	if *installs != 1 || len(*runs) != 2 {
		t.Fatalf("wrong preparation counts: installs=%d runs=%d", *installs, len(*runs))
	}
	if model, err := configuredDucxModel(home); err != nil || model != "gpt-6-sol" {
		t.Fatalf("default not configured: %v", err)
	}
	if err := prepareDucxRuntime(context.Background(), home, "alice", nil, ops); err != nil {
		t.Fatal(err)
	}
	if *installs != 1 || len(*runs) != 3 {
		t.Fatal("repeat setup installed or changed model again")
	}
	data, err := readDuccCredential(home, "alice")
	if err != nil || string(data) != "fixture-shared-login" {
		t.Fatal("shared credential changed")
	}
	if strings.Contains(strings.Join(messages, "\n"), "fixture-shared-login") {
		t.Fatal("credential leaked to progress")
	}
}

func TestDucxSetupPreservesExistingModels(t *testing.T) {
	for _, tc := range []struct{ name, file, content, model string }{
		{"wrapper", "user.json", `{"model":"existing-custom-model","other":true}`, "existing-custom-model"},
		{"native", "config.toml", "model = \"existing-native-model\"\n", "existing-native-model"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if err := writeDuccCredential(home, "alice", []byte("fixture-login")); err != nil {
				t.Fatal(err)
			}
			writeDucxFixture(t, home, tc.file, tc.content)
			ops, runs, _ := ducXFixtureOps(t, home)
			if err := prepareDucxRuntime(context.Background(), home, "alice", nil, ops); err != nil {
				t.Fatal(err)
			}
			if len(*runs) != 1 || (*runs)[0][0] != "doctor" {
				t.Fatal("existing model overwritten")
			}
			data, _ := os.ReadFile(filepath.Join(home, ".baidu-cx", tc.file))
			if string(data) != tc.content {
				t.Fatal("existing configuration changed")
			}
		})
	}
}

func TestDucxSetupRequiresOwnSharedCredential(t *testing.T) {
	home := t.TempDir()
	if err := writeDuccCredential(home, "bob", []byte("other-account-fixture")); err != nil {
		t.Fatal(err)
	}
	ops, runs, _ := ducXFixtureOps(t, home)
	err := prepareDucxRuntime(context.Background(), home, "alice", nil, ops)
	if err == nil || err.Error() != "ducx_shared_credential_unavailable" || len(*runs) != 0 {
		t.Fatal("another account credential accepted")
	}
	if err := prepareDucxRuntime(context.Background(), home, "../bob", nil, ops); err == nil {
		t.Fatal("unsafe identity accepted")
	}
}

func TestDucxModelConfigFailsClosed(t *testing.T) {
	for _, tc := range []struct{ file, content string }{
		{"user.json", "private-malformed-config"},
		{"user.json", `{"model":123}`},
		{"config.toml", "model = [invalid"},
	} {
		t.Run(tc.file+tc.content, func(t *testing.T) {
			home := t.TempDir()
			writeDucxFixture(t, home, tc.file, tc.content)
			if _, err := configuredDucxModel(home); err == nil || strings.Contains(err.Error(), tc.content) {
				t.Fatal("bad config accepted or leaked")
			}
		})
	}
	home := t.TempDir()
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(home, ".baidu-cx")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := configuredDucxModel(home); err == nil {
		t.Fatal("symlink config directory accepted")
	}
}

func TestDucxDoctorAuthContract(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		ready     bool
	}{
		{"ready with CDN warning", readyDucxDoctor, true},
		{"startup noise", "notice {not json}\n" + readyDucxDoctor + "\ntrailer", true},
		{"missing auth", `{"overallStatus":"ok","checks":{}}`, false},
		{"auth failed", `{"checks":{"auth.credentials":{"status":"error"}}}`, false},
		{"unsupported", "Usage: codex", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if ducxProviderAuthReady([]byte(tc.raw)) != tc.ready {
				t.Fatal("incorrect auth verdict")
			}
		})
	}
}

func TestDucxSetupFailuresDoNotReportSuccess(t *testing.T) {
	for _, name := range []string{"installer", "missing executable", "model command", "model not applied", "doctor command", "doctor auth"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			if err := writeDuccCredential(home, "alice", []byte("fixture-login")); err != nil {
				t.Fatal(err)
			}
			ops, _, _ := ducXFixtureOps(t, home)
			original := ops.run
			switch name {
			case "installer":
				ops.install = func(context.Context, string, func(string)) error { return errors.New("private-output") }
			case "missing executable":
				ops.install = func(context.Context, string, func(string)) error { return nil }
			default:
				ops.run = func(ctx context.Context, executable, username string, args []string) ([]byte, error) {
					if args[0] == "config" && name == "model command" {
						return nil, errors.New("private-output")
					}
					if args[0] == "config" && name == "model not applied" {
						return nil, nil
					}
					if args[0] == "doctor" && name == "doctor command" {
						return nil, errors.New("private-output")
					}
					if args[0] == "doctor" && name == "doctor auth" {
						return []byte(`{"checks":{}}`), nil
					}
					return original(ctx, executable, username, args)
				}
			}
			var messages []string
			err := prepareDucxRuntime(context.Background(), home, "alice", func(s string) { messages = append(messages, s) }, ops)
			if err == nil || strings.Contains(err.Error(), "private-output") || strings.Contains(strings.Join(messages, ""), "检查通过") {
				t.Fatal("failure hidden or leaked")
			}
		})
	}
}

func TestDucxSetupLockRecovery(t *testing.T) {
	home := t.TempDir()
	if err := writeDuccCredential(home, "alice", []byte("fixture-login")); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(home, ".multica-ducx-setup-lock")
	if err := os.Mkdir(lock, 0700); err != nil {
		t.Fatal(err)
	}
	ops, _, _ := ducXFixtureOps(t, home)
	if err := prepareDucxRuntime(context.Background(), home, "alice", nil, ops); err == nil {
		t.Fatal("concurrent setup allowed")
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	if err := prepareDucxRuntime(context.Background(), home, "alice", nil, ops); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatal("setup lock leaked")
	}
}

func TestDucxOutputIsBounded(t *testing.T) {
	var out ducxBoundedOutput
	data := make([]byte, ducxOutputLimit+10)
	if n, err := out.Write(data); n != len(data) || err != nil || !out.truncated || out.Len() != ducxOutputLimit {
		t.Fatal("output limit failed")
	}
}
