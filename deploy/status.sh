#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")"
. ./env.sh

./postgres.sh status
for name in api web backup; do
  pidfile="$DEPLOY_DIR/logs/$name.pid"
  if [ -f "$pidfile" ] && kill -0 "$(cat "$pidfile")" 2>/dev/null; then
    echo "$name: running (pid $(cat "$pidfile"))"
  else
    echo "$name: stopped"
  fi
done
python3 "$INSTANCE_DIR/infoflow-bridge/scripts/bridgectl.py" status
curl -s --noproxy '*' -o /dev/null -w "api    /health -> %{http_code}\n" \
  "http://127.0.0.1:$API_PORT/health" || true
curl -s --noproxy '*' -o /dev/null -w "web    /       -> %{http_code}\n" \
  "http://127.0.0.1:$WEB_PORT/" || true
curl -s --noproxy '*' -o /dev/null -w "bridge /       -> %{http_code}\n" \
  "http://127.0.0.1:$BRIDGE_PORT/" || true
echo "submodule clean: $(test -z "$(git -C "$INSTANCE_DIR/multica" status --porcelain)" && echo yes || echo no)"
