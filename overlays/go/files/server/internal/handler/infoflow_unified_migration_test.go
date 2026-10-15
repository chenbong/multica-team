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

func TestInfoFlowUnifiedMigrationsAndSafeReplay(t *testing.T) {
	if os.Getenv("MULTICA_INFOFLOW_MIGRATION_TEST") != "1" {
		t.Skip("opt-in secondary migration database")
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
	root := filepath.Join("..", "..", "..", "..")
	entries, err := os.ReadDir(filepath.Join(root, "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		n, e := strconv.Atoi(name[:3])
		if e == nil && n >= 504 && n <= 512 {
			names = append(names, strings.TrimSuffix(name, ".up.sql"))
		}
	}
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
		for _, name := range names {
			if e := run(name, "up"); e != nil {
				t.Fatal(e)
			}
		}
	}
	down := func() {
		t.Helper()
		for i := len(names) - 1; i >= 0; i-- {
			if e := run(names[i], "down"); e != nil {
				t.Fatal(e)
			}
		}
	}
	up()
	down()
	up()
	up()
	ws, agent, task1, task2 := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	defer pool.Exec(ctx, `DELETE FROM infoflow_issue_binding WHERE workspace_id=$1`, ws)
	defer pool.Exec(ctx, `DELETE FROM infoflow_web_private_request WHERE workspace_id=$1`, ws)
	insert := func(task string) error {
		_, e := pool.Exec(ctx, `INSERT INTO infoflow_issue_binding(id,robot_id,workspace_id,agent_id,conversation_kind,conversation_id,issue_id,generation) VALUES($1,'migration-private',$2,$3,'private','migration-user',$4,$5)`, uuid.NewString(), ws, agent, task, uuid.NewString())
		return e
	}
	if err = insert(task1); err != nil {
		t.Fatal(err)
	}
	if err = insert(task2); err != nil {
		t.Fatal(err)
	}
	if err = insert(task1); err == nil {
		t.Fatal("duplicate issue allowed in same private scope")
	}
	if err = run(names[3], "down"); err == nil {
		t.Fatal("multi-private rollback lost histories")
	}
	if _, err = pool.Exec(ctx, `INSERT INTO infoflow_web_private_request(id,issue_id,workspace_id,requester_id,sender,agent_id,assignment_version,generation,binding_snapshot,state,expires_at) VALUES($1,$2,$3,$4,'migration-user',$5,0,$6,'[]','pending',clock_timestamp()+interval '1 minute')`, uuid.NewString(), task1, ws, uuid.NewString(), agent, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if err = run(names[len(names)-1], "down"); err == nil {
		t.Fatal("pending private request rollback allowed")
	}
	// Exact deployment helper replay must not recreate retired scope indexes.
	dir := t.TempDir()
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		n, e := strconv.Atoi(name[:3])
		if e != nil || n < 477 || n > 512 {
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
		t.Fatalf("unified replay: %v\n%s", e, output)
	}
	var n int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM infoflow_issue_binding WHERE workspace_id=$1`, ws).Scan(&n); err != nil || n != 2 {
		t.Fatal("replay changed binding data", n, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM pg_index WHERE NOT indisvalid AND indrelid IN('infoflow_issue_binding'::regclass,'infoflow_web_private_request'::regclass)`).Scan(&n); err != nil || n != 0 {
		t.Fatal("invalid indexes", n, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM pg_constraint WHERE contype='f' AND conrelid='infoflow_web_private_request'::regclass`).Scan(&n); err != nil || n != 0 {
		t.Fatal("unexpected private FK", n, err)
	}
	_, err = pool.Exec(ctx, `DELETE FROM infoflow_web_private_request WHERE workspace_id=$1`, ws)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `DELETE FROM infoflow_issue_binding WHERE workspace_id=$1`, ws)
	if err != nil {
		t.Fatal(err)
	}
	down()
	up()
}
