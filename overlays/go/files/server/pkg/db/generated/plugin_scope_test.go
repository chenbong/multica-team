package db_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// A temporary table reproduces the overlay's partial unique index without
// depending on migrated application data or touching persistent skill rows.
func TestPluginSkillWorkspaceScope(t *testing.T) {
	databaseURL := os.Getenv("MULTICA_OVERLAY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set MULTICA_OVERLAY_TEST_DATABASE_URL to run the PostgreSQL scope regression")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	defer conn.Close(context.Background())
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	_, err = tx.Exec(ctx, `
CREATE TEMP TABLE skill (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id uuid, name text NOT NULL,
    description text NOT NULL DEFAULT '', content text NOT NULL DEFAULT '',
    config jsonb NOT NULL DEFAULT '{}', created_by uuid,
    created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
    plugin_installation_id uuid, scope text NOT NULL DEFAULT 'workspace', owner_user_id uuid
) ON COMMIT DROP;
CREATE UNIQUE INDEX skill_test_workspace_name ON skill(workspace_id, name) WHERE scope = 'workspace';
CREATE UNIQUE INDEX skill_test_personal_name ON skill(owner_user_id, name) WHERE scope = 'personal';
CREATE UNIQUE INDEX skill_test_team_name ON skill(name) WHERE scope = 'team';
SET LOCAL search_path = pg_temp;
`)
	if err != nil {
		t.Fatalf("create scoped skill fixture: %v", err)
	}
	uuid := func(value string) pgtype.UUID {
		var id pgtype.UUID
		if err := id.Scan(value); err != nil {
			t.Fatal(err)
		}
		return id
	}
	workspaceID := uuid("00000000-0000-4000-8000-000000000001")
	ownerID := uuid("00000000-0000-4000-8000-000000000002")
	installationID := uuid("00000000-0000-4000-8000-000000000003")
	otherInstallationID := uuid("00000000-0000-4000-8000-000000000004")
	queries := db.New(tx)
	seed := func(name, scope, content string, installation pgtype.UUID) db.Skill {
		t.Helper()
		skillWorkspaceID, skillOwnerID := workspaceID, pgtype.UUID{}
		if scope == "team" {
			skillWorkspaceID = pgtype.UUID{}
		}
		if scope == "personal" {
			skillOwnerID = ownerID
		}
		var id pgtype.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO skill
            (workspace_id, name, scope, content, owner_user_id, plugin_installation_id)
            VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
			skillWorkspaceID, name, scope, content, skillOwnerID, installation).Scan(&id); err != nil {
			t.Fatal(err)
		}
		skill, err := queries.GetSkill(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return skill
	}
	personal := seed("shared", "personal", "personal content", pgtype.UUID{})
	team := seed("shared", "team", "team content", pgtype.UUID{})
	params := db.UpsertPluginSkillParams{
		WorkspaceID: workspaceID, Name: "shared", Description: "plugin skill",
		Content: "plugin v1", CreatedBy: ownerID, PluginInstallationID: installationID,
	}
	installed, err := queries.UpsertPluginSkill(ctx, params)
	if err != nil {
		t.Fatalf("install alongside same-name personal/team skills: %v", err)
	}
	if installed.Scope != "workspace" || installed.OwnerUserID.Valid || installed.PluginInstallationID != installationID {
		t.Fatalf("unexpected installed skill ownership: %+v", installed)
	}
	params.Content = "plugin v2"
	upgraded, err := queries.UpsertPluginSkill(ctx, params)
	if err != nil || upgraded.ID != installed.ID || upgraded.Content != params.Content {
		t.Fatalf("upgrade must update the same workspace skill: %+v, %v", upgraded, err)
	}
	for _, original := range []db.Skill{personal, team} {
		preserved, err := queries.GetSkill(ctx, original.ID)
		if err != nil || preserved.Content != original.Content || preserved.PluginInstallationID.Valid {
			t.Fatalf("plugin changed %s skill: %+v, %v", original.Scope, preserved, err)
		}
	}
	workspaceSkill, err := queries.GetSkillByScopeAndName(ctx, db.GetSkillByScopeAndNameParams{
		WorkspaceID: workspaceID, Name: params.Name, Scope: "workspace",
	})
	if err != nil || workspaceSkill.ID != installed.ID {
		t.Fatalf("workspace lookup selected a different scope: %+v, %v", workspaceSkill, err)
	}
	seed("personal-only", "personal", "private", pgtype.UUID{})
	if _, err := queries.GetSkillByScopeAndName(ctx, db.GetSkillByScopeAndNameParams{
		WorkspaceID: workspaceID, Name: "personal-only", Scope: "workspace",
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("workspace lookup must ignore personal-only names: %v", err)
	}
	for _, owner := range []struct {
		name         string
		installation pgtype.UUID
	}{{"human-owned", pgtype.UUID{}}, {"other-plugin", otherInstallationID}} {
		original := seed(owner.name, "workspace", "original content", owner.installation)
		attempt := params
		attempt.Name = owner.name
		if _, err := queries.UpsertPluginSkill(ctx, attempt); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("must refuse to replace %s: %v", owner.name, err)
		}
		preserved, err := queries.GetSkill(ctx, original.ID)
		if err != nil || preserved.Content != original.Content || preserved.PluginInstallationID != original.PluginInstallationID {
			t.Fatalf("overwrote %s: %+v, %v", owner.name, preserved, err)
		}
	}
	params.WorkspaceID = uuid("00000000-0000-4000-8000-000000000005")
	otherWorkspace, err := queries.UpsertPluginSkill(ctx, params)
	if err != nil || otherWorkspace.ID == installed.ID || otherWorkspace.WorkspaceID != params.WorkspaceID {
		t.Fatalf("same-name install in another workspace must be independent: %+v, %v", otherWorkspace, err)
	}
}
