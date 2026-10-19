#!/usr/bin/env python3
"""Guard source/SQLc cleanup composition and resource panel UI wiring."""
import json
from pathlib import Path
import runpy
import unittest

ROOT = Path(__file__).resolve().parents[1]


class ResourceWebPanels(unittest.TestCase):
    def test_cleanup_uses_the_same_tenant_checked_issue_target(self):
        rewrite = runpy.run_path(str(ROOT / "overlays/resource-web-panels-sql.py"))["rewrite"]
        for kind in ("issue", "workspace"):
            for relative in (f"server/pkg/db/queries/{kind}.sql", f"server/pkg/db/generated/{kind}.sql.go"):
                source = (ROOT / "multica" / relative).read_text()
                changed = rewrite(source, kind)
                self.assertEqual(changed.count("cleared_resource_web_panels AS ("), 1)
                if kind == "issue":
                    self.assertIn("WHERE issue_id IN (SELECT target.id FROM target)", changed)
                    self.assertIn("issue.workspace_id = $2", changed)
                else:
                    self.assertIn("issue_id IN (SELECT id FROM issue WHERE workspace_id = $1)", changed)

    def test_cleanup_anchor_drift_fails_closed(self):
        rewrite = runpy.run_path(str(ROOT / "overlays/resource-web-panels-sql.py"))["rewrite"]
        with self.assertRaises(SystemExit):
            rewrite("changed query", "issue")

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
