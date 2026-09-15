#!/bin/bash
# Optional isolated daemon. It uses a separate profile and is not started by
# deploy/start.sh, preventing a cloned pending task from being executed twice.
set -euo pipefail
cd "$(dirname "$0")"
. ./env.sh

MULTICA_HOME_DIR="${MULTICA_HOME:-$HOME/.multica}"
PROFILE_DIR="$MULTICA_HOME_DIR/profiles/$PROFILE_NAME"
# Source profile to clone the signed-in token from. Defaults to the CLI's own
# default profile; point MULTICA_SOURCE_PROFILE at a named profile instead.
SOURCE_PROFILE="${MULTICA_SOURCE_PROFILE:-$MULTICA_HOME_DIR/config.json}"
PROFILE_CONFIG="$PROFILE_DIR/config.json"
mkdir -p "$PROFILE_DIR"
if [ ! -f "$PROFILE_CONFIG" ]; then
  if [ ! -f "$SOURCE_PROFILE" ]; then
    echo "source Multica profile is missing: $SOURCE_PROFILE" >&2
    exit 1
  fi
  cp -p "$SOURCE_PROFILE" "$PROFILE_CONFIG"
fi
python3 - "$PROFILE_CONFIG" "$MULTICA_DAEMON_SERVER_URL" "$FRONTEND_ORIGIN" <<'PY'
import json
import os
import sys
path, server_url, app_url = sys.argv[1:]
data = json.loads(open(path, encoding="utf-8").read())
if not data.get("token"):
    raise SystemExit("isolated profile has no token")
data["server_url"] = server_url
data["app_url"] = app_url
with open(path, "w", encoding="utf-8") as handle:
    json.dump(data, handle, indent=2)
    handle.write("\n")
os.chmod(path, 0o600)
PY

pidfile="$DEPLOY_DIR/logs/daemon.pid"
if [ -f "$pidfile" ] && kill -0 "$(cat "$pidfile")" 2>/dev/null; then
  echo "daemon already running (pid $(cat "$pidfile"))"
  exit 0
fi
setsid nohup "$DEPLOY_DIR/bin/multica" --profile "$PROFILE_NAME" daemon start \
  --foreground --no-auto-update --server-url "$MULTICA_DAEMON_SERVER_URL" \
  >> "$DEPLOY_DIR/logs/daemon.log" 2>&1 < /dev/null &
echo $! > "$pidfile"
echo "started isolated daemon profile $PROFILE_NAME (pid $!)"
