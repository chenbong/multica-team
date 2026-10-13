package handler

import (
	"context"
	"os"
	"path/filepath"
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
	names := []string{"488_infoflow_group_coordination", "489_infoflow_group_scope", "490_infoflow_group_global_scope"}
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
