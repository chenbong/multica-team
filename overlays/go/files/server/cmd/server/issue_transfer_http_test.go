package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/realtime"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestIssueTransferAuthenticatedHTTP(t *testing.T) {
	if os.Getenv("MULTICA_ISSUE_TRANSFER_HTTP_TEST") != "1" {
		t.Skip("opt-in isolated HTTP test")
	}
	dsn := os.Getenv("DATABASE_URL")
	u, err := url.Parse(dsn)
	if err != nil || u.Hostname() != "127.0.0.1" || u.Path != "/issue_transfer_http_test" {
		t.Fatal("requires dedicated loopback issue_transfer_http_test database")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	f := testutil.New(pool, "", "")
	user := f.User(t, "Transfer user", uuid.NewString()+"@example.test")
	source := f.Workspace(t, "Source", "src-"+uuid.NewString(), testutil.Cols{"issue_prefix": "SRC"})
	target := f.Workspace(t, "Target", "dst-"+uuid.NewString(), testutil.Cols{"issue_prefix": "DST"})
	f.Member(t, source, user, "owner")
	f.Member(t, target, user, "member")
	f.WorkspaceID = source
	f.UserID = user
	f.Exec(t, `INSERT INTO issue_status(workspace_id,key,name,category,color,position) VALUES($1,'cancelled','Cancelled','closed','#808080',0),($2,'cancelled','Cancelled','closed','#808080',0) ON CONFLICT DO NOTHING`, source, target)
	issue := f.Issue(t, "PiCoKV synthetic migration", testutil.Cols{"status": "cancelled"})
	f.Comment(t, issue, "Historical comment")
	runtime := f.Runtime(t, "Fixture runtime", testutil.Cols{"status": "offline"})
	agent := f.Agent(t, "Historical agent", runtime)
	task := f.Task(t, agent, testutil.Cols{"issue_id": issue, "runtime_id": runtime, "status": "completed", "completed_at": testutil.Raw("now()")})
	f.Insert(t, "task_message", testutil.Cols{"task_id": task, "seq": 1, "type": "text", "content": "Historical transcript"})
	token := "mul_" + uuid.NewString()
	hash := sha256.Sum256([]byte(token))
	f.Insert(t, "personal_access_token", testutil.Cols{"user_id": user, "name": "isolated-transfer-test", "token_hash": hex.EncodeToString(hash[:]), "token_prefix": token[:12]})
	hub := realtime.NewHub()
	go hub.Run()
	server := httptest.NewServer(NewRouter(pool, hub, events.New(), analytics.NoopClient{}, nil))
	defer server.Close()
	call := func(ws, method, path string, body any, want int) []byte {
		t.Helper()
		var payload []byte
		if body != nil {
			payload, _ = json.Marshal(body)
		}
		r, err := http.NewRequest(method, server.URL+path, bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Workspace-ID", ws)
		r.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != want {
			t.Fatalf("%s %s: status %d expected %d: %s", method, path, resp.StatusCode, want, data)
		}
		return data
	}
	var preview struct {
		Fingerprint string   `json:"fingerprint"`
		Blockers    []string `json:"blockers"`
	}
	if err = json.Unmarshal(call(source, "POST", "/api/issues/"+issue+"/transfer-preview", map[string]string{"target_workspace_id": target}, 200), &preview); err != nil || len(preview.Blockers) > 0 || preview.Fingerprint == "" {
		t.Fatalf("bad preview %+v %v", preview, err)
	}
	call(source, "POST", "/api/issues/"+issue+"/transfer", map[string]string{"target_workspace_id": target, "fingerprint": preview.Fingerprint}, 200)
	call(target, "GET", "/api/issues/"+issue, nil, 200)
	call(source, "GET", "/api/issues/"+issue, nil, 404)
	call(target, "GET", "/api/issues/"+issue+"/comments", nil, 200)
	call(target, "GET", "/api/issues/"+issue+"/task-runs", nil, 200)
	data := call(target, "GET", "/api/tasks/"+task+"/messages", nil, 200)
	if !bytes.Contains(data, []byte("Historical transcript")) {
		t.Fatal("history was not readable in destination")
	}
	call(source, "GET", "/api/tasks/"+task+"/messages", nil, 404)
	// History is readable but does not authorize dispatch on the old host.
	call(target, "POST", "/api/issues/"+issue+"/rerun", map[string]string{"task_id": task}, 403)
	var total int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1`, issue).Scan(&total); err != nil || total != 1 {
		t.Fatalf("rerun dispatched work: %d %v", total, err)
	}
}
