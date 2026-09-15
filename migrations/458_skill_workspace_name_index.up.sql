CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_skill_workspace_name ON skill(workspace_id,name) WHERE scope='workspace';
