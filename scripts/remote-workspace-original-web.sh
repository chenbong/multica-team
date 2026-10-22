#!/usr/bin/env bash
# Original Next.js Web + existing team overlays. NO component/controller aliases.
# First start remote-workspace-lab.sh for the isolated daemon and WS relay.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
stage="${REMOTE_WORKSPACE_WEB_STAGE:-$root/build/remote-workspace-test}"
: "${TEAMMESH_LAB_TOKEN_FILE:?Set this to the .teammesh-lab file in the temporary root printed by remote-workspace-lab.sh}"
[[ -f "$TEAMMESH_LAB_TOKEN_FILE" ]] || exit 1
for port in 4182 4183; do
 if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then echo "Port $port is busy; stop the existing preview first." >&2;exit 1;fi
done
cd "$root"
test -z "$(git -C multica status --porcelain)"
mkdir -p "$stage"
git -C multica archive HEAD | tar -x -C "$stage"
cp -R overlays/web-direct/. "$stage/"
MULTICA_WEB_TARGET="$stage" MULTICA_WEB_BACKUP="$stage/.overlay-backup" python3 overlays/web-patch.py apply
if [[ ! -d "$stage/node_modules" ]];then (cd "$stage" && pnpm install --frozen-lockfile);fi
(cd "$stage/apps/web/compat" && npm ci --ignore-scripts --no-audit --no-fund)
export TEAMMESH_LAB_WEB_STAGE="$stage"
node scripts/remote-workspace-original-web/api-server.mjs > "$stage/original-web-api.log" 2>&1 &
api=$!
trap 'kill "$api" ${web:-} 2>/dev/null || true' EXIT
# Root layout, DashboardLayout, AppSidebar, ChatPage, useChatController, ApiClient
# and WebProviders are all the actual generated product code. Data stays local.
(cd "$stage/apps/web" && export REMOTE_API_URL=http://127.0.0.1:4183 NEXT_PUBLIC_API_URL='' NEXT_PUBLIC_WS_URL='' NEXT_TELEMETRY_DISABLED=1 && exec ./node_modules/.bin/next dev --webpack --hostname 127.0.0.1 --port 4182) > "$stage/original-web-next.log" 2>&1 &
web=$!
echo 'Original Web: http://127.0.0.1:4182/local-lab/chat?session=55555555-5555-4555-8555-555555555551'
echo 'API metadata/authentication/replies are TEST fixtures; filesystem requests use the live lab daemon.'
echo "Logs: $stage/original-web-{api,next}.log"
echo 'Ctrl-C stops this Next server and API fixture (the separate daemon lab remains running).'
wait "$web"
