package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestBuiltinSandboxNewMikaAndBuilder(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	cleanupMika(t)
	runtimeID := handlerTestRuntimeID(t)
	w := createMika(t, map[string]any{"runtime_id": runtimeID, "language": "en"})
	if w.Code != http.StatusCreated {
		t.Fatalf("create Mika: HTTP %d", w.Code)
	}
	mika := decodeAgent(t, w)
	w = httptest.NewRecorder()
	testHandler.CreateAgentBuilderSession(w, newRequest(http.MethodPost, "/api/agent-builder/sessions", map[string]any{"runtime_id": runtimeID}))
	if w.Code != http.StatusCreated {
		t.Fatalf("create builder: HTTP %d", w.Code)
	}
	var builder CreateAgentBuilderSessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &builder); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM chat_session WHERE id=$1`, builder.SessionID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id=$1`, builder.BuilderAgentID)
	})
	for _, id := range []string{mika.ID, builder.BuilderAgentID} {
		var env map[string]string
		var raw []byte
		if err := testPool.QueryRow(context.Background(), `SELECT custom_env FROM agent WHERE id=$1`, id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &env); err != nil || env["IS_SANDBOX"] != "1" || len(env) != 1 {
			t.Fatal("built-in environment must contain only its sandbox default")
		}
	}
	// The fix must not make hidden builder infrastructure accessible through
	// the generic public environment-management endpoint.
	w = httptest.NewRecorder()
	testHandler.GetAgentEnv(w, withURLParams(newRequest(http.MethodGet, "/api/agents/"+builder.BuilderAgentID+"/env", nil), "id", builder.BuilderAgentID))
	if w.Code != http.StatusNotFound {
		t.Fatalf("hidden builder environment unexpectedly exposed: HTTP %d", w.Code)
	}
}

func TestBuiltinSandboxMikaExistingConfigurationIsNotReset(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	cleanupMika(t)
	runtimeID := handlerTestRuntimeID(t)
	w := createMika(t, map[string]any{"runtime_id": runtimeID, "language": "en"})
	if w.Code != http.StatusCreated {
		t.Fatalf("create Mika: HTTP %d", w.Code)
	}
	mika := decodeAgent(t, w)
	if _, err := testPool.Exec(context.Background(), `UPDATE agent SET custom_env='{"IS_SANDBOX":"0","KEEP":"unchanged"}'::jsonb WHERE id=$1`, mika.ID); err != nil {
		t.Fatal(err)
	}
	w = createMika(t, map[string]any{"runtime_id": runtimeID, "language": "en"})
	if w.Code != http.StatusOK || decodeAgent(t, w).ID != mika.ID {
		t.Fatal("Mika re-entry must return its existing agent")
	}
	var matches bool
	if err := testPool.QueryRow(context.Background(), `SELECT custom_env='{"IS_SANDBOX":"0","KEEP":"unchanged"}'::jsonb FROM agent WHERE id=$1`, mika.ID).Scan(&matches); err != nil || !matches {
		t.Fatal("Mika re-entry changed an explicit environment configuration")
	}
}

func TestBuiltinSandboxMigrationPreservesExplicitValuesAndOtherAgents(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	schema := pgx.Identifier{"builtin_env_" + strings.ReplaceAll(uuid.NewString(), "-", "")}.Sanitize()
	if _, err = tx.Exec(ctx, `CREATE SCHEMA `+schema+`; SET LOCAL search_path TO `+schema+`; CREATE TABLE agent(id text PRIMARY KEY,kind text,system_key text,custom_env jsonb,updated_at timestamptz)`); err != nil {
		t.Fatal(err)
	}
	rows := []struct{ id, kind, key, before, after string }{
		{"mika", "user", "mika", `{"KEEP":"value"}`, `{"KEEP":"value","IS_SANDBOX":"1"}`},
		{"builder", "system", "agent_builder:fixture", `{}`, `{"IS_SANDBOX":"1"}`},
		{"sql-null", "system", "agent_builder:null", "", `{"IS_SANDBOX":"1"}`},
		{"json-null", "user", "mika", `null`, `{"IS_SANDBOX":"1"}`},
		{"off", "user", "mika", `{"IS_SANDBOX":"0","KEEP":"value"}`, `{"IS_SANDBOX":"0","KEEP":"value"}`},
		{"empty", "system", "agent_builder:empty", `{"IS_SANDBOX":""}`, `{"IS_SANDBOX":""}`},
		{"false", "system", "agent_builder:false", `{"IS_SANDBOX":false}`, `{"IS_SANDBOX":false}`},
		{"explicit-null", "user", "mika", `{"IS_SANDBOX":null}`, `{"IS_SANDBOX":null}`},
		{"normal", "user", "", `{"KEEP":"value"}`, `{"KEEP":"value"}`},
		{"wrong-kind", "user", "agent_builder:fixture", `{}`, `{}`},
		{"other-system", "system", "other:fixture", `{}`, `{}`},
		{"invalid-map", "user", "mika", `["unchanged"]`, `["unchanged"]`},
	}
	for _, row := range rows {
		var raw any = row.before
		if row.before == "" {
			raw = nil
		}
		if _, err = tx.Exec(ctx, `INSERT INTO agent VALUES($1,$2,$3,$4::jsonb,'2000-01-01'::timestamptz)`, row.id, row.kind, row.key, raw); err != nil {
			t.Fatal(err)
		}
	}
	run := func(direction string) {
		t.Helper()
		sql, e := os.ReadFile(filepath.Join("..", "..", "..", "..", "migrations", "522_builtin_agent_sandbox_env."+direction+".sql"))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(ctx, string(sql)); e != nil {
			t.Fatal(e)
		}
	}
	run("up")
	for _, row := range rows {
		t.Run(row.id, func(t *testing.T) {
			var matches, unchangedTimestamp bool
			if err = tx.QueryRow(ctx, `SELECT custom_env=$2::jsonb,updated_at='2000-01-01'::timestamptz FROM agent WHERE id=$1`, row.id, row.after).Scan(&matches, &unchangedTimestamp); err != nil || !matches {
				t.Fatal("backfill did not preserve the expected environment")
			}
			if row.before == row.after && !unchangedTimestamp {
				t.Fatal("untargeted row was rewritten")
			}
		})
	}
	// Replaying the data-only migration is idempotent, and rollback must not
	// erase either the filled value or an owner's later explicit setting.
	if _, err = tx.Exec(ctx, `UPDATE agent SET updated_at='2000-01-01'::timestamptz`); err != nil {
		t.Fatal(err)
	}
	run("up")
	run("down")
	var changed int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM agent WHERE updated_at<>'2000-01-01'::timestamptz`).Scan(&changed); err != nil || changed != 0 {
		t.Fatal("replay or rollback rewrote existing rows")
	}
	for _, row := range rows {
		var matches bool
		if err = tx.QueryRow(ctx, `SELECT custom_env=$2::jsonb FROM agent WHERE id=$1`, row.id, row.after).Scan(&matches); err != nil || !matches {
			t.Fatal("rollback erased a preserved or backfilled environment")
		}
	}
}
