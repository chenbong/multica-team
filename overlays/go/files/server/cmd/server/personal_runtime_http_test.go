package main

import (
	"context"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/realtime"
)

// Opt-in real-router contract tests use a dedicated local fixture database and
// an ephemeral loopback listener. No daemon or external provider is started.
func TestPersonalRuntimeHTTPContract(t *testing.T) {
	if os.Getenv("MULTICA_PERSONAL_RUNTIME_HTTP_TEST") != "1" {
		t.Skip("set MULTICA_PERSONAL_RUNTIME_HTTP_TEST=1 for isolated HTTP tests")
	}
	dbURL := os.Getenv("PERSONAL_RUNTIME_TEST_DATABASE_URL")
	u, err := url.Parse(dbURL)
	if err != nil || u.Hostname() != "127.0.0.1" || u.Path != "/runtime_scope_http_test" {
		t.Fatal("HTTP tests require the loopback-only runtime_scope_http_test fixture database")
	}
	script := os.Getenv("PERSONAL_RUNTIME_TEST_SCRIPT")
	if script == "" || os.Getenv("TEST_NODE_PROJECT") == "" {
		t.Fatal("set PERSONAL_RUNTIME_TEST_SCRIPT and TEST_NODE_PROJECT explicitly")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	hub := realtime.NewHub()
	go hub.Run()
	router := NewRouter(pool, hub, events.New(), analytics.NoopClient{}, nil)
	server := httptest.NewServer(router)
	defer server.Close()
	command := exec.CommandContext(ctx, "node", script)
	command.Env = append(os.Environ(), "PERSONAL_RUNTIME_TEST_API_URL="+server.URL)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("isolated HTTP contract: %v\n%s", err, output)
	}
	t.Log(string(output))
}
