package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/realtime"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestOwnerManagedReadAuthenticatedHTTP(t *testing.T) {
	if os.Getenv("MULTICA_ISSUE_TRANSFER_HTTP_TEST") != "1" {
		t.Skip("opt-in isolated HTTP test")
	}
	dsn := os.Getenv("DATABASE_URL")
	u, err := url.Parse(dsn)
	if err != nil || u.Hostname() != "127.0.0.1" || u.Path != "/issue_transfer_http_test" {
		t.Fatal("dedicated loopback HTTP database required")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	f := testutil.New(pool, "", "")
	machineOwner := f.User(t, "Machine owner", uuid.NewString()+"@example.test")
	agentOwner := f.User(t, "Agent owner", uuid.NewString()+"@example.test")
	source := f.Workspace(t, "Source", "scope-src-"+uuid.NewString())
	target := f.Workspace(t, "Target", "scope-dst-"+uuid.NewString())
	f.Member(t, source, machineOwner, "owner")
	f.Member(t, source, agentOwner, "member")
	f.Member(t, target, agentOwner, "owner")
	f.Member(t, target, machineOwner, "member")
	f.WorkspaceID = source
	f.UserID = machineOwner
	runtime := f.Runtime(t, "Shared machine", testutil.Cols{"visibility": "public"})
	agent := f.Agent(t, "Shared reader", runtime, testutil.Cols{"owner_id": agentOwner})
	task := f.Task(t, agent, testutil.Cols{"runtime_id": runtime, "status": "running"})
	token := "mat_" + uuid.NewString()
	hash := sha256.Sum256([]byte(token))
	encoded := hex.EncodeToString(hash[:])
	f.Insert(t, "task_token", testutil.Cols{"token_hash": encoded, "task_id": task, "agent_id": agent, "workspace_id": source, "user_id": machineOwner, "expires_at": testutil.Raw("now()+interval '10 minutes'")})
	f.InsertNoID(t, "agent_token_read_scope", testutil.Cols{"token_hash": encoded, "task_id": task, "agent_id": agent, "owner_id": agentOwner, "workspace_id": source}, "token_hash=$1", encoded)
	own := f.Issue(t, "Agent owner's context", testutil.Cols{"workspace_id": target, "creator_id": agentOwner})
	other := f.Issue(t, "Machine owner's context", testutil.Cols{"workspace_id": target, "creator_id": machineOwner})
	f.Exec(t, `SELECT team_mesh_ensure_issue_managers($1),team_mesh_ensure_issue_managers($2)`, own, other)
	hub := realtime.NewHub()
	go hub.Run()
	server := httptest.NewServer(NewRouter(pool, hub, events.New(), analytics.NoopClient{}, nil))
	defer server.Close()
	call := func(method, path, ws string, want int) string {
		t.Helper()
		r, _ := http.NewRequest(method, server.URL+path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		if ws != "" {
			r.Header.Set("X-Workspace-ID", ws)
		}
		resp, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != want {
			t.Fatalf("%s %s status=%d want=%d body=%s", method, path, resp.StatusCode, want, raw)
		}
		return string(raw)
	}
	listing := call("GET", "/api/agent/managed-issues", "", 200)
	if !strings.Contains(listing, own) || strings.Contains(listing, other) {
		t.Fatal("HTTP scope used runtime owner instead of agent owner")
	}
	call("GET", "/api/agent/managed-issues/"+own, "", 200)
	call("GET", "/api/agent/managed-issues/"+other, "", 404)
	call("GET", "/api/issues/"+own, target, 404)
	call("PUT", "/api/agent/managed-issues/"+own, "", 405)
	f.Exec(t, `UPDATE agent SET owner_id=$2 WHERE id=$1`, agent, machineOwner)
	call("GET", "/api/agent/managed-issues/"+own, "", 403)
}
