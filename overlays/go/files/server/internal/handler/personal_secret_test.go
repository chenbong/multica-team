package handler

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
	"strings"
	"testing"
)

func secretFixture(t *testing.T) panelRunFixture {
	t.Helper()
	f := newPanelRunFixture(t)
	t.Setenv("MULTICA_PLUGIN_SECRET_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32)))
	f.f.Exec(t, `CREATE TABLE IF NOT EXISTS personal_secret(id uuid PRIMARY KEY,owner_id uuid NOT NULL,name text NOT NULL,description text NOT NULL DEFAULT '',ciphertext bytea NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now())`)
	f.f.Exec(t, `CREATE UNIQUE INDEX IF NOT EXISTS personal_secret_owner_name_idx ON personal_secret(owner_id,name)`)
	f.f.Cleanup(t, `DELETE FROM personal_secret WHERE owner_id=$1 OR owner_id=$2`, f.owner, f.originator)
	return f
}

func secretCall(t *testing.T, f panelRunFixture, method, id, user string, body any, want int, agent bool) *testutil.Response {
	t.Helper()
	path := "/api/me/secrets"
	if agent {
		path = "/api/agent/personal-secrets"
	}
	if id != "" {
		path += "/" + id
	}
	req := testutil.JSONRequest(method, path, body)
	req.Header.Set("X-User-ID", user)
	req.Header.Set("X-Workspace-ID", f.workspace)
	if agent {
		req.Header.Set("X-Actor-Source", "task_token")
		req.Header.Set("X-Agent-ID", f.agent)
		req.Header.Set("X-Task-ID", f.task)
	}
	req = testutil.WithURLParams(req, "secretId", id)
	return testutil.Call(t, f.h.PersonalSecrets, req).Want(want)
}

func TestPersonalSecretOwnerIsolationAndLifecycle(t *testing.T) {
	f := secretFixture(t)
	value := "fixture-private-value-not-for-logs\nsecond line"
	var item personalSecret
	secretCall(t, f, "POST", "", f.owner, map[string]any{"name": "ssh", "description": "test key", "value": value}, 201, false).JSON(&item)
	f.f.Cleanup(t, `DELETE FROM personal_secret WHERE id=$1`, item.ID)
	f.call(t, "GET", "/api/me/secrets", f.token, nil, 403)
	f.call(t, "GET", "/api/agent/personal-secrets/"+item.ID, f.token, nil, 200, "X-User-ID", f.originator, "X-Agent-ID", uuid.NewString())
	listed := secretCall(t, f, "GET", "", f.owner, nil, 200, false).ResponseRecorder.Body.String()
	if strings.Contains(listed, value) || strings.Contains(listed, "ciphertext") {
		t.Fatal("secret value exposed in list")
	}
	if f.f.Count(t, `SELECT count(*) FROM personal_secret WHERE id=$1 AND position($2::bytea in ciphertext)>0`, item.ID, []byte(value)) != 0 {
		t.Fatal("stored plaintext")
	}
	secretCall(t, f, "GET", item.ID, f.originator, nil, 404, false)
	secretCall(t, f, "PUT", item.ID, f.originator, map[string]any{"name": "takeover", "value": "bad"}, 404, false)
	secretCall(t, f, "DELETE", item.ID, f.originator, nil, 404, false)
	secretCall(t, f, "POST", "", f.owner, map[string]any{"name": "ssh", "value": "duplicate"}, 409, false)
	read := secretCall(t, f, "GET", item.ID, f.owner, nil, 200, true).ResponseRecorder
	if !strings.Contains(read.Body.String(), "fixture-private-value") || !strings.Contains(read.Header().Get("Cache-Control"), "no-store") {
		t.Fatal("owner cannot read through live shared run")
	}
	secretCall(t, f, "GET", item.ID, f.originator, nil, 403, true)
	secretCall(t, f, "POST", "", f.owner, map[string]any{"name": "bad", "value": "no"}, 403, true)
	secretCall(t, f, "PUT", item.ID, f.owner, map[string]any{"name": "ssh", "description": "renamed metadata"}, 200, false)
	read = secretCall(t, f, "GET", item.ID, f.owner, nil, 200, true).ResponseRecorder
	if !strings.Contains(read.Body.String(), "fixture-private-value") {
		t.Fatal("metadata edit cleared value")
	}
	secretCall(t, f, "PUT", item.ID, f.owner, map[string]any{"name": "ssh", "value": "latest-fixture"}, 200, false)
	if !strings.Contains(secretCall(t, f, "GET", item.ID, f.owner, nil, 200, true).ResponseRecorder.Body.String(), "latest-fixture") {
		t.Fatal("stale value")
	}
	f.f.Exec(t, `UPDATE agent_task_queue SET issue_id=NULL WHERE id=$1`, f.task)
	secretCall(t, f, "GET", item.ID, f.owner, nil, 200, true)
	f.f.Exec(t, `UPDATE agent_task_queue SET status='completed' WHERE id=$1`, f.task)
	secretCall(t, f, "GET", item.ID, f.owner, nil, 403, true)
	secretCall(t, f, "DELETE", item.ID, f.owner, nil, 204, false)
	secretCall(t, f, "GET", item.ID, f.owner, nil, 404, false)
}

func TestPersonalSecretCiphertextIsBoundToIdentity(t *testing.T) {
	box, _ := secretbox.New(bytes.Repeat([]byte{4}, 32))
	owner, id := uuid.NewString(), uuid.NewString()
	raw, _ := json.Marshal(struct{ Owner, ID, Value string }{owner, id, "fixture"})
	sealed, _ := box.Seal(raw)
	if value, err := openPersonalSecret(box, sealed, owner, id); err != nil || value != "fixture" {
		t.Fatal("round trip failed")
	}
	if _, err := openPersonalSecret(box, sealed, uuid.NewString(), id); err == nil {
		t.Fatal("owner substitution")
	}
	if _, err := openPersonalSecret(box, sealed, owner, uuid.NewString()); err == nil {
		t.Fatal("row substitution")
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := openPersonalSecret(box, sealed, owner, id); err == nil {
		t.Fatal("tampering")
	}
}

func TestPersonalSecretBoundaryValidation(t *testing.T) {
	f := secretFixture(t)
	for _, body := range []map[string]any{{"name": "", "value": "x"}, {"name": "a"}, {"name": "a", "value": ""}, {"name": "a", "value": "x", "owner_id": f.originator}} {
		secretCall(t, f, "POST", "", f.owner, body, 400, false)
	}
	secretCall(t, f, "GET", "not-a-uuid", f.owner, nil, 400, false)
	t.Setenv("MULTICA_PLUGIN_SECRET_KEY", "")
	secretCall(t, f, "POST", "", f.owner, map[string]any{"name": "a", "value": "x"}, 503, false)
}
