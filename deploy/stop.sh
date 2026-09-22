#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")"
. ./env.sh

python3 "$INSTANCE_DIR/infoflow-bridge/scripts/bridgectl.py" stop || true
for name in web api backup; do
  pidfile="$DEPLOY_DIR/logs/$name.pid"
  [ -f "$pidfile" ] || continue
  pid="$(cat "$pidfile")"
  if kill -0 "$pid" 2>/dev/null; then
    kill -TERM -"$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null || true
    echo "stopped $name (pid $pid)"
  fi
  rm -f "$pidfile"
done
./postgres.sh stop || true
