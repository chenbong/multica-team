#!/usr/bin/env python3
"""Smoke-test audit ownership as a maintenance role; every object rolls back.

Usage: python3 scripts/test-skill-transfer-migration.py LOCAL_POSTGRES_DSN
Requires a disposable loopback PostgreSQL database and CREATEROLE privileges.
"""
import os
from pathlib import Path
import subprocess
import sys
from urllib.parse import urlsplit
import uuid


def main():
    dsn = sys.argv[1] if len(sys.argv) == 2 else os.environ.get("DATABASE_URL", "")
    if urlsplit(dsn).hostname not in ("localhost", "127.0.0.1", "::1"):
        raise SystemExit("Use an explicit disposable loopback PostgreSQL database")
    suffix = uuid.uuid4().hex[:16]
    role, schema = "st_owner_" + suffix, "st_fixture_" + suffix
    migration = (Path(__file__).resolve().parents[1] / "migrations/558_skill_transfer_audit.up.sql").read_text()
    query = f"""BEGIN;
CREATE ROLE {role};
CREATE SCHEMA {schema};
SET LOCAL search_path={schema},public;
GRANT USAGE ON SCHEMA {schema} TO {role};
CREATE TABLE skill(id uuid PRIMARY KEY);
ALTER TABLE skill OWNER TO {role};
"""
    # Replaying must be harmless, including transfer of the owned identity sequence.
    query += migration + "\n" + migration
    query += "\nSET LOCAL ROLE " + role + ";\n"
    query += """INSERT INTO team_mesh_skill_transfer_audit(skill_id,skill_name,from_user_id,to_user_id)
VALUES('00000000-0000-0000-0000-000000000001','fixture','00000000-0000-0000-0000-000000000002','00000000-0000-0000-0000-000000000003');
SELECT count(*) FROM team_mesh_skill_transfer_audit;
RESET ROLE;
ROLLBACK;
"""
    result = subprocess.run(["psql", dsn, "-X", "-Atq", "-v", "ON_ERROR_STOP=1"], input=query, text=True, capture_output=True)
    if result.returncode or result.stdout.strip() != "1":
        raise SystemExit("Audit migration smoke failed: " + result.stderr)
    print("Audit migration and replay passed under a distinct Skill-owner role; all fixtures rolled back.")


if __name__ == "__main__":
    main()
