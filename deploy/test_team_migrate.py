"""Migration selection tests with a fake psql; no real database or service."""
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).resolve().with_name("migrate-team.sh")
RETIRED = {"479_infoflow_binding_scope.up.sql", "488_infoflow_group_coordination.up.sql",
           "489_infoflow_group_scope.up.sql", "490_infoflow_group_global_scope.up.sql"}
CURRENT = {"477_infoflow_issue_relay.up.sql", "492_infoflow_issue_group_unique.up.sql",
           "493_infoflow_private_scope.up.sql", "494_infoflow_remove_robot_group_scope.up.sql",
           "495_infoflow_remove_workspace_group_scope.up.sql", "496_infoflow_remove_global_group_scope.up.sql",
           "497_infoflow_multitask_rollback_guard.up.sql"}


class TeamMigrationReplayTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="team-migrations-test-")
        self.addCleanup(self.temp.cleanup)
        root = Path(self.temp.name)
        self.migrations = root / "migrations with spaces"
        self.migrations.mkdir()
        for name in RETIRED | CURRENT:
            (self.migrations / name).write_text("SELECT 1;\n")
        self.log = root / "calls.txt"
        psql = root / "psql"
        psql.write_text(f"#!{sys.executable}\n" + """
import os, sys
from pathlib import Path
args = sys.argv[1:]
if '-Atqc' in args:
    print(os.environ['TEST_GROUP_MODEL'])
    sys.exit(0)
name = Path(args[args.index('-f')+1]).name
with open(os.environ['TEST_MIGRATION_CALLS'], 'a') as f:
    f.write(name + '\\n')
if name == os.environ.get('TEST_FAIL_MIGRATION'):
    sys.exit(1)
""")
        psql.chmod(0o700)
        self.env = {**os.environ, "PATH": str(root) + os.pathsep + os.environ["PATH"],
                    "DATABASE_URL": "postgres://test-only.invalid/fixture",
                    "MULTICA_TEAM_MIGRATIONS_DIR": str(self.migrations),
                    "TEST_MIGRATION_CALLS": str(self.log)}

    def run_model(self, model, **extra):
        return subprocess.run(["bash", str(SCRIPT)], env={**self.env, "TEST_GROUP_MODEL": model, **extra},
                              capture_output=True, text=True)

    def calls(self):
        return self.log.read_text().splitlines() if self.log.exists() else []

    def test_first_upgrade_runs_original_files_in_order(self):
        result = self.run_model("legacy")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.calls(), sorted(RETIRED | CURRENT))

    def test_upgraded_database_never_recreates_retired_group_indexes(self):
        result = self.run_model("multi")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.calls(), sorted(CURRENT))
        for name in RETIRED:
            self.assertIn("skip retired migration: " + name, result.stdout)

    def test_invalid_or_unknown_marker_runs_no_migration(self):
        for value in ("invalid", "", "unexpected"):
            with self.subTest(value=value):
                result = self.run_model(value)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(self.calls(), [])

    def test_migration_failure_stops_before_later_files(self):
        name = "493_infoflow_private_scope.up.sql"
        result = self.run_model("multi", TEST_FAIL_MIGRATION=name)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.calls(), [n for n in sorted(CURRENT) if n <= name])


if __name__ == "__main__":
    unittest.main()
