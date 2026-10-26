package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPublicLiveHourIncludesFinishedWorkAndExcludesStaleOrQueuedWork(t *testing.T) {
	pool := liveTestPool(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	addAgent := func(archived bool) string {
		id := uuid.NewString()
		var archivedAt any
		if archived {
			archivedAt = at.Add(-10 * time.Minute)
		}
		if _, e := pool.Exec(ctx, "INSERT INTO agent VALUES($1,$2)", id, archivedAt); e != nil {
			t.Fatal(e)
		}
		return id
	}
	addRuntime := func(daemon string, minutesAgo int) string {
		id := uuid.NewString()
		if _, e := pool.Exec(ctx, "INSERT INTO agent_runtime VALUES($1,$2,NULL,$3,'offline',$4)", id, daemon, at.Add(-24*time.Hour), at.Add(-time.Duration(minutesAgo)*time.Minute)); e != nil {
			t.Fatal(e)
		}
		return id
	}
	idValue := func(value string) any {
		if value == "" {
			return nil
		}
		return value
	}
	addTask := func(agent, runtime, issue, chat, status string, startedMinutes int, endedMinutes *int) {
		var end any
		if endedMinutes != nil {
			end = at.Add(-time.Duration(*endedMinutes) * time.Minute)
		}
		if _, e := pool.Exec(ctx, `INSERT INTO agent_task_queue VALUES($1,$2,$3,$4,$5,$6,$7,NULL)`, agent, idValue(issue), idValue(chat), idValue(runtime), status, at.Add(-time.Duration(startedMinutes)*time.Minute), end); e != nil {
			t.Fatal(e)
		}
	}
	minutes := func(n int) *int { return &n }
	shared := addRuntime("shared", 1)
	finished := addRuntime("finished", 180)
	stale := addRuntime("stale", 120)
	_ = addRuntime("shared", 30)
	_ = addRuntime("heartbeat-only", 55)
	_ = addRuntime("future-heartbeat", -5)
	issueA, issueB, cloudIssue, chatA, chatB := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	activeAgent := addAgent(false)
	addTask(activeAgent, shared, issueA, "", "running", 120, nil)
	addTask(addAgent(false), finished, issueB, chatA, "completed", 90, minutes(30))
	addTask(addAgent(true), shared, issueA, chatB, "completed", 40, minutes(20))
	addTask(activeAgent, shared, issueA, chatA, "failed", 60, minutes(59))
	addTask(addAgent(false), stale, uuid.NewString(), uuid.NewString(), "running", 180, nil)
	addTask(addAgent(false), stale, uuid.NewString(), uuid.NewString(), "completed", 120, minutes(60))
	addTask(addAgent(false), shared, uuid.NewString(), uuid.NewString(), "queued", 20, nil)
	addTask(addAgent(false), shared, uuid.NewString(), uuid.NewString(), "running", 0, nil)
	addTask(addAgent(false), "", cloudIssue, "", "running", 120, nil)
	user, boundaryUser := uuid.NewString(), uuid.NewString()
	for _, id := range []string{user, boundaryUser} {
		if _, e := pool.Exec(ctx, `INSERT INTO "user" VALUES($1,'fixture@example.test')`, id); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := pool.Exec(ctx, "UPDATE live_dashboard_config SET collection_started_at=$1", at.Add(-2*time.Hour)); e != nil {
		t.Fatal(e)
	}
	for _, event := range []struct {
		user string
		at   time.Time
	}{{user, at.Add(-time.Hour)}, {boundaryUser, at}, {boundaryUser, at.Add(-time.Hour - time.Microsecond)}} {
		if _, e := pool.Exec(ctx, "INSERT INTO live_dashboard_event VALUES($1,$2,$3,true)", uuid.NewString(), event.user, event.at); e != nil {
			t.Fatal(e)
		}
	}
	query, e := publicLiveSQL("realtime")
	if e != nil {
		t.Fatal(e)
	}
	query = strings.ReplaceAll(query, "current_timestamp", "'2026-10-22T12:00:00Z'::timestamptz")
	var raw []byte
	if e = pool.QueryRow(ctx, query).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	clean, _, e := sanitizeLive("realtime", raw)
	if e != nil {
		t.Fatal(e)
	}
	var out map[string]any
	if json.Unmarshal(clean, &out) != nil {
		t.Fatal("invalid projection")
	}
	for key, want := range map[string]float64{"active_users": 1, "working_agents": 4, "working_issues": 3, "working_chats": 2, "online_machines": 3} {
		if out[key] != want {
			t.Fatalf("%s got %v want %v", key, out[key], want)
		}
	}
	if liveInterval("realtime") != 10 || liveInterval("recent") != 10 || liveInterval("history") != 300 {
		t.Fatal("incorrect two-tier schedule")
	}
}

func TestPublicLiveHourDoesNotRestoreOldInstantaneousCache(t *testing.T) {
	pool := liveTestPool(t)
	ctx := context.Background()
	s := liveService(t, pool, nil)
	for _, key := range []string{"realtime", "recent"} {
		if e := s.Refresh(ctx, key); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := pool.Exec(ctx, "UPDATE live_dashboard_cache SET definition_version=1 WHERE section='realtime'"); e != nil {
		t.Fatal(e)
	}
	restored := liveService(t, pool, nil)
	restored.restore(ctx)
	now, _ := restored.Get("realtime")
	recent, _ := restored.Get("recent")
	if now.Data != nil || recent.Data == nil {
		t.Fatal("old instant values reused as one-hour observations")
	}
}
