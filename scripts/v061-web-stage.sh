#!/usr/bin/env bash
# Build an isolated frontend input without touching the upstream checkout.
set -euo pipefail
task_root="$(cd "$(dirname "$0")/.." && pwd)"
expected=2ea01ae4ef55de4310b99af192d2dbd367832883
test "$(git -C "$task_root/multica" rev-parse HEAD)" = "$expected"
test -z "$(git -C "$task_root/multica" status --porcelain)"
stage="$(mktemp -d /tmp/multica-v061-web.XXXXXX)"
mkdir -p "$stage/source" "$stage/original-overlays"
git -C "$task_root/multica" archive "$expected" | tar -x -C "$stage/source"
cp -R "$task_root/overlays/web-direct" "$stage/original-overlays/"
cp "$task_root/overlays/web-patch.py" "$stage/original-overlays/"
cp "$task_root/deploy/apply-brand.cjs" "$stage/original-overlays/"
printf '%s\n' "$stage"
