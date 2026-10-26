package handler

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestPublicLiveAuthenticatedIngestBoundary(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	parsed, parseErr := url.Parse(databaseURL)
	if databaseURL == "" || parseErr != nil || (parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" && parsed.Hostname() != "::1") {
		t.Skip("explicit loopback test DB required")
	}
	f := newPanelRunFixture(t)
	ctx := context.Background()
	schema := "live_handler_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := testPool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	cfg, e := pgxpool.ParseConfig(databaseURL)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		pool.Close()
		_, e := testPool.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		if e != nil {
			t.Error(e)
		}
	})
	for _, name := range []string{"536_live_dashboard", "537_live_event_id", "538_live_event_time", "539_live_cache_key", "540_live_snapshot_day", "541_live_config_key"} {
		raw, e := os.ReadFile(filepath.Join("..", "..", "..", "..", "migrations", name+".up.sql"))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = pool.Exec(ctx, string(raw)); e != nil {
			t.Fatal(e)
		}
	}
	s, e := service.NewPublicLiveService(pool, nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	// Readiness is independent of unavailable legacy business-history tables.
	_ = s.Refresh(ctx, "recent")
	if !s.CanRecord() {
		t.Fatal("schema not ready")
	}
	f.h.PublicLive = s
	router := chi.NewRouter()
	router.Use(middleware.Auth(f.h.Queries, nil, nil, nil))
	router.With(RequireHumanActor).Post("/api/live/activity", f.h.RecordLiveActivity)
	pat := "mul_" + uuid.NewString()
	f.f.Insert(t, "personal_access_token", testutil.Cols{"id": uuid.NewString(), "user_id": f.owner, "name": "live fixture", "token_hash": auth.HashToken(pat), "token_prefix": pat[:12]})
	id := uuid.NewString()
	for _, c := range []struct {
		body, token string
		want        int
	}{
		{`{"event_id":"` + id + `","page_view":true}`, pat, 204},
		{`{"event_id":"` + id + `","page_view":true}`, pat, 204},
		{`{"event_id":"` + uuid.NewString() + `","page_view":false}`, pat, 204},
		{`{"event_id":"` + id + `","page_view":true,"user_id":"` + f.originator + `"}`, pat, 400},
		{`{"event_id":"` + id + `","observed_at":"2020-01-01"}`, pat, 400},
		{`{"event_id":"` + id + `","url":"https://private.test"}`, pat, 400},
		{`{"event_id":"not-a-uuid"}`, pat, 400},
		{`{"event_id":"` + id + `"} {}`, pat, 400},
		{`{"event_id":"` + id + `"}`, f.token, 403},
		{`{"event_id":"` + id + `"}`, "", 401},
	} {
		r := httptest.NewRequest("POST", "/api/live/activity", strings.NewReader(c.body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+c.token)
		r.Header.Set("X-User-ID", f.originator)
		r.Header.Set("X-Actor-Source", "human")
		testutil.Call(t, router.ServeHTTP, r).Want(c.want)
	}
	var total, owners int
	_ = pool.QueryRow(ctx, "SELECT count(*),count(*) FILTER(WHERE user_id=$1) FROM live_dashboard_event", f.owner).Scan(&total, &owners)
	if total != 2 || owners != 2 {
		t.Fatal("forged actor or duplicate accepted", total, owners)
	}
	secret := strings.Repeat("l", 32)
	t.Setenv("INFOFLOW_ISSUE_RELAY_TOKEN", secret)
	t.Setenv("INFOFLOW_ISSUE_IDENTITY_DOMAIN", "example.test")
	var email string
	if e := testPool.QueryRow(ctx, `SELECT email FROM "user" WHERE id=$1`, f.owner).Scan(&email); e != nil {
		t.Fatal(e)
	}
	for range 2 {
		req := newRequest("POST", "/api/integrations/infoflow/live-activity", map[string]string{"sender": strings.Split(email, "@")[0], "workspace_id": f.workspace, "robot_id": "fixture-robot", "message_id": "same-accepted-message"})
		req.Header.Set("Authorization", "Bearer "+secret)
		req.Header.Set("X-User-ID", f.originator)
		testutil.Call(t, f.h.InfoFlowLiveActivity, req).Want(204)
	}
	_ = pool.QueryRow(ctx, "SELECT count(*),count(*) FILTER(WHERE user_id=$1) FROM live_dashboard_event", f.owner).Scan(&total, &owners)
	if total != 3 || owners != 3 {
		t.Fatal("bridge sender/retry attribution failed", total, owners)
	}
	unknown := newRequest("POST", "/", map[string]string{"sender": "unknown-unregistered", "workspace_id": f.workspace, "robot_id": "fixture-robot", "message_id": "no-account"})
	unknown.Header.Set("Authorization", "Bearer "+secret)
	testutil.Call(t, f.h.InfoFlowLiveActivity, unknown).Want(403)
	r := newRequest("POST", "/api/live/activity", map[string]any{"event_id": id})
	r.Header.Set("Content-Type", "text/plain")
	testutil.Call(t, f.h.RecordLiveActivity, r).Want(415)
}

func TestPublicLivePublicRoutesAndTrustedBridge(t *testing.T) {
	h := &Handler{}
	testutil.Call(t, h.PublicLiveStatus, httptest.NewRequest("GET", "/", nil)).Want(200)
	testutil.Call(t, h.PublicLiveStats, httptest.NewRequest("GET", "/", nil)).Want(404)
	s, e := service.NewPublicLiveService(nil, func(context.Context, string) ([]byte, error) {
		return []byte(`{"observed_at":"2026-10-22T12:00:00Z","window_start":"2026-10-21T12:00:00Z","active_users":null,"page_views":null,"messages":7,"created_issues":0,"completed_issues":0,"unavailable_metrics":["active_users","page_views"],"raw_email":"private@example.test"}`), nil
	})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	h.PublicLive = s
	if e = s.Refresh(context.Background(), "recent"); e != nil {
		t.Fatal(e)
	}
	r := chi.NewRouter()
	r.Get("/api/public/live/{section}", h.PublicLiveStats)
	out := testutil.Call(t, r.ServeHTTP, httptest.NewRequest("GET", "/api/public/live/recent", nil)).Want(200)
	if strings.Contains(out.Body.String(), "private") || out.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("unsafe public cache")
	}
	testutil.Call(t, r.ServeHTTP, httptest.NewRequest("GET", "/api/public/live/recent?user_id=x", nil)).Want(400)
	testutil.Call(t, r.ServeHTTP, httptest.NewRequest("GET", "/api/public/live/unknown", nil)).Want(404)
	var payload map[string]any
	if json.Unmarshal(out.Body.Bytes(), &payload) != nil {
		t.Fatal("invalid public JSON")
	}
	t.Setenv("INFOFLOW_ISSUE_RELAY_TOKEN", strings.Repeat("x", 32))
	t.Setenv("INFOFLOW_ISSUE_IDENTITY_DOMAIN", "example.test")
	forged := newRequest("POST", "/api/integrations/infoflow/live-activity", map[string]string{"sender": "sender"})
	testutil.Call(t, h.InfoFlowLiveActivity, forged).Want(401)
	if !reservedSlugs["live"] {
		t.Fatal("public route collides with workspace slugs")
	}
}
