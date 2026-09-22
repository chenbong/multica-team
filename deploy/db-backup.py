#!/usr/bin/env python3
"""Periodic logical backups for the local Multica PostgreSQL database."""
from datetime import datetime, timezone
import os
from pathlib import Path
import signal
import subprocess
import time

INSTANCE_DIR = Path(__file__).resolve().parent.parent
BACKUP_DIR = Path(os.environ.get("MULTICA_BACKUP_DIR", INSTANCE_DIR / "backups")).resolve()
DATABASE_NAME = os.environ.get("MULTICA_DATABASE_NAME", os.environ.get("DATABASE_NAME", "multica"))
INTERVAL_SECONDS = int(os.environ.get("MULTICA_BACKUP_INTERVAL_SECONDS", "1800"))
RETENTION_SECONDS = int(os.environ.get("MULTICA_BACKUP_RETENTION_SECONDS", "1209600"))
STOP = False


def stop_handler(_signum, _frame):
    global STOP
    STOP = True


for signal_number in (signal.SIGINT, signal.SIGTERM):
    signal.signal(signal_number, stop_handler)


def backup_environment():
    env = os.environ.copy()
    env.update(
        {
            "PGHOST": "127.0.0.1",
            "PGPORT": "5432",
            "PGUSER": "multica",
            "PGDATABASE": DATABASE_NAME,
            "PGPASSWORD": os.environ["MULTICA_DATABASE_PASSWORD"],
        }
    )
    return env


def run_backup():
    BACKUP_DIR.mkdir(parents=True, exist_ok=True)
    os.chmod(BACKUP_DIR, 0o700)
    stamp = datetime.now(timezone.utc).astimezone().strftime("%Y%m%d-%H%M%S")
    dump_path = BACKUP_DIR / ("multica-" + stamp + ".dump")
    globals_path = BACKUP_DIR / ("multica-" + stamp + "-globals.sql")
    dump_tmp = BACKUP_DIR / ("." + dump_path.name + ".tmp")
    globals_tmp = BACKUP_DIR / ("." + globals_path.name + ".tmp")
    env = backup_environment()

    try:
        subprocess.run(
            ["pg_dump", "--format=custom", "--no-owner", "--file", str(dump_tmp)],
            check=True,
            env=env,
            timeout=900,
        )
        subprocess.run(
            ["pg_dumpall", "--globals-only", "--file", str(globals_tmp)],
            check=True,
            env=env,
            timeout=120,
        )
        os.replace(dump_tmp, dump_path)
        os.replace(globals_tmp, globals_path)
        print("backup completed: " + str(dump_path), flush=True)
    finally:
        dump_tmp.unlink(missing_ok=True)
        globals_tmp.unlink(missing_ok=True)

    cutoff = time.time() - RETENTION_SECONDS
    for path in BACKUP_DIR.glob("multica-*.dump"):
        if path.stat().st_mtime < cutoff:
            path.unlink(missing_ok=True)
    for path in BACKUP_DIR.glob("multica-*-globals.sql"):
        if path.stat().st_mtime < cutoff:
            path.unlink(missing_ok=True)


while not STOP:
    try:
        run_backup()
    except Exception as error:
        print("backup failed: " + str(error), flush=True)
    for _ in range(INTERVAL_SECONDS):
        if STOP:
            break
        time.sleep(1)
