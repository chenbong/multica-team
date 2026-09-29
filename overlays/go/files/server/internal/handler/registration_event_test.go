package handler

import (
	"context"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"net/http"
	"testing"
)

func TestFailedRegistrationDoesNotAnnounceConnectedComputer(t *testing.T) {
	if testHandler == nil {
		t.Fatal("isolated test database required")
	}
	ctx := context.Background()
	_, profile := createProfileBackedRuntime(t, ctx, "Missing CLI")
	h := *testHandler
	h.Bus = events.New()
	count := 0
	h.Bus.SubscribeAll(func(e events.Event) {
		if e.Type == protocol.EventDaemonRegister {
			count++
		}
	})
	daemon := "registration-notification-fixture"
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_runtime WHERE daemon_id=$1`, daemon) })
	body := map[string]any{"workspace_id": testWorkspaceID, "daemon_id": daemon, "device_name": "Test computer", "runtimes": []any{}, "failed_profiles": []map[string]string{{"profile_id": profile, "command_name": "missing-cli", "reason": "missing"}}}
	testutil.Call(t, h.DaemonRegister, newRequest(http.MethodPost, "/api/daemon/register", body)).Want(200)
	if count != 0 {
		t.Fatal("empty registration announced success")
	}
	body["failed_profiles"] = []any{}
	body["runtimes"] = []map[string]string{{"name": "Working fixture", "type": "codex", "version": "1.0.0", "status": "online"}}
	testutil.Call(t, h.DaemonRegister, newRequest(http.MethodPost, "/api/daemon/register", body)).Want(200)
	if count != 1 {
		t.Fatal("valid registration did not announce success")
	}
}
