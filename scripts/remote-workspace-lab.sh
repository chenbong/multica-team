#!/usr/bin/env bash
# Interactive two-process lab. All listeners are loopback; no production DB,
# credentials, daemon configuration, source directories or model calls are used.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
stage="${REMOTE_WORKSPACE_WEB_STAGE:-$root/build/remote-workspace-test}"
cd "$root"
for port in 4180 4181; do
 if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then echo "Port $port is busy; stop the existing lab first." >&2;exit 1;fi
done
test -z "$(git -C multica status --porcelain)"
python3 overlays/prepare.py
mkdir -p "$root/build"
(cd multica/server && go test -c -overlay "$root/overlays/go-overlay.json" ./internal/daemon -o "$root/build/workspace-lab.test")
mkdir -p "$stage"
git -C multica archive HEAD | tar -x -C "$stage"
cp -R overlays/web-direct/. "$stage/"
MULTICA_WEB_TARGET="$stage" MULTICA_WEB_BACKUP="$stage/.overlay-backup" python3 overlays/web-patch.py apply
if [[ ! -d "$stage/node_modules" ]]; then (cd "$stage" && pnpm install --frozen-lockfile);fi
mkdir -p "$stage/packages/views/workspace-lab"
cp scripts/remote-workspace-lab/* "$stage/packages/views/workspace-lab/"
lab="$(mktemp -d /tmp/teammesh-live-lab.XXXXXX)"
chmod 700 "$lab"
python3 - "$lab" <<'PY'
import secrets,sys,pathlib
p=pathlib.Path(sys.argv[1])/'.teammesh-lab';p.write_text(secrets.token_hex(32));p.chmod(0o600)
PY
export TEAMMESH_LAB_TOKEN_FILE="$lab/.teammesh-lab"
export TEAMMESH_LAB_TOKEN="$(cat "$TEAMMESH_LAB_TOKEN_FILE")"
export TEAMMESH_LAB_ROOT="$lab"
TEAMMESH_LAB_ROLE=server "$root/build/workspace-lab.test" -test.run '^TestRemoteWorkspaceInteractiveLab$' -test.timeout 0 > "$lab/server.log" 2>&1 &
server=$!
TEAMMESH_LAB_ROLE=daemon "$root/build/workspace-lab.test" -test.run '^TestRemoteWorkspaceInteractiveLab$' -test.timeout 0 > "$lab/daemon.log" 2>&1 &
daemon=$!
export TEAMMESH_LAB_SERVER_PID="$server" TEAMMESH_LAB_DAEMON_PID="$daemon"
trap 'kill "$server" "$daemon" ${web:-} 2>/dev/null || true' EXIT
# The relay process receives token only through the private file, not frontend JS.
unset TEAMMESH_LAB_TOKEN
(cd "$stage/packages/views" && exec pnpm exec vite --config workspace-lab/vite.config.ts) > "$lab/web.log" 2>&1 &
web=$!
printf 'URL: http://127.0.0.1:4180/\nTemporary filesystem: %s\nServer PID: %s\nDaemon PID: %s\nLogs: %s/{server,daemon,web}.log\n' "$lab" "$server" "$daemon" "$lab"
echo 'Ctrl-C stops all lab processes. Temporary files remain for inspection; no production files touched.'
wait "$web"
