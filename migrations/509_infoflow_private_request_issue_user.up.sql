CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS infoflow_private_request_issue_user_idx ON infoflow_web_private_request (issue_id,requester_id);
