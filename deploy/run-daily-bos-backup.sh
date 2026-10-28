#!/bin/bash
# Host retry boundary also covers a temporarily unavailable Docker service.
set -uo pipefail
umask 077
instance_dir="${1:-}"
docker_bin="${2:-}"
python_image="${3:-}"
case "$instance_dir" in /*) ;; *) exit 2;; esac
test "$instance_dir" != / && test -f "$instance_dir/config/daily-backup.json" || exit 2
test -x "$docker_bin" || exit 2
command -v flock >/dev/null || exit 2
[[ "$python_image" =~ ^sha256:[a-f0-9]{64}$ ]] || exit 2
state_dir="$instance_dir/data/daily-backup"
mkdir -p "$state_dir"
chmod 700 "$state_dir"
exec 9>"$state_dir/host-run.lock"
if ! flock -n 9; then
 printf '{"ok":true,"skipped":"already-running"}\n'
 exit 0
fi
stop_job(){ "$docker_bin" stop -t 30 team-mesh-daily-backup-job >/dev/null 2>&1 || true; }
attempt_result="$(mktemp "$state_dir/.attempt-result.XXXXXXXX")" || exit 2
cleanup(){ rm -f -- "$attempt_result"; }
trap cleanup EXIT
trap 'stop_job; exit 143' TERM INT
for attempt in 1 2 3; do
 "$docker_bin" run --rm --name team-mesh-daily-backup-job --log-driver none --network host \
  -v "$instance_dir:$instance_dir" -v /var/run/docker.sock:/var/run/docker.sock \
  -v "$docker_bin:/usr/local/bin/docker:ro" \
  -e HTTPS_PROXY -e HTTP_PROXY -e NO_PROXY \
  "$python_image" python3 "$instance_dir/code/multica-team/deploy/daily-bos-backup.py" \
  --config "$instance_dir/config/daily-backup.json" --attempt "$attempt" > "$attempt_result"
 code=$?
 stamp=$(date -u +%FT%TZ)
 if [ "$code" = 0 ]; then
  if grep -Eq '"skipped"[[:space:]]*:[[:space:]]*"already-running"' "$attempt_result"; then
   printf '{"ok":true,"skipped":"already-running","finished_at":"%s"}\n' "$stamp" >> "$state_dir/runner-runs.jsonl"
   exit 0
  fi
  printf '{"ok":true,"attempts":%s,"finished_at":"%s"}\n' "$attempt" "$stamp" >> "$state_dir/runner-runs.jsonl"
  notice_ok=false
  if [[ "${TEAM_MESH_BACKUP_NOTIFY_USER:-}" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]]; then
   if "$docker_bin" exec -i team-mesh-bridge node /team/code/multica-team/deploy/notify-backup-success.mjs "$TEAM_MESH_BACKUP_NOTIFY_USER" < "$attempt_result"; then notice_ok=true; fi
  fi
  printf '{"ok":%s,"kind":"success","attempts":%s,"finished_at":"%s"}\n' "$notice_ok" "$attempt" "$(date -u +%FT%TZ)" > "$state_dir/.notification-result.tmp"
  mv -f "$state_dir/.notification-result.tmp" "$state_dir/notification-result.json"
  if [ "$notice_ok" = true ]; then exit 0; fi
  # A delivery failure is not a backup failure and must not trigger a new
  # backup attempt. Keep the verified backup and report notification failure.
  exit 1
 fi
 stop_job
 printf '{"ok":false,"attempts":%s,"exit_code":%s,"failed_stage":"backup_or_container","finished_at":"%s"}\n' "$attempt" "$code" "$stamp" > "$state_dir/.runner-result.tmp"
 mv -f "$state_dir/.runner-result.tmp" "$state_dir/last-result.json"
 printf '{"ok":false,"attempts":%s,"exit_code":%s,"finished_at":"%s"}\n' "$attempt" "$code" "$stamp" >> "$state_dir/runner-runs.jsonl"
 if [ "$attempt" != 3 ]; then sleep 600; fi
done
notice_ok=false
if [[ "${TEAM_MESH_BACKUP_NOTIFY_USER:-}" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]];then
 if "$docker_bin" exec team-mesh-bridge node /team/code/multica-team/deploy/notify-backup-failure.mjs "$TEAM_MESH_BACKUP_NOTIFY_USER";then notice_ok=true;fi
fi
printf '{"ok":%s,"attempts":3,"finished_at":"%s"}\n' "$notice_ok" "$(date -u +%FT%TZ)" > "$state_dir/notification-result.json"
exit 1
