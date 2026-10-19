package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
)

func TestUpdateStartupProbationBlocksEveryClaim(t *testing.T) {
	probation := &cli.UpdateProbation{Done: make(chan struct{})}
	d := New(Config{WorkspacesRoot: t.TempDir(), UpdateProbation: probation}, slog.Default())
	if !d.pauseClaims || d.tryEnterClaim() {
		t.Fatal("unconfirmed successor can claim tasks")
	}
	if d.trySetClaimBarrier() {
		t.Fatal("nested upgrade acquired the startup barrier")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if d.awaitUpdateProbation(ctx) {
		t.Fatal("cancelled successor escaped probation")
	}
	close(probation.Done)
	if !d.awaitUpdateProbation(context.Background()) {
		t.Fatal("confirmed startup did not release background update loop")
	}
}

func TestUpdateStartupFailureReleasesHealthListener(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	d := New(Config{Profile: "update-authless-fixture", WorkspacesRoot: t.TempDir(), HealthPort: port}, slog.Default())
	// Auth fails before any discovery loop or user-installed agent can run.
	if err := d.Run(context.Background()); err == nil {
		t.Fatal("authless fixture unexpectedly started")
	}
	if d.rootCtx.Err() == nil {
		t.Fatal("failed startup left background context alive")
	}
	listener, err = net.Listen("tcp", "127.0.0.1:"+fmt.Sprint(port))
	if err != nil {
		t.Fatal("failed startup retained the health port:", err)
	}
	listener.Close()
}

func TestGuardedUpdateResultFollowsStartupOutcome(t *testing.T) {
	for _, failed := range []bool{false, true} {
		statuses := make(chan string, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/daemon/runtimes/runtime/update/request/result" {
				t.Error("unexpected update target")
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			status, _ := body["status"].(string)
			statuses <- status
			_, _ = w.Write([]byte(`{}`))
		}))
		d := &Daemon{client: NewClient(server.URL), logger: slog.Default(), guardedUpdateRuntimeID: "runtime", guardedUpdateRequestID: "request"}
		var startupErr error
		if failed {
			startupErr = errors.New("startup failed; previous executable restored")
		}
		d.FinishGuardedUpdate(startupErr)
		want := "completed"
		if failed {
			want = "failed"
		}
		select {
		case status := <-statuses:
			if status != want {
				t.Fatalf("status=%q, want %q", status, want)
			}
		case <-time.After(time.Second):
			t.Fatal("missing startup result report")
		}
		if d.guardedUpdateRequestID != "" {
			t.Fatal("completed handoff remained pending")
		}
		server.Close()
	}
}

func TestTeamMeshAutoUpdateDefersForBusyFork(t *testing.T) {
	d, restarts := newAutoUpdateTestDaemon(t, "0.6.1-overlay.4")
	withStubRelease(t, &cli.GitHubRelease{TagName: "v0.6.1-overlay.5"}, nil)
	d.activeTasks.Store(1)
	d.tryAutoUpdate(context.Background())
	if restarts.Load() != 0 || d.updating.Load() {
		t.Fatal("busy fork daemon began an upgrade")
	}
	d.activeTasks.Store(0)
	d.claimsInFlight = 1
	d.tryAutoUpdate(context.Background())
	if restarts.Load() != 0 || d.pauseClaims {
		t.Fatal("in-flight claim was interrupted")
	}
}

func TestTeamMeshAutoUpdateForkSuccessAndOfflineFailure(t *testing.T) {
	d, restarts := newAutoUpdateTestDaemon(t, "0.6.1-overlay.4")
	withStubRelease(t, &cli.GitHubRelease{TagName: "v0.6.1-overlay.5"}, nil)
	d.runUpdateFn = func(target string) (string, error) {
		if target != "v0.6.1-overlay.5" {
			t.Fatal(target)
		}
		return "staged", nil
	}
	d.tryAutoUpdate(context.Background())
	if restarts.Load() != 1 || !d.pauseClaims {
		t.Fatal("fork update did not preserve the restart barrier")
	}
	d, restarts = newAutoUpdateTestDaemon(t, "0.6.1-overlay.4")
	withStubRelease(t, nil, os.ErrNotExist)
	d.tryAutoUpdate(context.Background())
	if restarts.Load() != 0 || d.pauseClaims {
		t.Fatal("offline lookup interrupted normal work")
	}
}
