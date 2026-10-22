#!/usr/bin/env bash
# Run from any directory. Requires Go 1.26.6+, pnpm and the clean submodule.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"
test -z "$(git -C multica status --porcelain)"
python3 overlays/prepare.py
(
 cd multica/server
 go test -race -overlay "$root/overlays/go-overlay.json" ./internal/daemonws ./internal/daemon -run '^TestRemoteWorkspace' -count=1
 # Bypass the upstream DB-gated handler TestMain for this pure policy suite.
 go test "$root/overlays/go/files/server/internal/handler/remote_workspace_policy.go" "$root/overlays/go/files/server/internal/handler/remote_workspace_policy_test.go" -v
 go test -overlay "$root/overlays/go-overlay.json" ./cmd/server ./cmd/multica -run '^$'
)
stage="${REMOTE_WORKSPACE_WEB_STAGE:-$root/build/remote-workspace-test}"
mkdir -p "$stage"
git -C multica archive HEAD | tar -x -C "$stage"
cp -R overlays/web-direct/. "$stage/"
MULTICA_WEB_TARGET="$stage" MULTICA_WEB_BACKUP="$stage/.overlay-backup" python3 overlays/web-patch.py apply
(
 cd "$stage"
 pnpm install --frozen-lockfile
 pnpm --filter @multica/core typecheck
 pnpm --filter @multica/views typecheck
 pnpm --filter @multica/core exec vitest run api/remote-workspace.test.ts api/client.test.ts
 pnpm --filter @multica/views exec vitest run chat/components/remote-workspace-state.test.ts chat/components/remote-workspace.test.tsx chat/components/chat-input.test.tsx chat/components/use-chat-controller.test.tsx
)
test -z "$(git -C multica status --porcelain)"
