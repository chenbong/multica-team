CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS issue_manager_identity_idx ON issue_manager(issue_id,user_id);
