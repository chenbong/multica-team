package handler

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Migrations are deliberately tested on a SECOND empty isolated database,
// never against the fixtures used by handler/router tests or a running API.
func TestInfoFlowGroupMigrationsRoundTrip(t *testing.T) {
	if os.Getenv("MULTICA_INFOFLOW_MIGRATION_TEST") != "1" {
		t.Skip("opt-in isolated migration database")
	}
	cfg, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil || cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "infoflow_binding_test" {
		t.Fatal("requires isolated55439/infoflow_binding_test configuration")
	}
	for _, f := range cfg.ConnConfig.Fallbacks {
		if f.Host != "127.0.0.1" || f.Port != 55439 {
			t.Fatal("unsafe fallback")
		}
	}
	cfg.ConnConfig.Database = "infoflow_binding_migrations_test"
	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var n int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM infoflow_issue_binding`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("migration DB must have empty relay tables: %d %v", n, err)
	}
	names := append([]string{"488_infoflow_group_coordination", "489_infoflow_group_scope", "490_infoflow_group_global_scope"}, infoFlowMultiMigrations...)
	run := func(name, direction string) error {
		data, e := os.ReadFile(filepath.Join("..", "..", "..", "..", "migrations", name+"."+direction+".sql"))
		if e != nil {
			return e
		}
		_, e = pool.Exec(ctx, string(data))
		return e
	}
	down := func() {
		t.Helper()
		for i := len(names) - 1; i >= 0; i-- {
			if e := run(names[i], "down"); e != nil {
				t.Fatal(e)
			}
		}
	}
	up := func() {
		t.Helper()
		for _, name := range names {
			if e := run(name, "up"); e != nil {
				t.Fatal(e)
			}
		}
	}
	down()
	// Older robot-scoped rows could claim the same group twice. Refuse upgrade
	// before changing data, including conflicts spread over different workspaces.
	ids := []string{uuid.NewString(), uuid.NewString()}
	defer pool.Exec(ctx, `DELETE FROM infoflow_issue_binding WHERE id=ANY($1::uuid[])`, ids)
	for _, id := range ids {
		_, err = pool.Exec(ctx, `INSERT INTO infoflow_issue_binding(id,robot_id,workspace_id,agent_id,conversation_kind,conversation_id,issue_id,generation) VALUES($1,$6,$2,$3,'group','9223372036854775805',$4,$5)`, id, uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), "migration-"+id)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = run(names[0], "up"); err == nil {
		t.Fatal("ambiguous legacy rows silently upgraded")
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM infoflow_issue_binding WHERE id=ANY($1::uuid[])`, ids).Scan(&n); err != nil || n != 2 {
		t.Fatal("migration deleted ambiguous data")
	}
	if _, err = pool.Exec(ctx, `DELETE FROM infoflow_issue_binding WHERE id=ANY($1::uuid[])`, ids); err != nil {
		t.Fatal(err)
	}
	up()
	down()
	up()
	up()
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM pg_index WHERE NOT indisvalid AND indrelid IN ('infoflow_issue_binding'::regclass,'infoflow_issue_source'::regclass,'infoflow_issue_delivery'::regclass)`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("invalid indexes: %d %v", n, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM pg_constraint WHERE contype='f' AND conrelid='infoflow_issue_binding'::regclass`).Scan(&n); err != nil || n != 0 {
		t.Fatal("unexpected foreign keys")
	}
}

var infoFlowMultiMigrations = []string{"491_infoflow_one_group_preflight", "492_infoflow_issue_group_unique", "493_infoflow_private_scope", "494_infoflow_remove_robot_group_scope", "495_infoflow_remove_workspace_group_scope", "496_infoflow_remove_global_group_scope", "497_infoflow_multitask_rollback_guard"}

func TestInfoFlowWebGroupMigrationsRoundTrip(t *testing.T) {
	if os.Getenv("MULTICA_INFOFLOW_MIGRATION_TEST") != "1" {
		t.Skip("opt-in isolated migration database")
	}
	cfg, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil || cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "infoflow_binding_test" {
		t.Fatal("requires isolated55439/infoflow_binding_test")
	}
	for _, fallback := range cfg.ConnConfig.Fallbacks {
		if fallback.Host != "127.0.0.1" || fallback.Port != 55439 {
			t.Fatal("unsafe fallback")
		}
	}
	cfg.ConnConfig.Database = "infoflow_binding_migrations_test"
	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	names := []string{"498_infoflow_web_group_binding", "499_infoflow_web_group_issue", "500_infoflow_group_observation", "501_infoflow_web_group_poll", "502_infoflow_web_group_id", "503_infoflow_web_group_rollback_guard"}
	run := func(name, direction string) error {
		data, e := os.ReadFile(filepath.Join("..", "..", "..", "..", "migrations", name+"."+direction+".sql"))
		if e != nil {
			return e
		}
		_, e = pool.Exec(ctx, string(data))
		return e
	}
	for _, name := range names {
		if err = run(name, "up"); err != nil {
			t.Fatal(err)
		}
	}
	id, issue, ws, actor := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	defer pool.Exec(ctx, `DELETE FROM infoflow_web_group_request WHERE id=$1`, id)
	_, err = pool.Exec(ctx, `INSERT INTO infoflow_web_group_request(id,issue_id,workspace_id,group_id,requester_id,generation,topology,state,expires_at) VALUES($1,$2,$3,'543210',$4,$5,'{}','queued',clock_timestamp()+interval '10 minutes')`, id, issue, ws, actor, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = run(names[len(names)-1], "down"); err == nil {
		t.Fatal("rollback dropped pending request")
	}
	var kept int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM pg_index WHERE indisvalid AND indrelid IN ('infoflow_web_group_request'::regclass,'infoflow_group_observation'::regclass)`).Scan(&kept); err != nil || kept != 4 {
		t.Fatal("refused rollback removed admission indexes", kept, err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO infoflow_web_group_request(id,issue_id,workspace_id,group_id,requester_id,generation,topology,state,expires_at) VALUES($1,$2,$3,'543211',$4,$5,'{}','queued',clock_timestamp()+interval '10 minutes')`, uuid.NewString(), issue, ws, actor, uuid.NewString()); err == nil {
		t.Fatal("duplicate issue request admitted")
	}
	_, err = pool.Exec(ctx, `DELETE FROM infoflow_web_group_request WHERE id=$1`, id)
	if err != nil {
		t.Fatal(err)
	}
	for i := len(names) - 1; i >= 0; i-- {
		if err = run(names[i], "down"); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		for _, name := range names {
			if err = run(name, "up"); err != nil {
				t.Fatal(err)
			}
		}
	}
	var invalid, fks int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM pg_index WHERE NOT indisvalid AND indrelid IN('infoflow_web_group_request'::regclass,'infoflow_group_observation'::regclass)`).Scan(&invalid); err != nil || invalid != 0 {
		t.Fatal("invalid web group index", invalid, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM pg_constraint WHERE contype='f' AND conrelid IN('infoflow_web_group_request'::regclass,'infoflow_group_observation'::regclass)`).Scan(&fks); err != nil || fks != 0 {
		t.Fatal("unexpected web binding FK", fks, err)
	}
}

func TestInfoFlowMultiGroupMigrationsAndDeploymentReplay(t *testing.T) {
	if os.Getenv("MULTICA_INFOFLOW_MIGRATION_TEST") != "1" {
		t.Skip("opt-in isolated migration database")
	}
	cfg, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil || cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "infoflow_binding_test" {
		t.Fatal("requires isolated55439/infoflow_binding_test")
	}
	for _, f := range cfg.ConnConfig.Fallbacks {
		if f.Host != "127.0.0.1" || f.Port != 55439 {
			t.Fatal("unsafe fallback")
		}
	}
	cfg.ConnConfig.Database = "infoflow_binding_migrations_test"
	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var n int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM infoflow_issue_binding`).Scan(&n); err != nil || n != 0 {
		t.Fatal("requires empty migration fixture", n, err)
	}
	root := filepath.Join("..", "..", "..", "..")
	run := func(name, direction string) error {
		data, e := os.ReadFile(filepath.Join(root, "migrations", name+"."+direction+".sql"))
		if e != nil {
			return e
		}
		_, e = pool.Exec(ctx, string(data))
		return e
	}
	up := func() {
		t.Helper()
		for _, name := range infoFlowMultiMigrations {
			if e := run(name, "up"); e != nil {
				t.Fatal(e)
			}
		}
	}
	down := func() {
		t.Helper()
		for i := len(infoFlowMultiMigrations) - 1; i >= 0; i-- {
			if e := run(infoFlowMultiMigrations[i], "down"); e != nil {
				t.Fatal(e)
			}
		}
	}
	down()
	ids := []string{uuid.NewString(), uuid.NewString()}
	ws, agent, issue := uuid.NewString(), uuid.NewString(), uuid.NewString()
	defer pool.Exec(ctx, `DELETE FROM infoflow_issue_binding WHERE workspace_id=$1`, ws)
	insert := func(id, kind, group, task string) error {
		_, e := pool.Exec(ctx, `INSERT INTO infoflow_issue_binding(id,robot_id,workspace_id,agent_id,conversation_kind,conversation_id,issue_id,generation) VALUES($1,'migration-multi',$2,$3,$4,$5,$6,$7)`, id, ws, agent, kind, group, task, uuid.NewString())
		return e
	}
	for i, id := range ids {
		if err = insert(id, "group", strconv.Itoa(9100+i), issue); err != nil {
			t.Fatal(err)
		}
	}
	if err = run(infoFlowMultiMigrations[0], "up"); err == nil {
		t.Fatal("accepted old one-issue/two-group conflict")
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM infoflow_issue_binding WHERE workspace_id=$1`, ws).Scan(&n); err != nil || n != 2 {
		t.Fatal("preflight deleted old data")
	}
	if _, err = pool.Exec(ctx, `DELETE FROM infoflow_issue_binding WHERE workspace_id=$1`, ws); err != nil {
		t.Fatal(err)
	}
	up()
	up()
	for _, id := range ids {
		if err = insert(id, "group", "9200", uuid.NewString()); err != nil {
			t.Fatal(err)
		}
	}
	if err = run(infoFlowMultiMigrations[len(infoFlowMultiMigrations)-1], "down"); err == nil {
		t.Fatal("unsafe multi-task downgrade succeeded")
	}
	// Exercise the exact deployment replay entry point using only the relay
	// migration set, never the rest of the application's schema or services.
	dir := t.TempDir()
	entries, err := os.ReadDir(filepath.Join(root, "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if len(name) < 4 || !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		number, e := strconv.Atoi(name[:3])
		if e != nil || number < 477 || number > 497 {
			continue
		}
		data, e := os.ReadFile(filepath.Join(root, "migrations", name))
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, name), data, 0600); e != nil {
			t.Fatal(e)
		}
	}
	cmd := exec.Command("bash", filepath.Join(root, "deploy", "migrate-team.sh"))
	cmd.Env = append(os.Environ(), "DATABASE_URL="+cfg.ConnConfig.ConnString(), "MULTICA_TEAM_MIGRATIONS_DIR="+dir)
	if output, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("deployment replay: %v\n%s", e, output)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM infoflow_issue_binding WHERE workspace_id=$1`, ws).Scan(&n); err != nil || n != 2 {
		t.Fatal("deployment replay changed associations")
	}
	privateIssue := uuid.NewString()
	if err = insert(uuid.NewString(), "private", "migration-user", privateIssue); err != nil {
		t.Fatal(err)
	}
	if err = insert(uuid.NewString(), "private", "migration-user", privateIssue); err == nil {
		t.Fatal("private uniqueness lost")
	}
	var task string
	if err = pool.QueryRow(ctx, `SELECT issue_id::text FROM infoflow_issue_binding WHERE id=$1`, ids[0]).Scan(&task); err != nil {
		t.Fatal(err)
	}
	if err = insert(uuid.NewString(), "group", "9201", task); err == nil {
		t.Fatal("one issue acquired second group")
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM pg_index WHERE NOT indisvalid AND indrelid IN ('infoflow_issue_binding'::regclass,'infoflow_issue_source'::regclass,'infoflow_issue_delivery'::regclass)`).Scan(&n); err != nil || n != 0 {
		t.Fatal("invalid index left behind", n, err)
	}
	if _, err = pool.Exec(ctx, `DELETE FROM infoflow_issue_binding WHERE workspace_id=$1`, ws); err != nil {
		t.Fatal(err)
	}
	down()
	up()
	up()
}
