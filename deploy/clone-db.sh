#!/bin/bash
# Clone another database into this instance once, without stopping the source.
# Point MULTICA_CLONE_SOURCE_DB (deploy/host.env or the environment) at it.
set -euo pipefail
cd "$(dirname "$0")"
. ./env.sh
OLD_DATABASE_NAME="${MULTICA_CLONE_SOURCE_DB:-}"
if [ -z "$OLD_DATABASE_NAME" ]; then
  echo "set MULTICA_CLONE_SOURCE_DB to the database to snapshot, for example in deploy/host.env" >&2
  exit 1
fi

pg_isready -h 127.0.0.1 -p 5432 >/dev/null 2>&1 || pg_ctlcluster 16 main start
if su - postgres -c "psql -tAc \"SELECT 1 FROM pg_database WHERE datname = '$DATABASE_NAME'\"" | grep -q 1; then
  echo "$DATABASE_NAME already exists; refusing to overwrite it" >&2
  exit 1
fi
su - postgres -c "psql -c \"CREATE DATABASE $DATABASE_NAME OWNER multica\"" >/dev/null
pg_dump "postgres://multica:multica@127.0.0.1:5432/$OLD_DATABASE_NAME?sslmode=disable" \
  --format=custom --no-owner --no-acl \
  | su - postgres -c "pg_restore --exit-on-error --no-owner --no-acl -d $DATABASE_NAME"
echo "cloned $OLD_DATABASE_NAME into $DATABASE_NAME"
