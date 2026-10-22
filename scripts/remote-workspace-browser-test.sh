#!/usr/bin/env bash
# After remote-workspace-test.sh, exercise the actual React UI against a local
# mock transport. This is NOT a live Agent/database end-to-end deployment test.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
stage="${REMOTE_WORKSPACE_WEB_STAGE:-$root/build/remote-workspace-test}"
mkdir -p "$stage/packages/views/workspace-smoke"
cp "$root"/scripts/remote-workspace-smoke/* "$stage/packages/views/workspace-smoke/"
cp "$root/scripts/remote-workspace-smoke/browser.mjs" "$stage/workspace-smoke.mjs"
cd "$stage"
if [[ -z "${PLAYWRIGHT_EXECUTABLE_PATH:-}" ]]; then pnpm exec playwright install chromium; fi
(cd packages/views && exec pnpm exec vite --config workspace-smoke/vite.config.ts) > "$stage/workspace-smoke.log" 2>&1 &
server=$!
trap 'kill "$server" 2>/dev/null || true' EXIT
for i in $(seq 1 40); do
 if curl -fsS http://127.0.0.1:4179/ >/dev/null; then break; fi
 sleep 0.5
done
node workspace-smoke.mjs
