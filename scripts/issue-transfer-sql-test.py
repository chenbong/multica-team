#!/usr/bin/env python3
"""Offline checks for paired query-source/SQLc transfer write fences."""
import runpy
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
GUARDS = runpy.run_path(str(ROOT / "overlays/issue-transfer-sql.py"))


class TransferSQL(unittest.TestCase):
    def test_both_query_and_generated_variants_are_guarded(self):
        for name, expected in (("agent", 5), ("wakeup", 1), ("subscriber", 2)):
            for relative in (f"server/pkg/db/queries/{name}.sql", f"server/pkg/db/generated/{name}.sql.go"):
                with self.subTest(file=relative):
                    source = ROOT / "overlays/go/files" / relative
                    if not source.exists():
                        source = ROOT / "multica" / relative
                    before = source.read_text()
                    after = GUARDS["rewrite"](before, name)
                    self.assertEqual(after.count("FOR SHARE") - before.count("FOR SHARE"), expected)
                    self.assertEqual(after.count("lock_task_owner_rows("), before.count("lock_task_owner_rows("))
                    with self.assertRaises(SystemExit):
                        GUARDS["rewrite"]("upstream changed", name)


if __name__ == "__main__":
    unittest.main(verbosity=2)
