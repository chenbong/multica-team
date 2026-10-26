package service

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
)

func TestPublicLivePartialCountsCoverageAndLegacySnapshots(t *testing.T) {
	p := liveTestPool(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 23, 6, 0, 0, 0, time.UTC)
	start := time.Date(2026, 10, 22, 12, 52, 0, 0, time.UTC)
	user, other := uuid.NewString(), uuid.NewString()
	if _, e := p.Exec(ctx, `UPDATE live_dashboard_config SET collection_started_at=$1`, start); e != nil {
		t.Fatal(e)
	}
	for _, e := range []struct {
		u  string
		at time.Time
	}{{user, start}, {user, start.Add(time.Hour)}, {other, start.Add(-time.Hour)}, {other, at}} {
		if _, err := p.Exec(ctx, `INSERT INTO live_dashboard_event VALUES($1,$2,$3,true)`, uuid.NewString(), e.u, e.at); err != nil {
			t.Fatal(err)
		}
	}
	load := func(section string, clock time.Time) []byte {
		q, e := publicLiveSQL(section)
		if e != nil {
			t.Fatal(e)
		}
		q = strings.ReplaceAll(q, "current_timestamp", "'"+clock.Format(time.RFC3339Nano)+"'::timestamptz")
		var b []byte
		if e = p.QueryRow(ctx, q).Scan(&b); e != nil {
			t.Fatal(e)
		}
		clean, _, e := sanitizeLive(section, b)
		if e != nil {
			t.Fatal(e)
		}
		return clean
	}
	var recent liveRecent
	if e := json.Unmarshal(load("recent", at), &recent); e != nil {
		t.Fatal(e)
	}
	if recent.ActiveUsers == nil || *recent.ActiveUsers != 1 || *recent.PageViews != 2 || len(recent.Partial) != 2 || len(recent.Unavailable) != 0 {
		t.Fatalf("partial count or boundaries: %+v", recent)
	}
	if e := json.Unmarshal(load("recent", start.Add(24*time.Hour)), &recent); e != nil {
		t.Fatal(e)
	}
	if len(recent.Partial) != 0 {
		t.Fatal("full coverage must clear collecting badge")
	}
	var history liveHistory
	raw := load("history", at)
	if e := json.Unmarshal(raw, &history); e != nil {
		t.Fatal(e)
	}
	for i, d := range history.Days {
		if i < 6 && (d.ActiveUsers != nil || d.PageViews != nil || d.Partial) {
			t.Fatal("uncaptured day fabricated", d)
		}
		if i == 6 && (d.ActiveUsers == nil || *d.ActiveUsers != 1 || *d.PageViews != 2 || !d.Partial) {
			t.Fatal("partial Beijing day missing", d)
		}
	}
	if _, e := p.Exec(ctx, `INSERT INTO live_dashboard_snapshot VALUES('2026-10-22',1,'{"date":"2026-10-22","active_users":null,"page_views":null,"messages":321,"created_issues":7,"completed_issues":2}',now())`); e != nil {
		t.Fatal(e)
	}
	s := liveService(t, p, nil)
	saved, e := s.persist(ctx, "history", raw, at)
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(saved, &history); e != nil {
		t.Fatal(e)
	}
	d := history.Days[6]
	if *d.Messages != 321 || *d.Created != 7 || *d.Completed != 2 || *d.PageViews != 2 || !d.Partial {
		t.Fatal("legacy business snapshot lost or telemetry not recovered", d)
	}
	var n int
	if e = p.QueryRow(ctx, `SELECT count(*) FROM live_dashboard_snapshot WHERE day='2026-10-22'`).Scan(&n); e != nil || n != 2 {
		t.Fatal("must preserve original snapshot", n, e)
	}
	// Repeated refreshes do not mutate the new frozen archive.
	if _, e = p.Exec(ctx, `INSERT INTO live_dashboard_event VALUES($1,$2,$3,true)`, uuid.NewString(), user, start.Add(2*time.Hour)); e != nil {
		t.Fatal(e)
	}
	saved, e = s.persist(ctx, "history", load("history", at), at)
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(saved, &history); e != nil || *history.Days[6].PageViews != 2 {
		t.Fatal("snapshot changed", e)
	}
	if _, e = p.Exec(ctx, `UPDATE live_dashboard_config SET collection_started_at=NULL`); e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(load("recent", at), &recent); e != nil || recent.ActiveUsers != nil || recent.PageViews != nil || len(recent.Partial) != 0 {
		t.Fatal("unstarted collector fabricated counts", e)
	}
}
