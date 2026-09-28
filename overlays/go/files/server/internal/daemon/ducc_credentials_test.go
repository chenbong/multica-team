package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDuccCredentialFileSafety(t *testing.T) {
	home := t.TempDir()
	data := []byte("test-credential-only")
	if err := writeDuccCredential(home, "alice", data); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(home, ".comate/login-user/alice")
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("credential must be private")
	}
	if err = writeDuccCredential(home, "alice", []byte("replacement")); err == nil {
		t.Fatal("must not overwrite existing credential")
	}
	read, err := readDuccCredential(home, "alice")
	if err != nil || string(read) != string(data) {
		t.Fatal("credential roundtrip failed")
	}
	for _, name := range []string{"../bob", "/root/bob", "", "alice/bob"} {
		if writeDuccCredential(home, name, data) == nil {
			t.Fatal("unsafe account accepted")
		}
	}
	if err = os.Symlink(file, filepath.Join(home, ".comate/login-user/bob")); err != nil {
		t.Fatal(err)
	}
	if _, err = readDuccCredential(home, "bob"); err == nil {
		t.Fatal("symlink credential accepted")
	}
	other := t.TempDir()
	if err = os.Symlink(other, filepath.Join(t.TempDir(), "unused")); err != nil {
		t.Fatal(err)
	}
}

func TestDuccTransportDoesNotLeakOrRedirect(t *testing.T) {
	reached := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 307) }))
	defer source.Close()
	c := NewClient(source.URL)
	c.SetToken("mul_test_only")
	_, err := duccExchange(context.Background(), c, duccSync{Credential: "secret-should-not-leak", State: "ready"})
	if err == nil || reached || strings.Contains(err.Error(), "secret") {
		t.Fatal("redirect or secret disclosure")
	}
}

func TestDuccAuthUsesOnlyExplicitExecutable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-ducc")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s' '{\"loggedIn\":true,\"authMethod\":\"api_key_helper\"}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if !checkDuccAuth(context.Background(), path, "alice") {
		t.Fatal("fake provider status rejected")
	}
	if checkDuccAuth(context.Background(), filepath.Join(dir, "missing"), "alice") {
		t.Fatal("missing executable accepted")
	}
	if checkDuccAuth(context.Background(), path, "../alice") {
		t.Fatal("unsafe username accepted")
	}
}
