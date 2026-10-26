package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrLiveUnavailable = errors.New("live collection unavailable")
var ErrLiveRateLimited = errors.New("live collection rate limited")

type PublicLiveLoader func(context.Context, string) ([]byte, error)
type PublicLiveEnvelope struct {
	UpdatedAt      *time.Time      `json:"updated_at"`
	Stale          bool            `json:"stale"`
	RefreshSeconds int             `json:"refresh_seconds"`
	Data           json.RawMessage `json:"data"`
}
type liveMeta struct {
	ObservedAt  time.Time `json:"observed_at"`
	Unavailable []string  `json:"unavailable_metrics"`
	Partial     []string  `json:"partial_metrics"`
}
type liveEvent struct {
	Kind    string    `json:"kind"`
	At      time.Time `json:"at"`
	Count   int64     `json:"count"`
	Members []string  `json:"members"`
}
type liveRealtime struct {
	liveMeta
	WindowStart    time.Time   `json:"window_start"`
	ActiveUsers    *int64      `json:"active_users"`
	WorkingAgents  *int64      `json:"working_agents"`
	WorkingIssues  *int64      `json:"working_issues"`
	WorkingChats   *int64      `json:"working_chats"`
	OnlineMachines *int64      `json:"online_machines"`
	Events         []liveEvent `json:"events"`
	Scale          liveScale   `json:"scale"`
}
type liveRecent struct {
	liveMeta
	WindowStart time.Time `json:"window_start"`
	ActiveUsers *int64    `json:"active_users"`
	PageViews   *int64    `json:"page_views"`
	Messages    *int64    `json:"messages"`
	Created     *int64    `json:"created_issues"`
	Completed   *int64    `json:"completed_issues"`
}
type liveDay struct {
	Date        string `json:"date"`
	Partial     bool   `json:"partial"`
	ActiveUsers *int64 `json:"active_users"`
	PageViews   *int64 `json:"page_views"`
	Messages    *int64 `json:"messages"`
	Created     *int64 `json:"created_issues"`
	Completed   *int64 `json:"completed_issues"`
}
type liveScale struct {
	Users    *int64 `json:"users"`
	Issues   *int64 `json:"issues"`
	Agents   *int64 `json:"agents"`
	Machines *int64 `json:"machines"`
}
type liveCapabilities struct {
	MultiAgent *int64 `json:"multi_agent_issues"`
	Infoflow   *int64 `json:"infoflow_inputs"`
	Wakeups    *int64 `json:"wakeup_triggers"`
}
type liveHistory struct {
	liveMeta
	Days         []liveDay        `json:"days"`
	Scale        liveScale        `json:"scale"`
	Capabilities liveCapabilities `json:"capabilities"`
}
type liveLimit struct {
	start time.Time
	count int
}
type PublicLiveService struct {
	pool           *pgxpool.Pool
	load           PublicLiveLoader
	readOnlySource bool
	now            func() time.Time
	mu             sync.RWMutex
	refreshMu      sync.Mutex
	cache          map[string]PublicLiveEnvelope
	limits         map[string]liveLimit
	ready          bool
	running        bool
	closed         bool
	cancel         context.CancelFunc
	done           chan struct{}
	closeOnce      sync.Once
}

func NewPublicLiveService(parent *pgxpool.Pool, loader PublicLiveLoader) (*PublicLiveService, error) {
	s := &PublicLiveService{load: loader, readOnlySource: loader != nil, now: time.Now, cache: map[string]PublicLiveEnvelope{}, limits: map[string]liveLimit{}, done: make(chan struct{})}
	for _, key := range []string{"realtime", "recent", "history"} {
		s.cache[key] = PublicLiveEnvelope{RefreshSeconds: liveInterval(key)}
	}
	if parent != nil {
		cfg := parent.Config().Copy()
		cfg.MaxConns = 2
		cfg.MinConns = 0
		cfg.MinIdleConns = 0
		if cfg.ConnConfig.RuntimeParams == nil {
			cfg.ConnConfig.RuntimeParams = map[string]string{}
		}
		cfg.ConnConfig.RuntimeParams["application_name"] = "team-mesh-public-live"
		cfg.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
		cfg.ConnConfig.RuntimeParams["lock_timeout"] = "1000"
		pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
		if err != nil {
			return nil, err
		}
		s.pool = pool
	}
	if loader == nil {
		if s.pool == nil {
			return nil, ErrLiveUnavailable
		}
		s.load = s.loadSQL
	}
	return s, nil
}
func liveInterval(section string) int {
	switch section {
	case "realtime":
		return 10
	case "recent":
		return 10
	case "history":
		return 300
	}
	return 0
}
func liveDefinitionVersion(section string) int {
	if section == "realtime" {
		return 3
	}
	return 2
}

// No HTTP read triggers a database query. These envelopes are immutable copies.
func (s *PublicLiveService) Get(section string) (PublicLiveEnvelope, bool) {
	s.mu.RLock()
	v, ok := s.cache[section]
	s.mu.RUnlock()
	if !ok {
		return PublicLiveEnvelope{}, false
	}
	v.Data = append(json.RawMessage(nil), v.Data...)
	if v.UpdatedAt != nil {
		at := *v.UpdatedAt
		v.UpdatedAt = &at
		if s.now().Sub(at) > time.Duration(v.RefreshSeconds*2)*time.Second {
			v.Stale = true
		}
	}
	return v, true
}
func (s *PublicLiveService) CanRecord() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ready && !s.readOnlySource && !s.closed
}
func (s *PublicLiveService) ensureReady(ctx context.Context) error {
	if s.pool == nil {
		return nil
	}
	var since *time.Time
	if err := s.pool.QueryRow(ctx, "SELECT collection_started_at FROM live_dashboard_config WHERE id=1").Scan(&since); err != nil {
		return err
	}
	// Installing migrations does not start collection. Stamp the first actual
	// native worker activation, once, and never change it on restart.
	if since == nil && !s.readOnlySource {
		if err := s.pool.QueryRow(ctx, "UPDATE live_dashboard_config SET collection_started_at=coalesce(collection_started_at,clock_timestamp()) WHERE id=1 RETURNING collection_started_at").Scan(&since); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.ready = true
	s.mu.Unlock()
	return nil
}
func (s *PublicLiveService) Record(ctx context.Context, user, event string, pageView bool) error {
	if !s.CanRecord() {
		return ErrLiveUnavailable
	}
	now := s.now()
	s.mu.Lock()
	if len(s.limits) > 10000 {
		for key, limit := range s.limits {
			if now.Sub(limit.start) > 2*time.Minute {
				delete(s.limits, key)
			}
		}
	}
	limit := s.limits[user]
	if now.Sub(limit.start) >= time.Minute {
		limit = liveLimit{start: now}
	}
	if limit.count >= 120 || len(s.limits) > 10000 {
		s.mu.Unlock()
		return ErrLiveRateLimited
	}
	limit.count++
	s.limits[user] = limit
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err := s.pool.Exec(ctx, `INSERT INTO live_dashboard_event(event_id,user_id,is_page_view) SELECT $1::uuid,$2::uuid,$3 WHERE EXISTS(SELECT 1 FROM "user" WHERE id=$2::uuid) ON CONFLICT(event_id) DO NOTHING`, event, user, pageView)
	return err
}
func (s *PublicLiveService) Run(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	s.mu.Lock()
	if s.running || s.closed {
		s.mu.Unlock()
		cancel()
		return
	}
	s.running = true
	s.cancel = cancel
	s.mu.Unlock()
	defer close(s.done)
	defer cancel()
	s.restore(ctx)
	deadlines := map[string]time.Time{}
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		for _, section := range []string{"realtime", "recent", "history"} {
			if ctx.Err() != nil {
				return
			}
			if s.now().Before(deadlines[section]) {
				continue
			}
			deadlines[section] = s.now().Add(time.Duration(liveInterval(section)) * time.Second)
			if err := s.Refresh(ctx, section); err != nil && ctx.Err() == nil {
				slog.Warn("public live refresh delayed", "section", section)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}
func (s *PublicLiveService) Close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		cancel, running := s.cancel, s.running
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if running {
			<-s.done
		}
		if s.pool != nil {
			s.pool.Close()
		}
	})
}
func (s *PublicLiveService) Refresh(parent context.Context, section string) (err error) {
	if liveInterval(section) == 0 {
		return ErrLiveUnavailable
	}
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	defer func() {
		if err != nil {
			s.mu.Lock()
			v := s.cache[section]
			v.Stale = true
			s.cache[section] = v
			s.mu.Unlock()
		}
	}()
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	defer cancel()
	if err = s.ensureReady(ctx); err != nil {
		return err
	}
	raw, err := s.load(ctx, section)
	if err != nil {
		return err
	}
	clean, observed, err := sanitizeLive(section, raw)
	if err != nil {
		return err
	}
	if s.pool != nil {
		clean, err = s.persist(ctx, section, clean, observed)
		if err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.cache[section] = PublicLiveEnvelope{UpdatedAt: &observed, RefreshSeconds: liveInterval(section), Data: clean}
	s.mu.Unlock()
	return nil
}
func (s *PublicLiveService) restore(parent context.Context) {
	if s.pool == nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	rows, err := s.pool.Query(ctx, "SELECT section,payload,definition_version FROM live_dashboard_cache")
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var section string
		var raw []byte
		var version int
		if rows.Scan(&section, &raw, &version) != nil || version != liveDefinitionVersion(section) {
			continue
		}
		clean, at, err := sanitizeLive(section, raw)
		if err != nil {
			continue
		}
		s.mu.Lock()
		s.cache[section] = PublicLiveEnvelope{UpdatedAt: &at, Stale: true, RefreshSeconds: liveInterval(section), Data: clean}
		s.mu.Unlock()
	}
}
func (s *PublicLiveService) persist(ctx context.Context, section string, clean []byte, observed time.Time) ([]byte, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if section == "history" {
		var history liveHistory
		if err = json.Unmarshal(clean, &history); err != nil {
			return nil, err
		}
		for i, day := range history.Days {
			payload, _ := json.Marshal(day)
			// Retain frozen business counts from v1 while adding newly recoverable
			// telemetry and its coverage flag; never rewrite the original archive.
			if _, err = tx.Exec(ctx, `INSERT INTO live_dashboard_snapshot(day,definition_version,payload)
 SELECT $1::date,2,coalesce((SELECT payload FROM live_dashboard_snapshot WHERE day=$1::date AND definition_version=1),$2::jsonb)
 || jsonb_build_object('active_users',$2::jsonb->'active_users','page_views',$2::jsonb->'page_views','partial',$2::jsonb->'partial')
 ON CONFLICT(day,definition_version) DO NOTHING`, day.Date, payload); err != nil {
				return nil, err
			}
			var saved []byte
			if err = tx.QueryRow(ctx, "SELECT payload FROM live_dashboard_snapshot WHERE day=$1::date AND definition_version=2", day.Date).Scan(&saved); err != nil {
				return nil, err
			}
			if err = json.Unmarshal(saved, &history.Days[i]); err != nil || history.Days[i].Date != day.Date {
				return nil, ErrLiveUnavailable
			}
		}
		clean, _ = json.Marshal(history)
		clean, _, err = sanitizeLive(section, clean)
		if err != nil {
			return nil, err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO live_dashboard_cache(section,payload,observed_at,definition_version) VALUES($1,$2,$3,$4) ON CONFLICT(section) DO UPDATE SET payload=EXCLUDED.payload,observed_at=EXCLUDED.observed_at,definition_version=EXCLUDED.definition_version`, section, clean, observed, liveDefinitionVersion(section))
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return clean, nil
}

func (s *PublicLiveService) loadSQL(ctx context.Context, section string) ([]byte, error) {
	query, err := publicLiveSQL(section)
	if err != nil {
		return nil, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var result []byte
	if err = tx.QueryRow(ctx, query).Scan(&result); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

// Coverage begins at deployment, never at an invented historical timestamp.
// InfoFlow task sources are already verified; chat activity is recorded by the
// trusted bridge endpoint when ordinary (unbound) messages are accepted.
func publicLiveSQL(section string) (string, error) {
	var query, at, from string
	switch section {
	case "realtime":
		query = publicLiveRealtimeSQL
		at = "(SELECT at FROM bounds)"
		from = at + "-interval '1 hour'"
	case "recent":
		query = publicLiveRecentSQL
		at = "b.at"
		from = "b.since"
	case "history":
		query = publicLiveHistorySQL
		at = "d.b"
		from = "d.a"
	default:
		return "", ErrLiveUnavailable
	}
	started := "(SELECT collection_started_at FROM live_dashboard_config WHERE id=1)"
	covered := started + "<=" + from
	available := started + "<" + at
	from = "greatest(" + from + "," + started + ")"
	users := `(SELECT count(DISTINCT user_id) FROM (SELECT user_id FROM live_dashboard_event WHERE observed_at>=` + from + ` AND observed_at<` + at + ` UNION SELECT user_id FROM infoflow_issue_source WHERE comment_id IS NOT NULL AND user_id IS NOT NULL AND created_at>=` + from + ` AND created_at<` + at + `) actors)`
	query = strings.ReplaceAll(query, "'active_users',NULL", "'active_users',CASE WHEN "+available+" THEN "+users+" ELSE NULL END")
	pageViews := `(SELECT count(*) FROM live_dashboard_event WHERE is_page_view AND observed_at>=` + from + ` AND observed_at<` + at + `)`
	query = strings.ReplaceAll(query, "'page_views',NULL", "'page_views',CASE WHEN "+available+" THEN "+pageViews+" ELSE NULL END")
	coverageExpr := available
	if section == "history" {
		coverageExpr = started + "<=(SELECT min(a) FROM days)"
		query = strings.Replace(query, "'date',d.day,", "'date',d.day,'partial',coalesce(("+available+") AND NOT ("+covered+"),false),", 1)
	}
	missing := "jsonb_build_array('active_users')"
	if section != "realtime" {
		missing = "jsonb_build_array('active_users','page_views')"
	}
	query = strings.ReplaceAll(query, missing, "CASE WHEN "+coverageExpr+" THEN '[]'::jsonb ELSE "+missing+" END")
	partialExpr := "coalesce((" + available + ") AND NOT (" + covered + "),false)"
	if section == "history" {
		partialExpr = started + ">(SELECT min(a) FROM days) AND " + started + "<(SELECT max(b) FROM days)"
	}
	query = strings.Replace(query, "'observed_at',", "'partial_metrics',CASE WHEN "+partialExpr+" THEN "+missing+" ELSE '[]'::jsonb END,'observed_at',", 1)
	return query, nil
}

var maskedLiveAccount = regexp.MustCompile(`^@[a-zA-Z0-9._-]{0,2}\*{3}[a-zA-Z0-9._-]?$`)

func checkLiveCounts(values ...*int64) error {
	for _, v := range values {
		if v != nil && (*v < 0 || *v > int64(1<<53-1)) {
			return ErrLiveUnavailable
		}
	}
	return nil
}
func sanitizeLive(section string, raw []byte) ([]byte, time.Time, error) {
	if len(raw) > 256*1024 {
		return nil, time.Time{}, ErrLiveUnavailable
	}
	var value any
	var meta *liveMeta
	switch section {
	case "realtime":
		var v liveRealtime
		if json.Unmarshal(raw, &v) != nil || math.Abs(v.ObservedAt.Sub(v.WindowStart).Seconds()-3600) > 0.001 || len(v.Events) > 20 {
			return nil, time.Time{}, ErrLiveUnavailable
		}
		if checkLiveCounts(v.ActiveUsers, v.WorkingAgents, v.WorkingIssues, v.WorkingChats, v.OnlineMachines) != nil {
			return nil, time.Time{}, ErrLiveUnavailable
		}
		if checkLiveCounts(v.Scale.Users, v.Scale.Issues, v.Scale.Agents, v.Scale.Machines) != nil {
			return nil, time.Time{}, ErrLiveUnavailable
		}
		if v.Events == nil {
			v.Events = []liveEvent{}
		}
		for i := range v.Events {
			e := &v.Events[i]
			if e.Kind != "completed" && e.Kind != "agent_started" && e.Kind != "created" && e.Kind != "machine_connected" {
				return nil, time.Time{}, ErrLiveUnavailable
			}
			if e.Count <= 0 || e.Count > 1<<53-1 || e.At.IsZero() || len(e.Members) > 5 {
				return nil, time.Time{}, ErrLiveUnavailable
			}
			if e.Members == nil {
				e.Members = []string{}
			}
			for _, member := range e.Members {
				if !maskedLiveAccount.MatchString(member) {
					return nil, time.Time{}, ErrLiveUnavailable
				}
			}
		}
		value = &v
		meta = &v.liveMeta
	case "recent":
		var v liveRecent
		if json.Unmarshal(raw, &v) != nil || math.Abs(v.ObservedAt.Sub(v.WindowStart).Seconds()-86400) > 0.001 {
			return nil, time.Time{}, ErrLiveUnavailable
		}
		if checkLiveCounts(v.ActiveUsers, v.PageViews, v.Messages, v.Created, v.Completed) != nil {
			return nil, time.Time{}, ErrLiveUnavailable
		}
		value = &v
		meta = &v.liveMeta
	case "history":
		var v liveHistory
		if json.Unmarshal(raw, &v) != nil || len(v.Days) != 7 {
			return nil, time.Time{}, ErrLiveUnavailable
		}
		zone, _ := time.LoadLocation("Asia/Shanghai")
		today := v.ObservedAt.In(zone)
		today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, zone)
		for i, d := range v.Days {
			if d.Date != today.AddDate(0, 0, i-7).Format("2006-01-02") || checkLiveCounts(d.ActiveUsers, d.PageViews, d.Messages, d.Created, d.Completed) != nil {
				return nil, time.Time{}, ErrLiveUnavailable
			}
		}
		if checkLiveCounts(v.Scale.Users, v.Scale.Issues, v.Scale.Agents, v.Scale.Machines, v.Capabilities.MultiAgent, v.Capabilities.Infoflow, v.Capabilities.Wakeups) != nil {
			return nil, time.Time{}, ErrLiveUnavailable
		}
		value = &v
		meta = &v.liveMeta
	default:
		return nil, time.Time{}, ErrLiveUnavailable
	}
	if meta.ObservedAt.IsZero() {
		return nil, time.Time{}, ErrLiveUnavailable
	}
	if meta.Unavailable == nil {
		meta.Unavailable = []string{}
	}
	if meta.Partial == nil {
		meta.Partial = []string{}
	}
	for _, key := range append(append([]string{}, meta.Unavailable...), meta.Partial...) {
		if key != "active_users" && key != "page_views" {
			return nil, time.Time{}, ErrLiveUnavailable
		}
	}
	clean, err := json.Marshal(value)
	return clean, meta.ObservedAt, err
}
