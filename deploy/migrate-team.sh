#!/bin/bash
# Apply team overlays without recreating retired group-uniqueness constraints.
# This helper only migrates the explicitly supplied database; it starts/stops no
# process. Keep the original numbered files intact for upgrade and rollback.
set -euo pipefail
: "${DATABASE_URL:?Set DATABASE_URL for the intended database}"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
migration_dir="${MULTICA_TEAM_MIGRATIONS_DIR:-$script_dir/../migrations}"

# Index 492 is the transition marker. It is created before old constraints are
# retired, so an interrupted upgrade can resume without replaying those guards.
# An invalid concurrent index requires explicit repair, not silent acceptance.
group_model="$(psql -X "$DATABASE_URL" -v ON_ERROR_STOP=1 -Atqc "
SELECT CASE
 WHEN to_regclass('public.infoflow_binding_issue_group_idx') IS NULL THEN 'legacy'
 WHEN EXISTS (SELECT 1 FROM pg_index WHERE indexrelid=to_regclass('public.infoflow_binding_issue_group_idx') AND indisvalid) THEN 'multi'
 ELSE 'invalid' END;
")"
case "$group_model" in
  legacy|multi) ;;
  *) echo "Cannot verify the issue-group migration index; repair the failed migration before retrying" >&2; exit 1 ;;
esac

for migration in "$migration_dir"/*.up.sql; do
  [ -e "$migration" ] || continue
  if [ "$group_model" = multi ]; then
    case "${migration##*/}" in
      479_infoflow_binding_scope.up.sql|488_infoflow_group_coordination.up.sql|489_infoflow_group_scope.up.sql|490_infoflow_group_global_scope.up.sql)
        echo "skip retired migration: ${migration##*/}"
        continue
        ;;
    esac
  fi
  psql -X "$DATABASE_URL" -v ON_ERROR_STOP=1 -f "$migration"
done
