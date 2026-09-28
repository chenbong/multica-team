package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/multica-ai/multica/server/internal/testutil"
	"net/http"
	"strings"
	"testing"
)

func TestDuccCredentialIsolationAndRevocation(t *testing.T) {
	if testHandler == nil {
		t.Fatal("isolated database required")
	}
	t.Setenv("MULTICA_PLUGIN_SECRET_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	ctx := context.Background()
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM ducc_machine WHERE user_id=$1`, testUserID)
		testPool.Exec(ctx, `DELETE FROM ducc_credential WHERE user_id=$1`, testUserID)
	})
	call := func(h http.HandlerFunc, body any, status int) map[string]any {
		req := newRequest("POST", "/api/ducc-test", body)
		req.Header.Set("Authorization", "Bearer mul_fixture")
		result := testutil.Call(t, h, req).Want(status)
		var out map[string]any
		if status == 200 {
			json.Unmarshal(result.Body.Bytes(), &out)
		}
		return out
	}
	call(testHandler.UpdateDuccCredential, map[string]any{"action": "enable", "enabled": true}, 204)
	poll := map[string]any{"daemon_id": "test-daemon", "name": "Test computer", "client_version": "test", "phase": "poll", "installed": true, "state": "present"}
	reply := call(testHandler.SyncDuccCredential, poll, 200)
	if reply["action"] != "import" {
		t.Fatal("import not offered")
	}
	upload := map[string]any{"daemon_id": "test-daemon", "name": "Test computer", "client_version": "test", "phase": "upload", "installed": true, "state": "ready", "credential": "test-secret-do-not-log", "request_id": reply["request_id"], "expected_version": reply["version"]}
	call(testHandler.SyncDuccCredential, upload, 200)
	var sealed []byte
	if err := testPool.QueryRow(ctx, `SELECT sealed FROM ducc_credential WHERE user_id=$1`, testUserID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sealed), "test-secret") {
		t.Fatal("plaintext credential stored")
	}
	status := testutil.Call(t, testHandler.GetDuccCredential, newRequest("GET", "/api/me/ducc", nil)).Want(200)
	if strings.Contains(status.Body.String(), "test-secret") {
		t.Fatal("browser received secret")
	}
	call(testHandler.SyncDuccCredential, upload, 409)
	download := map[string]any{"daemon_id": "new-daemon", "name": "New computer", "client_version": "test", "phase": "download", "installed": true, "state": "missing", "expected_version": 1}
	got := call(testHandler.SyncDuccCredential, download, 200)
	if got["credential"] != "test-secret-do-not-log" {
		t.Fatal("credential not restored")
	}
	other := dbfx.User(t, "Other ducc user", "ducc-other@example.com")
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM ducc_machine WHERE user_id=$1`, other)
		testPool.Exec(ctx, `DELETE FROM ducc_credential WHERE user_id=$1`, other)
	})
	cross := newRequest("POST", "/api/daemon/ducc/sync", download)
	cross.Header.Set("X-User-ID", other)
	cross.Header.Set("Authorization", "Bearer mul_fixture")
	testutil.Call(t, testHandler.SyncDuccCredential, cross).Want(409)
	crossImport := newRequest("POST", "/api/me/ducc", map[string]any{"action": "import", "daemon_id": "test-daemon"})
	crossImport.Header.Set("X-User-ID", other)
	testutil.Call(t, testHandler.UpdateDuccCredential, crossImport).Want(409)
	actor := newRequest("POST", "/api/daemon/ducc/sync", download)
	actor.Header.Set("Authorization", "Bearer mul_fixture")
	actor.Header.Set("X-Actor-Source", "task_token")
	testutil.Call(t, testHandler.SyncDuccCredential, actor).Want(403)
	browser := newRequest("POST", "/api/daemon/ducc/sync", download)
	testutil.Call(t, testHandler.SyncDuccCredential, browser).Want(403)
	call(testHandler.UpdateDuccCredential, map[string]any{"action": "import", "daemon_id": "not-my-machine"}, 409)
	call(testHandler.UpdateDuccCredential, map[string]any{"action": "remove"}, 204)
	call(testHandler.SyncDuccCredential, download, 409)
}
