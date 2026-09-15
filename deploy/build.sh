#!/bin/bash
# Build the API/CLI with Go overlays and build the Web in a separate tree.
set -euo pipefail
cd "$(dirname "$0")"
. ./env.sh
cd "$INSTANCE_DIR"

mkdir -p "$DEPLOY_DIR/bin" "$DEPLOY_DIR/logs" "$DEPLOY_DIR/data"

if [ -n "$(git -C "$INSTANCE_DIR/multica" status --porcelain)" ]; then
  echo "multica submodule is not clean; refusing to build" >&2
  git -C "$INSTANCE_DIR/multica" status --short >&2
  exit 1
fi

python3 overlays/prepare.py
OVERLAY_JSON="$INSTANCE_DIR/overlays/go-overlay.json"

cd "$INSTANCE_DIR/multica/server"
go test -overlay="$OVERLAY_JSON" -run '^$' \
  ./cmd/server ./internal/daemon/... ./internal/handler ./internal/service ./pkg/agent ./pkg/db/generated
COMMIT="$(git -C "$INSTANCE_DIR/multica" rev-parse --short HEAD)"
go build -overlay="$OVERLAY_JSON" \
  -ldflags "-X main.commit=$COMMIT-overlay" \
  -o "$DEPLOY_DIR/bin/server" ./cmd/server
go build -overlay="$OVERLAY_JSON" \
  -ldflags "-X main.version=0.4.42-overlay -X main.commit=$COMMIT-overlay" \
  -o "$DEPLOY_DIR/bin/multica" ./cmd/multica
go build -o "$DEPLOY_DIR/bin/migrate" ./cmd/migrate

cd "$INSTANCE_DIR"
mkdir -p "$WEB_TREE"
# This tree is disposable build input. It is never the submodule checkout.
rsync -a --delete --exclude '.git' --exclude 'node_modules' --exclude '.next' \
  "$INSTANCE_DIR/multica/" "$WEB_TREE/"

while IFS= read -r rel; do
  target="$WEB_TREE/$rel"
  mkdir -p "$(dirname "$target")"
  cp -p "$INSTANCE_DIR/overlays/web-direct/$rel" "$target"
done < <(cd "$INSTANCE_DIR/overlays/web-direct" && find . -type f -print | sed 's#^./##' | sort)

rm -rf "$WEB_TREE/.overlay-backup"
MULTICA_WEB_TARGET="$WEB_TREE" \
MULTICA_WEB_BACKUP="$WEB_TREE/.overlay-backup" \
  python3 "$INSTANCE_DIR/overlays/web-patch.py" apply

if [ ! -x "$WEB_TREE/apps/web/node_modules/.bin/next" ]; then
  # Reuse a warm store when available; a fresh host may need the configured
  # network proxy to fetch missing tarballs.
  # The production target is the Web package; its trailing `...` filter also
  # includes the three local UI packages it depends on, without linking mobile
  # and desktop workspaces on a fresh host.
  if ! (cd "$WEB_TREE" && pnpm install --filter '@multica/web...' --offline --frozen-lockfile --config.confirmModulesPurge=false); then
    (cd "$WEB_TREE" && pnpm install --filter '@multica/web...' --frozen-lockfile --config.confirmModulesPurge=false)
  fi
fi

cd "$WEB_TREE/apps/web"
NEXT_FONT_GOOGLE_MOCKED_RESPONSES="$INSTANCE_DIR/overlays/font-mock.cjs" \
REMOTE_API_URL="$REMOTE_API_URL" \
FRONTEND_ORIGIN="$FRONTEND_ORIGIN" \
  ./node_modules/.bin/next build --webpack

if [ -n "$(git -C "$INSTANCE_DIR/multica" status --porcelain)" ]; then
  echo "build changed the multica submodule; refusing to continue" >&2
  git -C "$INSTANCE_DIR/multica" status --short >&2
  exit 1
fi
echo "built isolated API, CLI and Web artifacts"
