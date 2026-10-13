CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS infoflow_group_observation_idx ON infoflow_group_observation (workspace_id,group_id,robot_id);
