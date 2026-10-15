#!/usr/bin/env python3
"""Offline checks for scoped defaults and composition with prior SQL guards."""
import runpy
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
ENV = runpy.run_path(str(ROOT / "overlays/builtin-agent-env-sql.py"))
GUARDS = runpy.run_path(str(ROOT / "overlays/issue-transfer-sql.py"))


class BuiltinAgentEnvironment(unittest.TestCase):
    def test_paired_sources_and_generated_defaults(self):
        for relative in ("server/pkg/db/queries/agent.sql", "server/pkg/db/generated/agent.sql.go"):
            with self.subTest(relative=relative):
                before = (ROOT / "multica" / relative).read_text()
                after = ENV["rewrite"](before)
                self.assertEqual(after.count('"IS_SANDBOX":"1"'), 2)
                self.assertEqual(after.count("'[]'::jsonb"), before.count("'[]'::jsonb"))
                self.assertIn("= 'mika' THEN", after)
                with self.assertRaises(SystemExit):
                    ENV["rewrite"](after)

    def test_existing_transfer_guards_are_retained(self):
        source = list(GUARDS["generated"](ROOT / "multica", ROOT / "overlays/go/files"))
        result = dict(ENV["extend"](ROOT / "multica", source))
        relative = "server/pkg/db/generated/agent.sql.go"
        self.assertEqual(result[relative].count("FOR SHARE"), dict(source)[relative].count("FOR SHARE"))
        self.assertEqual(result[relative].count('"IS_SANDBOX":"1"'), 2)
        for name, text in source:
            if name != relative:
                self.assertEqual(result[name], text)

    def test_unknown_or_duplicate_anchors_fail_closed(self):
        with self.assertRaises(SystemExit):
            ENV["rewrite"]("upstream changed")
        with self.assertRaises(SystemExit):
            ENV["extend"](ROOT / "multica", [])
        before = (ROOT / "multica/server/pkg/db/queries/agent.sql").read_text()
        with self.assertRaises(SystemExit):
            ENV["rewrite"](before + before)


if __name__ == "__main__":
    unittest.main(verbosity=2)
