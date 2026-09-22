#!/bin/bash
# Start the isolated API, Web and local bridge. PostgreSQL lives on the
# persistent trainer block volume; the bridge and backup worker are supervised
# by their own process managers.
set -euo pipefail
cd "$(dirname "$0")"
. ./env.sh
mkdir -p "$DEPLOY_DIR/logs"

./postgres.sh start

(cd "$INSTANCE_DIR/multica/server" && "$DEPLOY_DIR/bin/migrate" up) \
  >> "$DEPLOY_DIR/logs/migrate.log" 2>&1
for migration in "$INSTANCE_DIR"/migrations/*.up.sql; do
  [ -e "$migration" ] || continue
  psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f "$migration" \
    >> "$DEPLOY_DIR/logs/migrate.log" 2>&1
done

BRIDGE_CONFIG="$INSTANCE_DIR/infoflow-bridge/config.local.json"
if [ ! -f "$BRIDGE_CONFIG" ]; then
  cp -p "$INSTANCE_DIR/infoflow-bridge/config.overlay.example.json" "$BRIDGE_CONFIG"
fi
python3 - "$BRIDGE_CONFIG" "$HOST_IP" "$API_PORT" "$WEB_PORT" "$BRIDGE_PORT" <<'PY'
import json
import os
import sys
path, host, api_port, web_port, bridge_port = sys.argv[1:]
data = json.loads(open(path, encoding="utf-8").read())
multica = data.setdefault("multica", {})
multica["webUrl"] = f"http://{host}:{web_port}"
multica["serverUrl"] = f"http://127.0.0.1:{api_port}"
multica["serverUrls"] = [
    f"http://127.0.0.1:{api_port}",
    f"http://{host}:{api_port}",
]
multica["apiLogPath"] = "../deploy/logs/api.log"
admin = data.setdefault("admin", {})
admin["port"] = int(bridge_port)
# Keep explicitly allowed addresses when refreshing the deployment hostname.
admin["publicHosts"] = list(dict.fromkeys([host, *admin.get("publicHosts", [])]))
data.setdefault("robots", [])
with open(path, "w", encoding="utf-8") as handle:
    json.dump(data, handle, indent=2, ensure_ascii=False)
    handle.write("\n")
os.chmod(path, 0o600)
PY

start_one() {
  local name="$1"
  shift
  local pidfile="$DEPLOY_DIR/logs/$name.pid"
  if [ -f "$pidfile" ] && kill -0 "$(cat "$pidfile")" 2>/dev/null; then
    echo "$name already running (pid $(cat "$pidfile"))"
    return
  fi
  setsid nohup "$@" >> "$DEPLOY_DIR/logs/$name.log" 2>&1 < /dev/null &
  echo $! > "$pidfile"
  echo "started $name (pid $!)"
}

start_one api "$DEPLOY_DIR/bin/server"
cd "$WEB_TREE/apps/web"
start_one web "$WEB_TREE/apps/web/node_modules/.bin/next" start \
  --hostname 0.0.0.0 --port "$WEB_PORT"
cd "$INSTANCE_DIR"
python3 "$INSTANCE_DIR/infoflow-bridge/scripts/bridgectl.py" start
start_one backup python3 "$DEPLOY_DIR/db-backup.py"

echo "API    http://$HOST_IP:$API_PORT"
echo "Web    http://$HOST_IP:$WEB_PORT"
echo "Bridge http://$HOST_IP:$BRIDGE_PORT (admin page; robots and their config live in infoflow-bridge/config.local.json)"
echo "Database data $MULTICA_PGDATA"
echo "Database backups $MULTICA_BACKUP_DIR"
