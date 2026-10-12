#!/usr/bin/env python3
"""Real PostgreSQL 15+ migration tests; Python stdlib and PostgreSQL CLI only.

    python3 scripts/personal-runtime-migration-test.py --restore

The ONLY writable database is runtime_scope_migration_test on
127.0.0.1:25439. --restore explicitly recreates that disposable database from
the supplied dump; runtime_scope_test and remote databases are never used.
No server, Go, frontend, daemon or production connection is started here.
Concurrency cases use real sessions and observed pg_stat_activity lock waits,
not timing-only sleeps. Tests exercise the intended canonical lock, retirement
and TTL lifecycle. Current Go needs separate handler integration verification.
"""

import argparse
import contextlib
import json
import os
from pathlib import Path
import queue
import re
import subprocess
import threading
import time
import unittest
import uuid


ROOT = Path(__file__).resolve().parents[1]
MANIFEST = (
    "467_personal_runtimes",
    "468_personal_profile_id_index",
    "469_personal_runtime_id_index",
    "470_personal_profile_owner_name_index",
    "471_personal_runtime_identity_index",
    "472_runtime_profile_owner_name_index",
    "473_runtime_profile_personal_index",
    "474_agent_runtime_personal_index",
    "475_runtime_profile_rollback_index",
    "476_personal_runtimes_activate",
)
UP = tuple(ROOT / "migrations" / (name + ".up.sql") for name in MANIFEST)
DOWN = tuple(ROOT / "migrations" / (name + ".down.sql") for name in reversed(MANIFEST))
DATABASE = "runtime_scope_migration_test"
CONNECT = ["-h", "127.0.0.1", "-p", "25439", "-U", "postgres"]
PSQL = ["psql", "-X", "-qAt", "-v", "ON_ERROR_STOP=1", "-v", "VERBOSITY=verbose", *CONNECT, "-d", DATABASE]
ENV = {**os.environ, "PGAPPNAME": "personal-runtime-migration-tests", "PGCONNECT_TIMEOUT": "5"}
# Do not inherit a service or options that might redirect these fixed inputs.
for _key in ("PGSERVICE", "PGSERVICEFILE", "PGOPTIONS", "PGHOSTADDR"):
    ENV.pop(_key, None)


def ident():
    return str(uuid.uuid4())


def literal(value):
    return "NULL" if value is None else "'" + str(value).replace("'", "''") + "'"


class SqlError(RuntimeError):
    pass


def sql(statement):
    result = subprocess.run(PSQL + ["-c", statement], env=ENV, text=True, capture_output=True, timeout=20)
    if result.returncode:
        raise SqlError(result.stderr.strip())
    return result.stdout.strip()


def migrate(path):
    for file in path if isinstance(path, (tuple, list)) else (path,):
        result = subprocess.run(PSQL + ["-f", str(file)], env=ENV, text=True, capture_output=True, timeout=20)
        if result.returncode:
            raise SqlError(file.name + ": " + result.stderr.strip())


def digest(table, omit=(), where="true"):
    minus = "".join(" - " + literal(column) for column in omit)
    return sql(f"SELECT md5(coalesce(jsonb_agg(to_jsonb(t){minus} ORDER BY id)::text,'')) FROM {table} t WHERE {where}")


class Session:
    def __init__(self):
        self.output = queue.Queue()
        self.proc = subprocess.Popen(PSQL, env=ENV, text=True, stdin=subprocess.PIPE,
                                     stdout=subprocess.PIPE, stderr=subprocess.STDOUT, bufsize=1)
        self.reader = threading.Thread(target=self._read, daemon=True)
        self.reader.start()
        self.execute("SET statement_timeout='8s'; SET lock_timeout='6s'; SET deadlock_timeout='100ms';")
        self.pid = int(self.execute("SELECT pg_backend_pid();"))

    def _read(self):
        for line in self.proc.stdout:
            self.output.put(line.rstrip("\n"))
        self.output.put(None)

    def start(self, statement):
        marker = "done_" + uuid.uuid4().hex
        self.proc.stdin.write(statement + "\n\\echo " + marker + "\n")
        self.proc.stdin.flush()
        return marker

    def finish(self, marker, timeout=10):
        lines = []
        deadline = time.monotonic() + timeout
        while True:
            line = self.output.get(timeout=max(0.01, deadline - time.monotonic()))
            if line == marker:
                return "\n".join(lines)
            if line is None:
                raise SqlError("\n".join(lines))
            lines.append(line)

    def execute(self, statement):
        return self.finish(self.start(statement))

    def close(self):
        if self.proc.poll() is None:
            self.proc.stdin.close()
            try:
                self.proc.wait(timeout=2)
            except subprocess.TimeoutExpired:
                self.proc.terminate()
                self.proc.wait(timeout=2)
        self.proc.stdout.close()
        self.reader.join(timeout=2)

    def __enter__(self):
        return self

    def __exit__(self, *_):
        self.close()


def wait_locked(session):
    deadline = time.monotonic() + 4
    while time.monotonic() < deadline:
        if sql(f"SELECT wait_event_type='Lock' FROM pg_stat_activity WHERE pid={session.pid}") == "t":
            return
        time.sleep(0.01)
    raise AssertionError("session did not reach the expected database lock wait")


class Fixture:
    def __init__(self):
        self.owner, self.other = ident(), ident()
        self.w1, self.w2, self.w3 = ident(), ident(), ident()
        self.profile, self.runtime = ident(), ident()
        self.daemon = "migration-test-" + ident()
        sql(f"""
          INSERT INTO "user"(id,name,email) VALUES
            ('{self.owner}','migration-test','{self.owner}@migration.invalid'),
            ('{self.other}','migration-test','{self.other}@migration.invalid');
          INSERT INTO workspace(id,name,slug) VALUES
            ('{self.w1}','migration-test','{self.w1}'),
            ('{self.w2}','migration-test','{self.w2}'),
            ('{self.w3}','migration-test','{self.w3}');
          INSERT INTO member(workspace_id,user_id,role) VALUES
            ('{self.w1}','{self.owner}','owner'),('{self.w2}','{self.owner}','owner'),
            ('{self.w1}','{self.other}','member'),('{self.w3}','{self.other}','owner');
          {self.profile_insert(self.w1, self.profile)};
          {self.runtime_insert(self.w1, self.runtime, self.profile)};
        """)
        self.cp = sql(f"SELECT personal_profile_id FROM runtime_profile WHERE id='{self.profile}'")
        self.cr = sql(f"SELECT personal_runtime_id FROM agent_runtime WHERE id='{self.runtime}'")

    def profile_insert(self, workspace, row_id=None, owner=None, canonical=None, name="migration-profile"):
        return f"""INSERT INTO runtime_profile(id,workspace_id,created_by,display_name,
          protocol_family,runtime_type,command_name,fixed_args,personal_profile_id)
          VALUES ('{row_id or ident()}','{workspace}','{owner or self.owner}',{literal(name)},
          'codex','codex','old-command','["--old"]',{literal(canonical)})"""

    def runtime_insert(self, workspace, row_id=None, profile=None, daemon=None, canonical=None, owner=None, custom=None):
        return f"""INSERT INTO agent_runtime(id,workspace_id,owner_id,daemon_id,name,runtime_mode,
          provider,status,profile_id,personal_runtime_id,custom_name)
          VALUES ('{row_id or ident()}','{workspace}','{owner or self.owner}',
          '{daemon or self.daemon}','test-machine','local','codex','online',
          {literal(profile)},{literal(canonical)},{literal(custom)})"""

    def ensure(self, workspace=None):
        return f"SELECT team_mesh_ensure_runtime_workspace('{self.owner}','{workspace or self.w2}');"

    def profile_edit(self, command="edited-command"):
        return f"""SELECT id FROM personal_runtime_profile WHERE id='{self.cp}' FOR UPDATE;
          UPDATE personal_runtime_profile SET command_name={literal(command)},fixed_args='["--new"]',
          enabled=false,description='edited description',revision=revision+1 WHERE id='{self.cp}';"""

    def profile_propagate(self):
        return f"""UPDATE runtime_profile p SET display_name=c.display_name,command_name=c.command_name,
          description=c.description,fixed_args=c.fixed_args,enabled=c.enabled,updated_at=c.updated_at
          FROM personal_runtime_profile c WHERE c.id='{self.cp}' AND p.personal_profile_id=c.id;"""

    def runtime_propagate(self):
        return f"""UPDATE agent_runtime r SET custom_name=c.custom_name FROM personal_runtime c
          WHERE c.id='{self.cr}' AND r.personal_runtime_id=c.id;"""

    def cleanup(self):
        sql(f"""BEGIN;
          DELETE FROM agent_task_queue WHERE agent_id IN (SELECT id FROM agent WHERE owner_id IN ('{self.owner}','{self.other}'));
          DELETE FROM agent WHERE workspace_id IN ('{self.w1}','{self.w2}','{self.w3}');
          DELETE FROM agent_runtime WHERE workspace_id IN ('{self.w1}','{self.w2}','{self.w3}');
          DELETE FROM runtime_profile WHERE workspace_id IN ('{self.w1}','{self.w2}','{self.w3}');
          DO $$ BEGIN
            IF to_regclass('personal_runtime') IS NOT NULL THEN
              DELETE FROM personal_runtime WHERE owner_id IN ('{self.owner}','{self.other}');
              DELETE FROM personal_runtime_profile WHERE owner_id IN ('{self.owner}','{self.other}');
            END IF;
          END $$;
          DELETE FROM workspace WHERE id IN ('{self.w1}','{self.w2}','{self.w3}');
          DELETE FROM "user" WHERE id IN ('{self.owner}','{self.other}');
          COMMIT;""")


class MigrationTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.foreign_keys = sql("SELECT md5(string_agg(conrelid::text||':'||conname||':'||pg_get_constraintdef(oid),',' ORDER BY conrelid,conname)) FROM pg_constraint WHERE contype='f'")
        cls.before = {table: digest(table) for table in ("agent", "agent_task_queue")}
        cls.runtime_ids = sql("SELECT string_agg(id::text,',' ORDER BY id) FROM agent_runtime")
        cls.profile_ids = sql("SELECT string_agg(id::text,',' ORDER BY id) FROM runtime_profile")
        cls.profile_counts = sql("SELECT count(DISTINCT ROW(created_by,display_name)) FROM runtime_profile WHERE created_by IS NOT NULL")
        cls.runtime_counts = sql("""SELECT count(DISTINCT ROW(r.owner_id,r.daemon_id,r.provider,p.created_by,p.display_name))
            FROM agent_runtime r LEFT JOIN runtime_profile p ON p.id=r.profile_id WHERE r.owner_id IS NOT NULL AND r.daemon_id IS NOT NULL""")
        cls.registrations = digest("agent_runtime", ("visibility", "custom_name"))
        migrate(UP)

    @contextlib.contextmanager
    def fixture(self):
        f = Fixture()
        try:
            yield f
        finally:
            f.cleanup()
            migrate(UP)

    def rejected(self, statement, reason):
        with self.assertRaisesRegex(SqlError, reason):
            sql(statement)

    def test_01_restored_snapshot_ids_tasks_and_registration_data_preserved(self):
        self.assertEqual(sql("SELECT count(*) FROM personal_runtime"), self.runtime_counts)
        self.assertEqual(sql("SELECT count(*) FROM personal_runtime_profile"), self.profile_counts)
        self.assertEqual(sql("SELECT string_agg(id::text,',' ORDER BY id) FROM agent_runtime"), self.runtime_ids)
        self.assertEqual(sql("SELECT string_agg(id::text,',' ORDER BY id) FROM runtime_profile"), self.profile_ids)
        for table, before in self.before.items():
            self.assertEqual(digest(table), before, table)
        self.assertEqual(digest("agent_runtime", ("personal_runtime_id", "visibility", "custom_name")), self.registrations)
        self.assertEqual(sql("SELECT count(*) FROM agent_runtime WHERE personal_runtime_id IS NULL OR visibility<>'private'"), "0")

    def test_02_repeated_up_is_an_exact_noop(self):
        tables = ("runtime_profile", "agent_runtime", "personal_runtime_profile", "personal_runtime", "agent_task_queue")
        before = {table: digest(table) for table in tables}
        migrate(UP)
        migrate(UP)
        self.assertEqual(before, {table: digest(table) for table in tables})

    def test_03_owner_identity_and_workspace_isolation(self):
        with self.fixture() as f:
            for statement in (
                f"UPDATE runtime_profile SET created_by=NULL WHERE id='{f.profile}'",
                f"UPDATE agent_runtime SET owner_id=NULL WHERE id='{f.runtime}'",
                f"UPDATE agent_runtime SET daemon_id=NULL WHERE id='{f.runtime}'",
                f"UPDATE agent_runtime SET workspace_id='{f.w2}' WHERE id='{f.runtime}'",
                f"UPDATE agent_runtime SET personal_runtime_id=NULL WHERE id='{f.runtime}'",
                f"UPDATE personal_runtime SET owner_id='{f.other}' WHERE id='{f.cr}'",
                f"UPDATE personal_runtime_profile SET runtime_type='claude' WHERE id='{f.cp}'",
            ):
                self.rejected(statement, "23514")
            self.rejected(f.profile_insert(f.w2, owner=f.other, canonical=f.cp), "P0002")
            self.rejected(f.runtime_insert(f.w2, profile=f.profile), "P0002")
            self.rejected(f.runtime_insert(f.w1, profile=f.profile, owner=f.other), "P0002")
            self.rejected(f"INSERT INTO personal_runtime(owner_id,daemon_id,provider,name,runtime_mode,personal_profile_id) VALUES ('{f.other}','x','codex','x','local','{f.cp}')", "23514")
            self.rejected(f"DELETE FROM personal_runtime WHERE id='{f.cr}'", "23514")
            self.rejected(f"DELETE FROM personal_runtime_profile WHERE id='{f.cp}'", "23514")
            self.rejected(f"DELETE FROM runtime_profile WHERE id='{f.profile}'", "23514")

    def test_04_agent_bindings_cannot_cross_owner_or_workspace(self):
        with self.fixture() as f:
            for owner, workspace in ((f.other, f.w1), (None, f.w1), (f.owner, f.w2)):
                self.rejected(f"INSERT INTO agent(workspace_id,owner_id,name,runtime_mode,runtime_id) VALUES ('{workspace}',{literal(owner)},'unsafe','local','{f.runtime}')", "23514")
            aid = ident()
            sql(f"INSERT INTO agent(id,workspace_id,owner_id,name,runtime_mode,runtime_id) VALUES ('{aid}','{f.w1}','{f.owner}','valid','local','{f.runtime}')")
            self.rejected(f"UPDATE agent SET owner_id='{f.other}' WHERE id='{aid}'", "23514")
            sql(f"UPDATE agent SET runtime_id=NULL WHERE id='{aid}'")

    def test_05_stale_daemon_profile_and_explicit_identity_use_canonical_config(self):
        with self.fixture() as f:
            sql("BEGIN;" + f.profile_edit() + f.profile_propagate() + "COMMIT;")
            alias = ident()
            sql(f.profile_insert(f.w2, alias))
            self.assertEqual(sql(f"SELECT command_name||':'||enabled::text||':'||fixed_args::text FROM runtime_profile WHERE id='{alias}'"), 'edited-command:false:["--new"]')
            sql(f"UPDATE personal_runtime_profile SET display_name='renamed profile' WHERE id='{f.cp}';" + f.profile_propagate())
            sql(f"DELETE FROM runtime_profile WHERE id='{alias}'")
            sql(f.profile_insert(f.w2, alias, canonical=f.cp))
            self.assertEqual(sql(f"SELECT display_name FROM runtime_profile WHERE id='{alias}'"), "renamed profile")
            self.assertEqual(sql(f"SELECT count(*) FROM personal_runtime_profile WHERE owner_id='{f.owner}'"), "1")

    def test_06_stale_runtime_registration_preserves_rename(self):
        with self.fixture() as f:
            sql(f"BEGIN; SELECT id FROM personal_runtime WHERE id='{f.cr}' FOR UPDATE; UPDATE personal_runtime SET custom_name='owner name' WHERE id='{f.cr}';" + f.runtime_propagate() + "COMMIT;")
            sql(f.runtime_insert(f.w1, profile=f.profile, custom="stale") + " ON CONFLICT(workspace_id,daemon_id,profile_id) WHERE profile_id IS NOT NULL DO UPDATE SET status='online',custom_name=excluded.custom_name")
            self.assertEqual(sql(f"SELECT custom_name FROM agent_runtime WHERE id='{f.runtime}'"), "owner name")
            self.assertEqual(sql(f"SELECT custom_name FROM personal_runtime WHERE id='{f.cr}'"), "owner name")

    def test_07_membership_materializes_only_supported_instances_offline(self):
        with self.fixture() as f:
            sql(f.profile_insert(f.w1, name="unavailable-on-this-machine"))
            self.rejected(f.ensure(f.w3), "workspace membership required")
            sql(f.ensure())
            sql(f.ensure())
            self.assertEqual(sql(f"SELECT count(*) FROM runtime_profile WHERE workspace_id='{f.w2}'"), "2")
            self.assertEqual(sql(f"SELECT count(*) FROM agent_runtime WHERE workspace_id='{f.w2}'"), "1")
            self.assertEqual(sql(f"SELECT status||':'||(last_seen_at IS NULL)::text FROM agent_runtime WHERE workspace_id='{f.w2}'"), "offline:true")
            self.assertEqual(sql(f"SELECT count(*) FROM agent_runtime WHERE workspace_id='{f.w3}'"), "0")
            sql(f.ensure(f.w1))
            self.assertEqual(sql(f"SELECT status FROM agent_runtime WHERE id='{f.runtime}'"), "online")

    def test_08_delete_does_not_resurrect_through_materialization_or_stale_canonical_id(self):
        with self.fixture() as f:
            sql(f"BEGIN; SELECT id FROM personal_runtime WHERE id='{f.cr}' FOR UPDATE; DELETE FROM agent_runtime WHERE id='{f.runtime}'; DELETE FROM personal_runtime WHERE id='{f.cr}'; COMMIT;")
            sql(f.ensure())
            self.assertEqual(sql(f"SELECT count(*) FROM agent_runtime WHERE owner_id='{f.owner}'"), "0")
            self.rejected(f.runtime_insert(f.w1, profile=f.profile, canonical=f.cr), "P0002")
            self.assertEqual(sql(f"SELECT count(*) FROM personal_runtime WHERE owner_id='{f.owner}'"), "0")

    def test_09_workspace_delete_retains_personal_data_and_down_refuses_loss(self):
        with self.fixture() as f:
            # runtime_profile historically lacks a workspace FK; emulate the
            # application's explicit profile cleanup as well as FK cascades.
            sql(f"DELETE FROM workspace WHERE id='{f.w1}'; DELETE FROM runtime_profile WHERE workspace_id='{f.w1}'")
            self.assertEqual(sql(f"SELECT count(*) FROM personal_runtime WHERE id='{f.cr}'"), "1")
            with self.assertRaisesRegex(SqlError, "unprojected personal configuration"):
                migrate(DOWN)
            self.assertEqual(sql(f"SELECT count(*) FROM personal_runtime_profile WHERE id='{f.cp}'"), "1")
            sql(f.ensure())
            self.assertEqual(sql(f"SELECT count(*) FROM agent_runtime WHERE workspace_id='{f.w2}'"), "1")

    def test_10_down_up_reapply_preserves_latest_config_new_rows_and_task_history(self):
        with self.fixture() as f:
            aid, tid = ident(), ident()
            sql(f"INSERT INTO agent(id,workspace_id,owner_id,name,runtime_mode,runtime_id) VALUES ('{aid}','{f.w1}','{f.owner}','history-agent','local','{f.runtime}'); INSERT INTO agent_task_queue(id,agent_id,runtime_id,status,completed_at,result) VALUES ('{tid}','{aid}','{f.runtime}','completed',now(),'{{\"retained\":true}}')")
            task_digest = digest("agent_task_queue")
            # Intentionally omit propagation. Rollback must read authority,
            # not assume an older alias already contains the latest edit.
            sql(f.profile_edit() + f"UPDATE personal_runtime SET custom_name='latest name' WHERE id='{f.cr}'")
            migrate(DOWN)
            self.assertEqual(sql(f"SELECT command_name||':'||enabled::text FROM runtime_profile WHERE id='{f.profile}'"), "edited-command:false")
            self.assertEqual(sql(f"SELECT custom_name FROM agent_runtime WHERE id='{f.runtime}'"), "latest name")
            self.assertEqual(digest("agent_task_queue"), task_digest)
            down_digest = digest("agent_runtime")
            migrate(DOWN)
            self.assertEqual(digest("agent_runtime"), down_digest)
            migrate(UP)
            migrate(UP)
            self.assertEqual(sql(f"SELECT custom_name FROM agent_runtime WHERE id='{f.runtime}'"), "latest name")
            self.assertEqual(digest("agent_task_queue"), task_digest)

    def test_11_down_equal_names_across_owners_refuses_atomically(self):
        with self.fixture() as f:
            sql(f.profile_insert(f.w1, owner=f.other))
            before = digest("runtime_profile")
            with self.assertRaisesRegex(SqlError, "equal profile names"):
                migrate(DOWN)
            self.assertEqual(digest("runtime_profile"), before)
            self.assertEqual(sql("SELECT to_regprocedure('team_mesh_profile_projection()') IS NOT NULL"), "t")

    def test_12_preflight_conflicting_profiles_and_names_are_atomic(self):
        with self.fixture() as f:
            sql(f.ensure())
            migrate(DOWN)
            sql(f"UPDATE runtime_profile SET command_name='conflict' WHERE workspace_id='{f.w2}'")
            before = digest("runtime_profile", ("personal_profile_id",))
            with self.assertRaisesRegex(SqlError, "conflicting profile configurations"):
                migrate(UP)
            self.assertEqual(digest("runtime_profile", ("personal_profile_id",)), before)
            self.assertEqual(sql("SELECT count(*) FROM personal_runtime_profile"), "0")
            sql(f"UPDATE runtime_profile SET command_name='old-command' WHERE workspace_id='{f.w2}'; UPDATE agent_runtime SET custom_name=CASE WHEN workspace_id='{f.w1}' THEN 'one' ELSE 'two' END WHERE owner_id='{f.owner}'")
            with self.assertRaisesRegex(SqlError, "conflicting computer names"):
                migrate(UP)
            self.assertEqual(sql("SELECT count(*) FROM personal_runtime"), "0")

    def test_13_preflight_bad_agent_binding_refuses_atomically(self):
        with self.fixture() as f:
            migrate(DOWN)
            sql(f"INSERT INTO agent(workspace_id,owner_id,name,runtime_mode,runtime_id) VALUES ('{f.w1}','{f.other}','bad-existing-binding','local','{f.runtime}')")
            with self.assertRaisesRegex(SqlError, "cross-owner, cross-workspace or dangling binding"):
                migrate(UP)
            self.assertEqual(sql("SELECT count(*) FROM personal_runtime"), "0")

    def test_14_concurrent_profile_edit_registration_retries_without_deadlock(self):
        with self.fixture() as f, Session() as edit, Session() as registration:
            edit.execute("BEGIN;" + f.profile_edit())
            with self.assertRaisesRegex(SqlError, "40001"):
                registration.execute(f.profile_insert(f.w2) + ";")
            edit.execute(f.profile_propagate() + "COMMIT;")
            sql(f.profile_insert(f.w2))
            self.assertEqual(sql(f"SELECT command_name FROM runtime_profile WHERE workspace_id='{f.w2}'"), "edited-command")

    def test_15_heartbeat_alias_first_does_not_deadlock_canonical_rename(self):
        with self.fixture() as f, Session() as heartbeat, Session() as rename:
            heartbeat.execute(f"BEGIN; UPDATE agent_runtime SET last_seen_at=now() WHERE id='{f.runtime}';")
            rename.execute(f"BEGIN; SELECT id FROM personal_runtime WHERE id='{f.cr}' FOR UPDATE; UPDATE personal_runtime SET custom_name='raced rename' WHERE id='{f.cr}';")
            pending = rename.start(f.runtime_propagate())
            wait_locked(rename)
            heartbeat.execute("COMMIT;")
            rename.finish(pending)
            rename.execute("COMMIT;")
            self.assertEqual(sql(f"SELECT custom_name FROM agent_runtime WHERE id='{f.runtime}'"), "raced rename")

    def test_16_legacy_profile_alias_lock_cannot_deadlock_profile_delete(self):
        with self.fixture() as f, Session() as deletion, Session() as registration:
            registration.execute(f"BEGIN; SELECT id FROM runtime_profile WHERE id='{f.profile}' FOR KEY SHARE;")
            deletion.execute(f"BEGIN; SELECT id FROM personal_runtime_profile WHERE id='{f.cp}' FOR UPDATE;")
            pending = deletion.start(f"SELECT id FROM runtime_profile WHERE id='{f.profile}' FOR UPDATE;")
            wait_locked(deletion)
            with self.assertRaisesRegex(SqlError, "40001"):
                registration.execute(f.runtime_insert(f.w1, profile=f.profile, daemon="second-machine") + ";")
            deletion.finish(pending)
            deletion.execute("ROLLBACK;")
            self.assertEqual(sql(f"SELECT count(*) FROM personal_runtime WHERE owner_id='{f.owner}'"), "1")

    def test_17_ensure_waits_for_delete_without_recreating_removed_machine(self):
        with self.fixture() as f, Session() as deletion, Session() as projection:
            deletion.execute(f"BEGIN; SELECT id FROM personal_runtime WHERE id='{f.cr}' FOR UPDATE; DELETE FROM agent_runtime WHERE id='{f.runtime}'; DELETE FROM personal_runtime WHERE id='{f.cr}';")
            pending = projection.start(f.ensure())
            wait_locked(projection)
            deletion.execute("COMMIT;")
            projection.finish(pending)
            self.assertEqual(sql(f"SELECT count(*) FROM agent_runtime WHERE owner_id='{f.owner}'"), "0")

    def test_18_membership_removal_waits_until_projection_finishes(self):
        with self.fixture() as f, Session() as projection, Session() as removal:
            projection.execute("BEGIN;" + f.ensure())
            pending = removal.start(f"DELETE FROM member WHERE workspace_id='{f.w2}' AND user_id='{f.owner}';")
            wait_locked(removal)
            projection.execute("COMMIT;")
            removal.finish(pending)
            self.rejected(f.ensure(), "workspace membership required")

    def test_19_teardown_detaches_completed_history_before_deleting_runtime(self):
        with self.fixture() as f:
            aid, tid = ident(), ident()
            sql(f"INSERT INTO agent(id,workspace_id,owner_id,name,runtime_mode,runtime_id) VALUES ('{aid}','{f.w1}','{f.owner}','retained-agent','local','{f.runtime}'); INSERT INTO agent_task_queue(id,agent_id,runtime_id,status,completed_at,result) VALUES ('{tid}','{aid}','{f.runtime}','completed',now(),'{{\"retained\":true}}')")
            sql(f"BEGIN; SELECT id FROM personal_runtime WHERE id='{f.cr}' FOR UPDATE; SELECT id FROM agent_runtime WHERE id='{f.runtime}' FOR UPDATE; UPDATE agent SET runtime_id=NULL WHERE id='{aid}'; UPDATE agent_task_queue SET runtime_id=NULL WHERE id='{tid}'; DELETE FROM agent_runtime WHERE id='{f.runtime}'; DELETE FROM personal_runtime WHERE id='{f.cr}'; COMMIT;")
            self.assertEqual(sql(f"SELECT (runtime_id IS NULL)::text||':'||(result->>'retained') FROM agent_task_queue WHERE id='{tid}'"), "true:true")
            self.assertEqual(sql(f"SELECT runtime_id IS NULL FROM agent WHERE id='{aid}'"), "t")

    def test_20_owner_cleanup_is_explicit_without_new_foreign_keys_or_cascades(self):
        with self.fixture() as f:
            # No account-delete feature is introduced. Fixture/admin cleanup
            # removes its canonical rows explicitly before deleting the user.
            self.assertEqual(sql("SELECT count(*) FROM pg_constraint WHERE contype='f' AND conrelid IN ('personal_runtime'::regclass,'personal_runtime_profile'::regclass)"), "0")
            sql(f"BEGIN; SELECT id FROM \"user\" WHERE id='{f.owner}' FOR UPDATE; DELETE FROM agent_runtime WHERE owner_id='{f.owner}'; DELETE FROM runtime_profile WHERE created_by='{f.owner}'; DELETE FROM personal_runtime WHERE owner_id='{f.owner}'; DELETE FROM personal_runtime_profile WHERE owner_id='{f.owner}'; DELETE FROM \"user\" WHERE id='{f.owner}'; COMMIT;")
            self.assertEqual(sql(f"SELECT count(*) FROM personal_runtime WHERE owner_id='{f.owner}'"), "0")
            self.assertEqual(sql(f"SELECT count(*) FROM personal_runtime_profile WHERE owner_id='{f.owner}'"), "0")

    def test_21_profile_delete_with_canonical_runtime_lock_first_serializes_rename(self):
        with self.fixture() as f, Session() as deletion, Session() as rename:
            rename.execute(f"BEGIN; SELECT id FROM personal_runtime WHERE id='{f.cr}' FOR UPDATE; UPDATE personal_runtime SET custom_name='rename-wins' WHERE id='{f.cr}';")
            deletion.execute(f"BEGIN; SELECT id FROM personal_runtime_profile WHERE id='{f.cp}' FOR UPDATE;")
            pending = deletion.start(f"SELECT id FROM personal_runtime WHERE personal_profile_id='{f.cp}' ORDER BY id FOR UPDATE;")
            wait_locked(deletion)
            rename.execute(f.runtime_propagate() + "COMMIT;")
            deletion.finish(pending)
            deletion.execute(f"SELECT id FROM runtime_profile WHERE personal_profile_id='{f.cp}' ORDER BY id FOR UPDATE; SELECT id FROM agent_runtime WHERE personal_runtime_id='{f.cr}' ORDER BY id FOR UPDATE; DELETE FROM agent_runtime WHERE personal_runtime_id='{f.cr}'; DELETE FROM runtime_profile WHERE personal_profile_id='{f.cp}'; DELETE FROM personal_runtime WHERE id='{f.cr}'; DELETE FROM personal_runtime_profile WHERE id='{f.cp}'; COMMIT;")
            sql(f.ensure())
            self.assertEqual(sql(f"SELECT count(*) FROM agent_runtime WHERE owner_id='{f.owner}'"), "0")

    def test_22_real_zero_uuid_profile_does_not_collide_with_builtin_identity(self):
        with self.fixture() as f:
            zero = "00000000-0000-0000-0000-000000000000"
            sql(f"INSERT INTO personal_runtime_profile(id,owner_id,display_name,protocol_family,runtime_type,command_name) VALUES ('{zero}','{f.owner}','zero-profile','codex','codex','codex')")
            alias = ident()
            sql(f.profile_insert(f.w1, alias, canonical=zero))
            sql(f.runtime_insert(f.w1, profile=alias, daemon="same-daemon"))
            sql(f.runtime_insert(f.w1, daemon="same-daemon"))
            self.assertEqual(sql(f"SELECT count(*) FROM personal_runtime WHERE owner_id='{f.owner}' AND daemon_id='same-daemon'"), "2")

    def test_23_unowned_legacy_row_cannot_be_adopted_around_binding_checks(self):
        with self.fixture() as f:
            legacy = ident()
            sql(f"INSERT INTO agent_runtime(id,workspace_id,name,runtime_mode,provider) VALUES ('{legacy}','{f.w1}','legacy','cloud','codex')")
            sql(f"INSERT INTO agent(workspace_id,owner_id,name,runtime_mode,runtime_id) VALUES ('{f.w1}','{f.other}','legacy-agent','cloud','{legacy}')")
            self.rejected(f"UPDATE agent_runtime SET owner_id='{f.owner}',daemon_id='adopted-machine' WHERE id='{legacy}'", "23514")
            sql(f"UPDATE agent_runtime SET status='offline' WHERE id='{legacy}'")
            self.assertEqual(sql(f"SELECT personal_runtime_id IS NULL FROM agent_runtime WHERE id='{legacy}'"), "t")

    def test_24_simultaneous_materialization_is_idempotent(self):
        with self.fixture() as f, Session() as first, Session() as second:
            first.execute("BEGIN;" + f.ensure())
            pending = second.start(f.ensure())
            wait_locked(second)
            first.execute("COMMIT;")
            second.finish(pending)
            self.assertEqual(sql(f"SELECT count(*) FROM agent_runtime WHERE workspace_id='{f.w2}'"), "1")
            self.assertEqual(sql(f"SELECT count(*) FROM runtime_profile WHERE workspace_id='{f.w2}'"), "1")
            self.assertEqual(sql(f"SELECT count(*) FROM personal_runtime WHERE owner_id='{f.owner}'"), "1")

    def test_25_manifest_has_no_new_foreign_keys_and_only_single_statement_concurrent_index_builds(self):
        self.assertEqual(len(UP), 10)
        self.assertEqual(len(DOWN), 10)
        builds = 0
        for path in (*UP, *DOWN):
            source = re.sub(r"--[^\n]*", "", path.read_text())
            self.assertNotRegex(source, r"(?i)\bREFERENCES\b|\bFOREIGN\s+KEY\b|\bON\s+(?:DELETE|UPDATE)\s+CASCADE\b")
            self.assertNotRegex(source, r"(?i)\bPRIMARY\s+KEY\b|\bUNIQUE\s*\(")
            if re.search(r"(?i)CREATE\s+(?:UNIQUE\s+)?INDEX", source):
                builds += 1
                self.assertRegex(source.strip(), r"(?i)^CREATE\s+(?:UNIQUE\s+)?INDEX\s+CONCURRENTLY\s+IF\s+NOT\s+EXISTS\b")
                self.assertEqual(source.count(";"), 1, path.name)
                self.assertNotRegex(source, r"(?i)\bBEGIN\b")
        self.assertEqual(builds, 8)  # Seven up indexes plus the rollback index.
        self.assertEqual(sql("SELECT md5(string_agg(conrelid::text||':'||conname||':'||pg_get_constraintdef(oid),',' ORDER BY conrelid,conname)) FROM pg_constraint WHERE contype='f'"), self.foreign_keys)

    def test_26_activation_refuses_missing_index_without_touching_legacy_data(self):
        with self.fixture() as f:
            migrate(DOWN)
            migrate(UP[:-1])
            before = digest("runtime_profile")
            sql("DROP INDEX CONCURRENTLY personal_runtime_identity_key")
            with self.assertRaisesRegex(SqlError, "missing or invalid index"):
                migrate(UP[-1])
            self.assertEqual(digest("runtime_profile"), before)
            self.assertEqual(sql("SELECT count(*) FROM personal_runtime"), "0")
            self.assertEqual(sql("SELECT to_regprocedure('team_mesh_personal_identity_guard()') IS NULL"), "t")
            migrate(UP[4])
            migrate(UP[-1])
            self.assertEqual(sql(f"SELECT count(*) FROM personal_runtime WHERE owner_id='{f.owner}'"), "1")

    def test_27_invalid_concurrent_index_requires_explicit_repair(self):
        with self.fixture() as f:
            migrate(DOWN)
            migrate(UP[:3])
            sql(f"INSERT INTO personal_runtime_profile(owner_id,display_name,protocol_family,runtime_type,command_name) VALUES ('{f.owner}','duplicate','codex','codex','codex'),('{f.owner}','duplicate','codex','codex','codex')")
            with self.assertRaisesRegex(SqlError, "23505"):
                migrate(UP[3])
            self.assertEqual(sql("SELECT indisvalid FROM pg_index WHERE indexrelid='personal_runtime_profile_owner_name_key'::regclass"), "f")
            # IF NOT EXISTS skips the invalid index, so activation must detect
            # it instead of silently accepting an unusable ON CONFLICT target.
            migrate(UP[3:-1])
            with self.assertRaisesRegex(SqlError, "missing or invalid index"):
                migrate(UP[-1])
            sql(f"DELETE FROM personal_runtime_profile WHERE owner_id='{f.owner}'")
            sql("DROP INDEX CONCURRENTLY personal_runtime_profile_owner_name_key")
            migrate(UP[3:])
            self.assertEqual(sql("SELECT indisvalid FROM pg_index WHERE indexrelid='personal_runtime_profile_owner_name_key'::regclass"), "t")

    def test_28_partial_rollback_can_resume_up_without_losing_personal_changes(self):
        with self.fixture() as f:
            sql("BEGIN;" + f.profile_edit("kept-through-recovery") + f.profile_propagate() + "COMMIT;")
            migrate(DOWN[:5])  # Prepare, restore legacy uniqueness, drop 3 indexes.
            self.assertEqual(sql(f"SELECT command_name FROM personal_runtime_profile WHERE id='{f.cp}'"), "kept-through-recovery")
            migrate(UP)
            sql(f.ensure())
            self.assertEqual(sql(f"SELECT command_name FROM runtime_profile WHERE workspace_id='{f.w2}'"), "kept-through-recovery")
            self.assertEqual(sql("SELECT to_regclass('runtime_profile_workspace_id_display_name_key') IS NULL"), "t")

    def test_29_upstream_workspace_cleanup_cte_retains_canonical_rows(self):
        with self.fixture() as f:
            # This is the existing DeleteWorkspaceRuntimesAndProjects shape.
            # Sibling data-changing CTEs have no guaranteed execution order.
            sql(f"WITH deleted_runtimes AS (DELETE FROM agent_runtime WHERE workspace_id='{f.w1}'), deleted_profiles AS (DELETE FROM runtime_profile WHERE workspace_id='{f.w1}') DELETE FROM project WHERE workspace_id='{f.w1}'")
            sql(f"DELETE FROM workspace WHERE id='{f.w1}'")
            self.assertEqual(sql(f"SELECT count(*) FROM personal_runtime WHERE id='{f.cr}'"), "1")
            self.assertEqual(sql(f"SELECT count(*) FROM personal_runtime_profile WHERE id='{f.cp}'"), "1")
            sql(f.ensure())
            self.assertEqual(sql(f"SELECT count(*) FROM agent_runtime WHERE workspace_id='{f.w2}'"), "1")

    def test_30_workspace_leave_retains_personal_configuration(self):
        with self.fixture() as f:
            sql(f"DELETE FROM member WHERE workspace_id='{f.w1}' AND user_id='{f.owner}'")
            self.rejected(f.ensure(f.w1), "workspace membership required")
            self.assertEqual(sql(f"SELECT count(*) FROM personal_runtime WHERE id='{f.cr}'"), "1")
            sql(f.ensure())
            self.assertEqual(sql(f"SELECT personal_runtime_id FROM agent_runtime WHERE workspace_id='{f.w2}'"), f.cr)

    def test_31_registration_holding_aliases_aborts_whole_transaction_before_retry(self):
        with self.fixture() as f, Session() as registration, Session() as rename:
            # Mirrors the parent's machine-owner registration fence. The
            # NOWAIT trigger must release BOTH these locks on transaction
            # abort, allowing the canonical rename to finish before a retry.
            registration.execute(f"BEGIN; SELECT pg_advisory_xact_lock(hashtextextended('{f.w1}/{f.daemon}',0)); SELECT id FROM agent_runtime WHERE workspace_id='{f.w1}' AND daemon_id='{f.daemon}' ORDER BY id FOR UPDATE;")
            rename.execute(f"BEGIN; SELECT id FROM personal_runtime WHERE id='{f.cr}' FOR UPDATE; UPDATE personal_runtime SET custom_name='committed rename' WHERE id='{f.cr}';")
            pending = rename.start(f.runtime_propagate())
            wait_locked(rename)
            upsert = f.runtime_insert(f.w1, profile=f.profile, custom="stale") + " ON CONFLICT(workspace_id,daemon_id,profile_id) WHERE profile_id IS NOT NULL DO UPDATE SET status='online',custom_name=excluded.custom_name;"
            with self.assertRaisesRegex(SqlError, "40001"):
                registration.execute(upsert)
            rename.finish(pending)
            rename.execute("COMMIT;")
            # A fresh transaction can acquire the same advisory fence and
            # register while preserving the rename that won the race.
            sql(f"BEGIN; SET LOCAL lock_timeout='2s'; SELECT pg_advisory_xact_lock(hashtextextended('{f.w1}/{f.daemon}',0));" + upsert + "COMMIT;")
            self.assertEqual(sql(f"SELECT custom_name FROM agent_runtime WHERE id='{f.runtime}'"), "committed rename")

    def test_32_profile_alias_contention_rolls_back_new_canonical_insertion(self):
        with self.fixture() as f, Session() as deletion, Session() as registration:
            deletion.execute(f"BEGIN; SELECT id FROM runtime_profile WHERE id='{f.profile}' FOR UPDATE;")
            # Canonical creation precedes the final alias KEY SHARE check.
            # Refusing that lock must not leave an orphan personal runtime.
            with self.assertRaisesRegex(SqlError, "40001"):
                registration.execute(f.runtime_insert(f.w1, profile=f.profile, daemon="new-contended-machine") + ";")
            deletion.execute("ROLLBACK;")
            self.assertEqual(sql(f"SELECT count(*) FROM personal_runtime WHERE owner_id='{f.owner}'"), "1")
            sql(f.runtime_insert(f.w1, profile=f.profile, daemon="new-contended-machine"))
            self.assertEqual(sql(f"SELECT count(*) FROM personal_runtime WHERE owner_id='{f.owner}'"), "2")

    def test_33_retirement_stops_projection_preserves_aliases_and_refuses_lossy_down(self):
        with self.fixture() as f:
            sql(f.ensure())
            sql(f"INSERT INTO member(workspace_id,user_id,role) VALUES ('{f.w3}','{f.owner}','member'); BEGIN; SELECT id FROM personal_runtime WHERE id='{f.cr}' FOR UPDATE; UPDATE personal_runtime SET retired_at=now() WHERE id='{f.cr}'; COMMIT;")
            # Existing registrations can finish their lifecycle, but neither
            # another workspace nor a replay may rematerialize the identity.
            sql(f"UPDATE agent_runtime SET last_seen_at=now() WHERE id='{f.runtime}'")
            sql(f.ensure(f.w3))
            self.assertEqual(sql(f"SELECT count(*) FROM agent_runtime WHERE personal_runtime_id='{f.cr}'"), "2")
            self.assertEqual(sql(f"SELECT count(*) FROM agent_runtime WHERE workspace_id='{f.w3}'"), "0")
            before = {table: digest(table) for table in ("personal_runtime", "runtime_profile", "agent_runtime")}
            migrate(UP)
            self.assertEqual(digest("personal_runtime"), before["personal_runtime"])
            with self.assertRaisesRegex(SqlError, "retired identities still present"):
                migrate(DOWN)
            self.assertEqual({table: digest(table) for table in before}, before)
            self.rejected(f"UPDATE personal_runtime SET daemon_id='replacement' WHERE id='{f.cr}'", "23514")
            self.rejected(f"UPDATE agent_runtime SET personal_runtime_id='{ident()}' WHERE id='{f.runtime}'", "23514")

    def test_34_concurrent_retirement_prevents_waiting_materialization(self):
        with self.fixture() as f, Session() as retirement, Session() as projection:
            retirement.execute(f"BEGIN; SELECT id FROM personal_runtime WHERE id='{f.cr}' FOR UPDATE; UPDATE personal_runtime SET retired_at=now() WHERE id='{f.cr}';")
            pending = projection.start(f.ensure())
            wait_locked(projection)
            retirement.execute("COMMIT;")
            projection.finish(pending)
            self.assertEqual(sql(f"SELECT count(*) FROM agent_runtime WHERE workspace_id='{f.w2}'"), "0")
            self.assertEqual(sql(f"SELECT count(*) FROM agent_runtime WHERE id='{f.runtime}'"), "1")
            self.assertEqual(sql(f"SELECT retired_at IS NOT NULL FROM personal_runtime WHERE id='{f.cr}'"), "t")

    def test_35_same_owner_legacy_merge_retires_then_drains_every_old_alias(self):
        with self.fixture() as f:
            old1, new1 = ident(), ident()
            sql(f.runtime_insert(f.w1, old1, daemon="old-hostname"))
            sql(f.runtime_insert(f.w1, new1, daemon="new-daemon-uuid"))
            old_c = sql(f"SELECT personal_runtime_id FROM agent_runtime WHERE id='{old1}'")
            new_c = sql(f"SELECT personal_runtime_id FROM agent_runtime WHERE id='{new1}'")
            sql(f.ensure())
            old2 = sql(f"SELECT id FROM agent_runtime WHERE personal_runtime_id='{old_c}' AND workspace_id='{f.w2}'")
            new2 = sql(f"SELECT id FROM agent_runtime WHERE personal_runtime_id='{new_c}' AND workspace_id='{f.w2}'")
            for workspace, runtime in ((f.w1, old1), (f.w2, old2)):
                aid = ident()
                sql(f"INSERT INTO agent(id,workspace_id,owner_id,name,runtime_mode,runtime_id) VALUES ('{aid}','{workspace}','{f.owner}','legacy-history','local','{runtime}'); INSERT INTO agent_task_queue(agent_id,runtime_id,status,completed_at,result) VALUES ('{aid}','{runtime}','completed',now(),'{{\"preserved\":true}}')")
            history = digest("agent_task_queue", ("runtime_id",))

            def merge(workspace, old_alias, new_alias):
                # The parent performs authorization and its existing task
                # fences; this SQL fixture validates the database contract.
                sql(f"""BEGIN;
                  SELECT id FROM workspace WHERE id='{workspace}' FOR KEY SHARE;
                  SELECT id FROM personal_runtime WHERE id IN ('{old_c}','{new_c}') AND owner_id='{f.owner}' ORDER BY id FOR UPDATE;
                  SELECT id FROM agent_runtime WHERE id IN ('{old_alias}','{new_alias}') ORDER BY id FOR UPDATE;
                  UPDATE agent_task_queue SET runtime_id='{new_alias}' WHERE runtime_id='{old_alias}';
                  UPDATE agent SET runtime_id='{new_alias}' WHERE runtime_id='{old_alias}';
                  UPDATE agent_runtime SET legacy_daemon_id=coalesce(legacy_daemon_id,'old-hostname') WHERE id='{new_alias}';
                  DELETE FROM agent_runtime WHERE id='{old_alias}';
                  UPDATE personal_runtime SET retired_at=coalesce(retired_at,now()) WHERE id='{old_c}';
                  DELETE FROM personal_runtime c WHERE c.id='{old_c}' AND NOT EXISTS(SELECT 1 FROM agent_runtime r WHERE r.personal_runtime_id=c.id);
                  COMMIT;""")

            merge(f.w1, old1, new1)
            self.assertEqual(sql(f"SELECT retired_at IS NOT NULL FROM personal_runtime WHERE id='{old_c}'"), "t")
            self.assertEqual(sql(f"SELECT count(*) FROM agent_runtime WHERE id='{old2}'"), "1")
            self.rejected(f"DELETE FROM personal_runtime WHERE id='{old_c}'", "23514")
            sql(f.ensure(f.w1))
            sql(f"INSERT INTO member(workspace_id,user_id,role) VALUES ('{f.w3}','{f.owner}','member')")
            sql(f.ensure(f.w3))
            self.assertEqual(sql(f"SELECT count(*) FROM agent_runtime WHERE personal_runtime_id='{old_c}' AND workspace_id IN ('{f.w1}','{f.w3}')"), "0")
            self.assertEqual(sql(f"SELECT status FROM agent_runtime WHERE personal_runtime_id='{new_c}' AND workspace_id='{f.w3}'"), "offline")
            merge(f.w2, old2, new2)
            self.assertEqual(sql(f"SELECT count(*) FROM personal_runtime WHERE id='{old_c}'"), "0")
            self.assertEqual(sql(f"SELECT count(*) FROM agent_runtime WHERE personal_runtime_id='{old_c}'"), "0")
            self.assertEqual(sql(f"SELECT count(*) FROM agent_task_queue WHERE runtime_id IN ('{new1}','{new2}')"), "2")
            self.assertEqual(digest("agent_task_queue", ("runtime_id",)), history)

    def test_36_legacy_workspace_ttl_filter_preserves_personal_computers(self):
        with self.fixture() as f:
            legacy = ident()
            sql(f"UPDATE agent_runtime SET status='offline',last_seen_at=now()-interval '30 days' WHERE id='{f.runtime}'; INSERT INTO agent_runtime(id,workspace_id,owner_id,name,runtime_mode,provider,status,created_at) VALUES ('{legacy}','{f.w1}','{f.owner}','old-legacy-cloud','cloud','codex','offline',now()-interval '30 days')")
            # Intended legacy GC predicate. Personal computers require an
            # explicit owner/lifecycle delete, never per-workspace TTL removal.
            deleted = sql(f"DELETE FROM agent_runtime WHERE workspace_id='{f.w1}' AND personal_runtime_id IS NULL AND status='offline' AND coalesce(last_seen_at,created_at)<now()-interval '7 days' RETURNING id")
            self.assertEqual(deleted, legacy)
            self.assertEqual(sql(f"SELECT count(*) FROM agent_runtime WHERE id='{f.runtime}'"), "1")
            self.assertEqual(sql(f"SELECT count(*) FROM personal_runtime WHERE id='{f.cr}' AND retired_at IS NULL"), "1")
            sql(f.ensure())
            self.assertEqual(sql(f"SELECT count(*) FROM agent_runtime WHERE personal_runtime_id='{f.cr}'"), "2")

    def test_37_retired_identity_rejects_fresh_insert_and_upsert_but_allows_heartbeat(self):
        with self.fixture() as f:
            sql(f.ensure())
            alias2 = sql(f"SELECT profile_id FROM agent_runtime WHERE workspace_id='{f.w2}' AND personal_runtime_id='{f.cr}'")
            sql(f"DELETE FROM agent_runtime WHERE workspace_id='{f.w2}'; BEGIN; SELECT id FROM personal_runtime WHERE id='{f.cr}' FOR UPDATE; UPDATE personal_runtime SET retired_at=now() WHERE id='{f.cr}'; COMMIT;")
            before = digest("agent_runtime")
            # Both natural-key lookup and a stale explicit canonical ID are
            # terminal conflicts, distinct from retryable lock contention.
            self.rejected(f.runtime_insert(f.w2, profile=alias2), "55000.*personal runtime identity is retired")
            self.rejected(f.runtime_insert(f.w2, profile=alias2, canonical=f.cr), "55000.*personal runtime identity is retired")
            self.rejected(f.runtime_insert(f.w1, profile=f.profile) + " ON CONFLICT(workspace_id,daemon_id,profile_id) WHERE profile_id IS NOT NULL DO UPDATE SET status='online'", "55000.*personal runtime identity is retired")
            self.assertEqual(digest("agent_runtime"), before)
            sql(f"UPDATE agent_runtime SET last_seen_at=now(),status='offline' WHERE id='{f.runtime}'")
            self.assertEqual(sql(f"SELECT status FROM agent_runtime WHERE id='{f.runtime}'"), "offline")

    def test_38_registration_retry_after_retirement_is_terminal_conflict(self):
        with self.fixture() as f, Session() as retirement, Session() as registration:
            retirement.execute(f"BEGIN; SELECT id FROM personal_runtime WHERE id='{f.cr}' FOR UPDATE; UPDATE personal_runtime SET retired_at=now() WHERE id='{f.cr}';")
            upsert = f.runtime_insert(f.w1, profile=f.profile) + " ON CONFLICT(workspace_id,daemon_id,profile_id) WHERE profile_id IS NOT NULL DO UPDATE SET status='online';"
            with self.assertRaisesRegex(SqlError, "40001"):
                registration.execute(upsert)
            retirement.execute("COMMIT;")
            self.rejected(upsert, "55000.*personal runtime identity is retired")
            self.assertEqual(sql(f"SELECT count(*) FROM agent_runtime WHERE personal_runtime_id='{f.cr}'"), "1")



def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--restore", action="store_true", help="recreate ONLY the hard-coded disposable migration test database")
    parser.add_argument("--dump", type=Path, help="explicit disposable pre-migration fixture dump (never a production backup)")
    parser.add_argument("--print-manifest", action="store_true", help="print ordered up/down file lists without connecting to any database")
    args = parser.parse_args()
    if args.print_manifest:
        print(json.dumps({"up": [str(p.relative_to(ROOT)) for p in UP], "down": [str(p.relative_to(ROOT)) for p in DOWN]}, indent=2))
        return 0
    if not args.restore:
        parser.error("--restore is required; this suite recreates its dedicated disposable database")
    if args.dump is None or not args.dump.is_file():
        parser.error("dump file does not exist")
    for command in (
        ["dropdb", *CONNECT, "--if-exists", DATABASE],
        ["createdb", *CONNECT, DATABASE],
        ["pg_restore", *CONNECT, "-d", DATABASE, "--no-owner", "--no-privileges", "--exit-on-error", str(args.dump)],
    ):
        subprocess.run(command, env=ENV, check=True, timeout=30, stdout=subprocess.DEVNULL)
    suite = unittest.defaultTestLoader.loadTestsFromTestCase(MigrationTests)
    result = unittest.TextTestRunner(verbosity=2).run(suite)
    if not result.wasSuccessful():
        return 1
    print("SQL tests passed. This does not validate Go handler canonical lock order, legacy merge/GC cleanup, or production readiness.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
