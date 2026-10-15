CREATE INDEX CONCURRENTLY IF NOT EXISTS infoflow_web_group_poll_idx ON infoflow_web_group_request (workspace_id,next_at) WHERE state IN ('queued','pending');
