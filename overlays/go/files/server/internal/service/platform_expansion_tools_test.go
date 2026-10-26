package service

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlatformExpansionToolUsesRunTokenAndCleansSecretFile(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python3 is required to exercise the delivered tool")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer mat_fixture" || r.Header.Get("X-Workspace-ID") != "workspace-fixture" {
			t.Error("wrong credential scope")
			w.WriteHeader(403)
			return
		}
		if r.URL.Path != "/api/agent/personal-secrets/fixture-id" {
			t.Error("wrong secret path")
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"value":"fixture-private-key"}`))
	}))
	defer server.Close()
	root := t.TempDir()
	script := filepath.Join(root, "tools.py")
	if err = os.WriteFile(script, []byte(platformExpansionTools), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(python, script, "secrets", "with-file", "--id", "fixture-id", "--", python, "-c", `import os,pathlib,sys; p=pathlib.Path(sys.argv[1]); assert p.read_text()=="fixture-private-key"; assert p.stat().st_mode & 0o777 == 0o600; print(p)`, "{secret_file}")
	cmd.Env = append(os.Environ(), "MULTICA_TOKEN=mat_fixture", "MULTICA_SERVER_URL="+server.URL, "MULTICA_WORKSPACE_ID=workspace-fixture", "TMPDIR="+root)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("delivered file tool failed: %s", output)
	}
	path := strings.TrimSpace(string(output))
	if !strings.HasPrefix(path, root+string(os.PathSeparator)) {
		t.Fatal("file outside temporary directory")
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("secret file was retained")
	}
}

func TestPlatformExpansionToolRejectsPersonalCredentials(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python3 unavailable")
	}
	root := t.TempDir()
	script := filepath.Join(root, "tools.py")
	if err = os.WriteFile(script, []byte(platformExpansionTools), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(python, script, "secrets", "list")
	cmd.Env = append(os.Environ(), "MULTICA_TOKEN=mul_do_not_use", "MULTICA_SERVER_URL=http://127.0.0.1:1")
	output, err := cmd.CombinedOutput()
	if err == nil || strings.Contains(string(output), "mul_do_not_use") {
		t.Fatal("personal token fallback or leak")
	}
}
