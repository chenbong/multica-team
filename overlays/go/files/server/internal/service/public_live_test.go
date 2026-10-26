package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Every live test owns a uniquely named schema on the managed loopback test DB.
// Refuse nonlocal database URLs; neither migrations nor fixtures may hit live.
func liveTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("DATABASE_URL")
	parsed, parseErr := url.Parse(databaseURL)
	if databaseURL == "" || parseErr != nil || (parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" && parsed.Hostname() != "::1") {
		t.Skip("live DB tests require an explicit loopback test DATABASE_URL")
	}
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	base, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	schema := "live_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = base.Exec(context.Background(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	cfg = cfg.Copy()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, err := base.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		if err != nil {
			t.Error(err)
		}
		base.Close()
	})
	root := os.Getenv("LIVE_TEST_REPO")
	if root == "" {
		root = filepath.Join("..", "..", "..", "..")
	}
	for _, name := range []string{"536_live_dashboard", "537_live_event_id", "538_live_event_time", "539_live_cache_key", "540_live_snapshot_day", "541_live_config_key"} {
		raw, err := os.ReadFile(filepath.Join(root, "migrations", name+".up.sql"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(context.Background(), string(raw)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	_, err = pool.Exec(context.Background(), `
 CREATE TABLE "user"(id uuid,email text);
 CREATE TABLE agent(id uuid,archived_at timestamptz);
 CREATE TABLE agent_runtime(id uuid,daemon_id text,owner_id uuid,created_at timestamptz,status text,last_seen_at timestamptz);
 CREATE TABLE issue(id uuid,creator_id uuid,creator_type text,created_at timestamptz);
 CREATE TABLE chat_session(id uuid);
 CREATE TABLE chat_message(chat_session_id uuid,created_at timestamptz,message_kind text,role text);
 CREATE TABLE comment(issue_id uuid,created_at timestamptz,deleted_at timestamptz,type text,author_type text);
 CREATE TABLE activity_log(issue_id uuid,workspace_id uuid,created_at timestamptz,action text,details jsonb);
 CREATE TABLE issue_status(workspace_id uuid,key text,category text);
 CREATE TABLE infoflow_issue_source(user_id uuid,comment_id uuid,created_at timestamptz);
 CREATE TABLE agent_task_queue(agent_id uuid,issue_id uuid,chat_session_id uuid,runtime_id uuid,status text,started_at timestamptz,completed_at timestamptz,originator_user_id uuid);
 `)
	if err != nil {
		t.Fatal(err)
	}
	return pool
}
func liveService(t *testing.T, pool *pgxpool.Pool, loader PublicLiveLoader) *PublicLiveService {
	t.Helper()
	s, e := NewPublicLiveService(pool, loader)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	return s
}
func liveObject(t *testing.T, s *PublicLiveService, section string) map[string]any {
	t.Helper()
	v, ok := s.Get(section)
	if !ok || v.Data == nil {
		t.Fatal("missing cache", section)
	}
	var out map[string]any
	if json.Unmarshal(v.Data, &out) != nil {
		t.Fatal("invalid cache")
	}
	return out
}
func recentJSON(at time.Time, count int) []byte {
	return []byte(fmt.Sprintf(`{"observed_at":%q,"window_start":%q,"active_users":1,"page_views":%d,"messages":2,"created_issues":1,"completed_issues":0,"unavailable_metrics":[],"email":"secret@example.test"}`, at.Format(time.RFC3339Nano), at.Add(-24*time.Hour).Format(time.RFC3339Nano), count))
}

func TestPublicLiveSQLCollectionCoverageIdempotencyAndSnapshots(t *testing.T) {
	ctx := context.Background()
	pool := liveTestPool(t)
	s := liveService(t, pool, nil)
	var before *time.Time
	if e := pool.QueryRow(ctx, "SELECT collection_started_at FROM live_dashboard_config").Scan(&before); e != nil || before != nil {
		t.Fatal("migration must not start collection", e)
	}
	if s.pool == pool || s.pool.Config().MaxConns != 2 {
		t.Fatal("must use separate bounded pool")
	}
	if err := s.Refresh(ctx, "recent"); err != nil {
		t.Fatal(err)
	}
	var started time.Time
	if e := pool.QueryRow(ctx, "SELECT collection_started_at FROM live_dashboard_config").Scan(&started); e != nil || time.Since(started) > time.Minute {
		t.Fatal("actual collection start not recorded", e)
	}
	if out := liveObject(t, s, "recent"); out["active_users"] != float64(0) || out["page_views"] != float64(0) || len(out["partial_metrics"].([]any)) != 2 {
		t.Fatal("partial window must show observed counts with a coverage flag", out)
	}
	user, second := uuid.NewString(), uuid.NewString()
	if _, e := pool.Exec(ctx, `INSERT INTO "user" VALUES($1,'alice@example.test'),($2,'bob@example.test')`, user, second); e != nil {
		t.Fatal(e)
	}
	id := uuid.NewString()
	var wg sync.WaitGroup
	var failures atomic.Int32
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.Record(ctx, user, id, true) != nil {
				failures.Add(1)
			}
		}()
	}
	wg.Wait()
	if failures.Load() != 0 {
		t.Fatal("concurrent report failed")
	}
	for _, p := range []struct {
		u  string
		pv bool
	}{{user, true}, {user, false}, {second, false}} {
		if e := s.Record(ctx, p.u, uuid.NewString(), p.pv); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := pool.Exec(ctx, `UPDATE live_dashboard_config SET collection_started_at=now()-interval '9 days';INSERT INTO infoflow_issue_source VALUES($1,$2,now()),($3,$4,now())`, pgx.QueryExecModeSimpleProtocol, user, uuid.NewString(), uuid.NewString(), uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	// The verified InfoFlow-only sender and two web users give three users,
	// while two distinct page events (not retry attempts) give two views.
	for _, section := range []string{"realtime", "recent", "history"} {
		if e := s.Refresh(ctx, section); e != nil {
			t.Fatalf("%s: %v", section, e)
		}
	}
	out := liveObject(t, s, "recent")
	if out["active_users"] != float64(3) || out["page_views"] != float64(2) {
		t.Fatal("wrong users/PV", out)
	}
	var events int
	if e := pool.QueryRow(ctx, "SELECT count(*) FROM live_dashboard_event").Scan(&events); e != nil || events != 4 {
		t.Fatal("retry counted twice", events, e)
	}
	// Long-term historical snapshots freeze even if source rows are edited.
	old := liveObject(t, s, "history")["days"].([]any)[0].(map[string]any)
	if _, e := pool.Exec(ctx, `INSERT INTO live_dashboard_event VALUES($1,$2,date_trunc('day',now() AT TIME ZONE 'Asia/Shanghai') AT TIME ZONE 'Asia/Shanghai'-interval '7 days'+interval '1 hour',true)`, uuid.NewString(), user); e != nil {
		t.Fatal(e)
	}
	if e := s.Refresh(ctx, "history"); e != nil {
		t.Fatal(e)
	}
	fresh := liveObject(t, s, "history")["days"].([]any)[0].(map[string]any)
	if fmt.Sprint(old) != fmt.Sprint(fresh) {
		t.Fatal("frozen snapshot changed", old, fresh)
	}
	restored := liveService(t, pool, nil)
	restored.restore(ctx)
	cached, _ := restored.Get("recent")
	if cached.Data == nil || !cached.Stale {
		t.Fatal("restart lost last good")
	}
	// Record reception time is server-generated, not the user's wall clock.
	var at time.Time
	if e := pool.QueryRow(ctx, "SELECT observed_at FROM live_dashboard_event WHERE event_id=$1", id).Scan(&at); e != nil || time.Since(at) > time.Minute {
		t.Fatal("bad receipt timestamp", e)
	}
}

func TestPublicLiveRollingWindowAndMasking(t *testing.T) {
	pool := liveTestPool(t)
	ctx := context.Background()
	s := liveService(t, pool, nil)
	user, issue := uuid.NewString(), uuid.NewString()
	_, e := pool.Exec(ctx, `UPDATE live_dashboard_config SET collection_started_at=now()-interval '9 days';INSERT INTO "user" VALUES($1,'private-account@sz.baidu.com');INSERT INTO issue VALUES($2,$1,'member',now()-interval '23 hours');INSERT INTO issue VALUES($3,$1,'member',now()-interval '25 hours');INSERT INTO live_dashboard_event VALUES($4,$1,now()-interval '23 hours',true),($5,$1,now()-interval '25 hours',true)`, pgx.QueryExecModeSimpleProtocol, user, issue, uuid.NewString(), uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	for _, key := range []string{"realtime", "recent", "history"} {
		if e = s.Refresh(ctx, key); e != nil {
			t.Fatal(key, e)
		}
	}
	out := liveObject(t, s, "recent")
	if out["created_issues"] != float64(1) || out["page_views"] != float64(1) {
		t.Fatal("not rolling 24h", out)
	}
	v, _ := s.Get("realtime")
	text := string(v.Data)
	if strings.Contains(text, "private-account") || strings.Contains(text, "baidu.com") || strings.Contains(text, user) || strings.Contains(text, issue) || !strings.Contains(text, "@pr***z") {
		t.Fatal("public projection leaked or lost mask", text)
	}
	if liveObject(t, s, "realtime")["active_users"] != float64(0) {
		t.Fatal("hour should exclude yesterday")
	}
}

func TestPublicLiveMachinesAndRunningUnitsAreDeduplicated(t *testing.T) {
	pool := liveTestPool(t)
	ctx := context.Background()
	s := liveService(t, pool, nil)
	agent, r1, r2, issue1, issue2, chat := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, e := pool.Exec(ctx, `INSERT INTO agent VALUES($1,NULL);INSERT INTO agent_runtime VALUES($2,'same-daemon',NULL,now(),'online',now()),($3,'same-daemon',NULL,now(),'online',now());INSERT INTO agent_task_queue VALUES($1,$4,$6,$2,'running',now(),NULL,NULL),($1,$5,NULL,$3,'running',now(),NULL,NULL)`, pgx.QueryExecModeSimpleProtocol, agent, r1, r2, issue1, issue2, chat)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Refresh(ctx, "realtime"); e != nil {
		t.Fatal(e)
	}
	out := liveObject(t, s, "realtime")
	if out["working_agents"] != float64(1) || out["working_issues"] != float64(2) || out["working_chats"] != float64(1) || out["online_machines"] != float64(1) {
		t.Fatal("runtime/agent counts not deduplicated", out)
	}
}

func TestPublicLiveCacheFailureSanitizerAndNoReadScans(t *testing.T) {
	var calls atomic.Int32
	var fail atomic.Bool
	at := time.Now().UTC()
	s := liveService(t, nil, func(context.Context, string) ([]byte, error) {
		calls.Add(1)
		if fail.Load() {
			return nil, errors.New("fixture failure")
		}
		return recentJSON(at, 12), nil
	})
	if e := s.Refresh(context.Background(), "recent"); e != nil {
		t.Fatal(e)
	}
	for range 1000 {
		s.Get("recent")
	}
	if calls.Load() != 1 {
		t.Fatal("visitor caused SQL scan")
	}
	fail.Store(true)
	if s.Refresh(context.Background(), "recent") == nil {
		t.Fatal("failure not propagated")
	}
	v, _ := s.Get("recent")
	if !v.Stale || strings.Contains(string(v.Data), "secret") || liveObject(t, s, "recent")["page_views"] != float64(12) {
		t.Fatal("lost safe last-good cache")
	}
	v.Data[0] = 'x'
	if _, _, e := sanitizeLive("recent", s.cache["recent"].Data); e != nil {
		t.Fatal("Get exposed shared cache")
	}
	for _, raw := range []string{`{}`, strings.Replace(string(recentJSON(at, 1)), `"page_views":1`, `"page_views":-1`, 1), strings.Replace(string(recentJSON(at, 1)), at.Add(-24*time.Hour).Format(time.RFC3339Nano), at.Add(-time.Hour).Format(time.RFC3339Nano), 1)} {
		if _, _, e := sanitizeLive("recent", []byte(raw)); e == nil {
			t.Fatal("malformed payload accepted", raw)
		}
	}
	if e := s.Record(context.Background(), uuid.NewString(), uuid.NewString(), true); !errors.Is(e, ErrLiveUnavailable) {
		t.Fatal("readonly source accepted telemetry")
	}
}

func TestPublicLiveSerializedJobsShutdownAndRateLimit(t *testing.T) {
	var running, peak atomic.Int32
	s := liveService(t, nil, func(ctx context.Context, _ string) ([]byte, error) {
		n := running.Add(1)
		defer running.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
			return recentJSON(time.Now(), 1), nil
		}
	})
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() { defer wg.Done(); _ = s.Refresh(context.Background(), "recent") }()
	}
	wg.Wait()
	if peak.Load() != 1 {
		t.Fatal("jobs not serial")
	}
	ctx, cancel := context.WithCancel(context.Background())
	go s.Run(ctx)
	time.Sleep(20 * time.Millisecond)
	cancel()
	s.Close()
	if s.CanRecord() {
		t.Fatal("closed service still writable")
	}
	pool := liveTestPool(t)
	native := liveService(t, pool, nil)
	if e := native.ensureReady(context.Background()); e != nil {
		t.Fatal(e)
	}
	user := uuid.NewString()
	if _, e := pool.Exec(context.Background(), `INSERT INTO "user" VALUES($1,'fixture@example.test')`, user); e != nil {
		t.Fatal(e)
	}
	for range 120 {
		if e := native.Record(context.Background(), user, uuid.NewString(), false); e != nil {
			t.Fatal(e)
		}
	}
	if e := native.Record(context.Background(), user, uuid.NewString(), false); !errors.Is(e, ErrLiveRateLimited) {
		t.Fatal("unbounded ingest", e)
	}
}

// Explicit loopback-only preview harness. Source queries remain read-only;
// production Go code owns scheduling, validation and local DB persistence.
func TestPublicLiveLocalPreviewServer(t *testing.T) {
	if os.Getenv("LIVE_LOCAL_PREVIEW") != "1" {
		t.Skip("explicit local preview only")
	}
	url := os.Getenv("DATABASE_URL")
	if !strings.Contains(url, "@127.0.0.1:55451/") {
		t.Fatal("preview requires explicit loopback test DB")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	base, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	if _, err = base.Exec(context.Background(), "CREATE SCHEMA IF NOT EXISTS live_preview_local_v1"); err != nil {
		t.Fatal(err)
	}
	cfg = cfg.Copy()
	cfg.ConnConfig.RuntimeParams["search_path"] = "live_preview_local_v1,public"
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	root := os.Getenv("LIVE_TEST_REPO")
	if root == "" {
		t.Fatal("explicit preview repository required")
	}
	for _, name := range []string{"536_live_dashboard", "537_live_event_id", "538_live_event_time", "539_live_cache_key", "540_live_snapshot_day", "541_live_config_key"} {
		raw, e := os.ReadFile(filepath.Join(root, "migrations", name+".up.sql"))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = pool.Exec(context.Background(), string(raw)); e != nil {
			t.Fatal(e)
		}
	}
	loader := func(ctx context.Context, section string) ([]byte, error) {
		req, _ := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1:19997/api/public/live/"+section, nil)
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			return nil, e
		}
		defer resp.Body.Close()
		var envelope PublicLiveEnvelope
		if e = json.NewDecoder(resp.Body).Decode(&envelope); e != nil {
			return nil, e
		}
		if envelope.Stale || envelope.Data == nil {
			return nil, ErrLiveUnavailable
		}
		return envelope.Data, nil
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	s := liveService(t, pool, loader)
	go s.Run(ctx)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/public/live/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"enabled": true, "collection_enabled": false})
	})
	mux.HandleFunc("/api/public/live/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != "GET" || r.URL.RawQuery != "" {
			http.Error(w, "Invalid request", 400)
			return
		}
		v, ok := s.Get(strings.TrimPrefix(r.URL.Path, "/api/public/live/"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(v)
	})
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"allow_signup":true,"allowed_email_domains":["baidu.com"]}`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "Local read-only preview", 401) })
	server := &http.Server{Addr: "127.0.0.1:19999", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		stop, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(stop)
	}()
	t.Log("local Go statistics API listening on loopback 19999; telemetry disabled for read-only source")
	if e := server.ListenAndServe(); e != nil && !errors.Is(e, http.ErrServerClosed) {
		t.Fatal(e)
	}
}
