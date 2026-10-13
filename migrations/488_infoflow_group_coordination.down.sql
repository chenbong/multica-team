ALTER TABLE IF EXISTS infoflow_issue_binding
  DROP COLUMN IF EXISTS group_topology,
  DROP COLUMN IF EXISTS group_state,
  DROP COLUMN IF EXISTS leader_robot_id,
  DROP COLUMN IF EXISTS leader_verified_at;
