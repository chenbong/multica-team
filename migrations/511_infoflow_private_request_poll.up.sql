CREATE INDEX CONCURRENTLY IF NOT EXISTS infoflow_private_request_poll_idx ON infoflow_web_private_request (workspace_id,next_at) WHERE state IN ('queued','pending');
