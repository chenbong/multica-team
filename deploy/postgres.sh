#!/bin/bash
# Manage the Multica PostgreSQL cluster on the persistent trainer block volume.
# The data directory is bind-mounted below /var/lib/postgresql so the postgres
# user can access it without weakening /root permissions.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"
. ./env.sh

PG_BIN="/usr/lib/postgresql/16/bin"
PG_DATA="$MULTICA_PGDATA"
PG_MOUNT="${MULTICA_PG_MOUNT:-/var/lib/postgresql/multica}"
PG_CTL="$PG_BIN/pg_ctl"
PSQL="$PG_BIN/psql"
PG_ISREADY="$PG_BIN/pg_isready"
INITDB="$PG_BIN/initdb"
DB_NAME="$DATABASE_NAME"

ensure_packages() {
  if [ -x "$PG_CTL" ] && command -v psql >/dev/null 2>&1; then
    return
  fi
  echo "PostgreSQL 16 is missing; installing the runtime packages"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y postgresql-16 postgresql-client-16
}

mount_data() {
  mkdir -p "$PG_DATA" "$PG_MOUNT"
  chown postgres:postgres "$PG_DATA"
  chmod 700 "$PG_DATA"
  if ! mountpoint -q "$PG_MOUNT"; then
    mount --bind "$PG_DATA" "$PG_MOUNT"
  fi
  chown postgres:postgres "$PG_MOUNT"
  chmod 700 "$PG_MOUNT"
}

stop_default_cluster() {
  if command -v pg_ctlcluster >/dev/null 2>&1; then
    pg_ctlcluster 16 main stop >/dev/null 2>&1 || true
  fi
}

init_cluster() {
  if [ -f "$PG_MOUNT/PG_VERSION" ]; then
    return
  fi
  echo "Initializing persistent PostgreSQL cluster at $PG_DATA"
  runuser -u postgres -- "$INITDB" \
    -D "$PG_MOUNT" \
    --auth-local=trust \
    --auth-host=scram-sha-256 \
    --encoding=UTF8 \
    --no-locale
}

is_ready() {
  "$PG_ISREADY" -h 127.0.0.1 -p 5432 -d postgres >/dev/null 2>&1
}

start_cluster() {
  if is_ready; then
    return
  fi
  runuser -u postgres -- "$PG_CTL" \
    -D "$PG_MOUNT" \
    -l "$PG_MOUNT/server.log" \
    -o "-h 127.0.0.1 -p 5432" \
    start -w
  for attempt in $(seq 1 30); do
    if is_ready; then
      return
    fi
    sleep 1
  done
  echo "PostgreSQL did not become ready" >&2
  exit 1
}

ensure_application_role_and_database() {
  local role_exists
  local database_exists
  role_exists="$(runuser -u postgres -- "$PSQL" -p 5432 -d postgres -tAc "SELECT 1 FROM pg_roles WHERE rolname = 'multica'" | tr -d '[:space:]')"
  # Read the password from the private environment, never command arguments.
  runuser -u postgres -- "$PSQL" -p 5432 -d postgres -v ON_ERROR_STOP=1 <<'SQL'
\getenv application_password MULTICA_DATABASE_PASSWORD
SELECT format(
  CASE WHEN EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'multica')
    THEN 'ALTER ROLE multica WITH LOGIN SUPERUSER PASSWORD %L'
    ELSE 'CREATE ROLE multica LOGIN SUPERUSER PASSWORD %L' END,
  :'application_password') \gexec
SQL

  database_exists="$(runuser -u postgres -- "$PSQL" -p 5432 -d postgres -tAc "SELECT 1 FROM pg_database WHERE datname = '$DB_NAME'" | tr -d '[:space:]')"
  if [ "$database_exists" != "1" ]; then
    runuser -u postgres -- "$PSQL" -p 5432 -d postgres -v ON_ERROR_STOP=1 \
      -c "CREATE DATABASE $DB_NAME OWNER multica"
  fi
}

start() {
  ensure_packages
  stop_default_cluster
  mount_data
  init_cluster
  start_cluster
  ensure_application_role_and_database
  echo "postgres: running (data=$PG_DATA)"
}

stop() {
  if [ -x "$PG_CTL" ] && mountpoint -q "$PG_MOUNT" && [ -f "$PG_MOUNT/PG_VERSION" ]; then
    runuser -u postgres -- "$PG_CTL" -D "$PG_MOUNT" stop -m fast -w >/dev/null 2>&1 || true
  fi
  if mountpoint -q "$PG_MOUNT"; then
    umount "$PG_MOUNT" >/dev/null 2>&1 || true
  fi
  echo "postgres: stopped"
}

status() {
  if [ ! -x "$PG_CTL" ] || ! mountpoint -q "$PG_MOUNT"; then
    echo "postgres: stopped"
    return
  fi
  if is_ready; then
    echo "postgres: running (data=$PG_DATA)"
  else
    echo "postgres: stopped"
  fi
}

action="status"
if [ "$#" -gt 0 ]; then
  action="$1"
fi
case "$action" in
  start) start ;;
  stop) stop ;;
  status) status ;;
  *) echo "usage: $0 {start|stop|status}" >&2; exit 2 ;;
esac
