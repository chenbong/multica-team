#!/usr/bin/env python3
"""Guard source/SQLc cleanup composition and resource panel UI wiring."""
import json
import os
from pathlib import Path
import runpy
import shutil
import subprocess
import unittest
import uuid

ROOT = Path(__file__).resolve().parents[1]


class ResourceWebPanels(unittest.TestCase):
    def test_cleanup_uses_the_same_tenant_checked_issue_target(self):
        rewrite = runpy.run_path(str(ROOT / "overlays/resource-web-panels-sql.py"))["rewrite"]
        for kind, filename in (("issue", "issue"), ("workspace", "workspace"), ("workspace_issue_roots", "workspace_delete")):
            for relative in (f"server/pkg/db/queries/{filename}.sql", f"server/pkg/db/generated/{filename}.sql.go"):
                source = (ROOT / "multica" / relative).read_text()
                changed = rewrite(source, kind)
                self.assertEqual(changed.count("cleared_resource_web_panels AS ("), 1)
                if kind == "issue":
                    self.assertIn("WHERE issue_id IN (SELECT target.id FROM target)", changed)
                    self.assertIn("issue.workspace_id = $2", changed)
                else:
                    self.assertIn("issue_id IN (SELECT id FROM issue WHERE workspace_id = $1)", changed)
                if kind == "workspace_issue_roots":
                    start = changed.index("-- name: DeleteWorkspaceIssueRoots :exec")
                    end = changed.index("-- name: DeleteWorkspace", start + 1)
                    statement = changed[start:end]
                    self.assertTrue(statement.startswith("-- name: DeleteWorkspaceIssueRoots :exec\nWITH cleared_resource_web_panels AS ("))
                    self.assertIn("DELETE FROM issue WHERE issue.workspace_id = $1", statement)

    def test_cleanup_anchor_drift_fails_closed(self):
        rewrite = runpy.run_path(str(ROOT / "overlays/resource-web-panels-sql.py"))["rewrite"]
        for kind in ("issue", "workspace", "workspace_issue_roots"):
            with self.assertRaises(SystemExit):
                rewrite("changed query", kind)
        roots = (ROOT / "multica/server/pkg/db/queries/workspace_delete.sql").read_text()
        with self.assertRaises(SystemExit):
            rewrite(roots.replace("WITH deleted_wakeup_receipts AS (", "WITH changed_anchor AS ("), "workspace_issue_roots")

    @unittest.skipUnless(os.environ.get("DATABASE_URL") and shutil.which("psql"), "isolated PostgreSQL fixture required")
    def test_orphan_audit_reports_only_missing_owners_without_mutation(self):
        ids = [str(uuid.uuid4()) for _ in range(8)]
        workspace, issue, missing_workspace, missing_issue, valid_workspace_panel, valid_issue_panel, orphan_workspace_panel, orphan_issue_panel = ids
        setup = f"""
CREATE TEMP TABLE workspace (id uuid);
CREATE TEMP TABLE issue (id uuid);
CREATE TEMP TABLE resource_web_panel (id uuid, workspace_id uuid, issue_id uuid, created_at timestamptz DEFAULT now(), name text, url text);
INSERT INTO workspace VALUES ('{workspace}');
INSERT INTO issue VALUES ('{issue}');
INSERT INTO resource_web_panel (id,workspace_id,issue_id,name,url) VALUES
 ('{valid_workspace_panel}','{workspace}',NULL,'fixture-name','https://example.test/?token=fixture-secret'),
 ('{valid_issue_panel}',NULL,'{issue}','fixture-name','https://example.test/?token=fixture-secret'),
 ('{orphan_workspace_panel}','{missing_workspace}',NULL,'fixture-name','https://example.test/?token=fixture-secret'),
 ('{orphan_issue_panel}',NULL,'{missing_issue}','fixture-name','https://example.test/?token=fixture-secret');
"""
        audit = (ROOT / "scripts/resource-web-panel-orphans.sql").read_text()
        result = subprocess.run(
            ["psql", "-X", "-Atq", os.environ["DATABASE_URL"], "-v", "ON_ERROR_STOP=1"],
            input=setup + audit + "\nSELECT 'remaining',count(*) FROM resource_web_panel;\n",
            text=True, capture_output=True, timeout=20,
        )
        self.assertEqual(result.returncode, 0, "temporary PostgreSQL audit fixture failed")
        lines = result.stdout.splitlines()
        self.assertEqual(len(lines), 3)
        self.assertTrue(any(line.startswith(orphan_workspace_panel + "|workspace|" + missing_workspace + "|missing_workspace|") for line in lines))
        self.assertTrue(any(line.startswith(orphan_issue_panel + "|issue|" + missing_issue + "|missing_issue|") for line in lines))
        self.assertIn("remaining|4", lines)
        self.assertNotIn("fixture-secret", result.stdout)
        self.assertNotIn("fixture-name", result.stdout)

    def test_indexes_are_concurrent_and_resources_have_no_foreign_keys(self):
        table = (ROOT / "migrations/523_resource_web_panel.up.sql").read_text()
        self.assertNotIn("REFERENCES", table)
        self.assertIn("(workspace_id IS NULL) <> (issue_id IS NULL)", table)
        for number, name in ((524, "identity"), (525, "workspace_url"), (526, "issue_url")):
            source = (ROOT / f"migrations/{number}_resource_web_panel_{name}.up.sql").read_text()
            self.assertEqual(source.count(";"), 1)
            self.assertIn("CREATE UNIQUE INDEX CONCURRENTLY", source)

    def test_locales_and_non_invasive_patch_plan(self):
        labels = json.loads((ROOT / "overlays/resource-web-panels-labels.json").read_text())
        expected = set(labels["en"])
        self.assertEqual(set(labels), {"en", "zh-Hans", "ja", "ko", "fr"})
        for values in labels.values():
            self.assertEqual(set(values), expected)
        changes = {}
        runpy.run_path(str(ROOT / "overlays/resource-web-panels-web-patch.py"))["extend"](changes)
        self.assertIn("packages/core/platform/storage-cleanup.ts", changes)
        self.assertIn("packages/views/issues/components/issue-detail.tsx", changes)
        self.assertIn("packages/views/layout/dashboard-layout.tsx", changes)
        self.assertFalse(any(path.startswith("apps/mobile/") for path in changes))

    def test_optional_names_keep_url_limits_and_use_additive_migration(self):
        labels = json.loads((ROOT / "overlays/resource-web-panels-labels.json").read_text())
        self.assertEqual(labels["zh-Hans"]["url"], "链接")
        self.assertEqual(labels["zh-Hans"]["name"], "名称")
        self.assertIn("可选", labels["zh-Hans"]["name_placeholder"])
        source = (ROOT / "overlays/web-direct/packages/views/web-panels/panel-list.tsx").read_text()
        self.assertLess(source.index('htmlFor={`${id}-url`}'), source.index('htmlFor={`${id}-name`}'))
        self.assertIn('placeholder={t(($) => $.sidebar.web_panels.name_placeholder)}', source)
        migration = (ROOT / "migrations/528_resource_web_panel_url_name.up.sql").read_text()
        self.assertIn("name = url", migration)
        self.assertIn("octet_length(name) BETWEEN 1 AND 4096", migration)
        self.assertNotIn("UPDATE resource_web_panel", migration)

    def test_agent_entry_keeps_human_management_and_workspace_guards(self):
        router = (ROOT / "overlays/go/files/server/cmd/server/router.go").read_text()
        self.assertEqual(router.count('r.Post("/api/agent/issue-web-panels", h.AddCurrentIssueWebPanel)'), 1)
        for resource in ("Issue", "Workspace"):
            for verb, handler in (("Get", "List"), ("Post", "Create"), ("Patch", "Update"), ("Delete", "Delete")):
                route = "/web-panels" if verb in ("Get", "Post") else "/web-panels/{panelId}"
                suffix = "s" if handler == "List" else ""
                self.assertIn(f'r.With(handler.RequireHumanActor).{verb}("{route}", h.{handler}{resource}WebPanel{suffix})', router)

    def test_agent_skill_loader_is_checked_and_does_not_modify_upstream(self):
        generated = runpy.run_path(str(ROOT / "overlays/resource-web-panels-agent.py"))["generated"]
        result = generated(ROOT / "multica", ROOT / "overlays/go/files")
        self.assertEqual(len(result), 1)
        self.assertEqual(result[0][0], "server/internal/service/builtin_skills.go")
        self.assertIn("withResourceWebPanelSkill(loadSkillDirs", result[0][1])


if __name__ == "__main__":
    unittest.main(verbosity=2)
