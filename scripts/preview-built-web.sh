#!/bin/bash
# Preview a generated frontend using a completed production build, never HMR.
set -euo pipefail
if [ "$#" -lt 2 ] || [ "$#" -gt 3 ]; then
  echo 'Usage: bash scripts/preview-built-web.sh build/<stage> http://127.0.0.1:<api-port> [web-port]' >&2
  exit 2
fi
task_repo=$(cd "$(dirname "$0")/.." && pwd -P)
task_stage=$(cd "$1" && pwd -P)
task_api=$2
task_port=${3:-18094}
task_node=${TEAMMESH_NODE_BIN:-node}
case "$task_stage" in "$task_repo"/build/*) ;; *) echo 'Use an isolated generated directory under build/.' >&2; exit 2;; esac
test ! -e "$task_stage/.git"
test -f "$task_stage/apps/web/package.json"
case "$task_api" in http://127.0.0.1:*|http://localhost:*) ;; *) echo 'Local previews must use an isolated loopback API.' >&2; exit 2;; esac
[[ "$task_port" =~ ^[0-9]+$ ]] && [ "$task_port" -ge 1024 ] && [ "$task_port" -le 65535 ]
export NODE_ENV=production REMOTE_API_URL="$task_api" NEXT_PUBLIC_API_URL=''
cd "$task_stage/apps/web"
"$task_node" node_modules/next/dist/bin/next build --webpack
test -s .next/BUILD_ID
exec "$task_node" node_modules/next/dist/bin/next start --hostname 127.0.0.1 --port "$task_port"
