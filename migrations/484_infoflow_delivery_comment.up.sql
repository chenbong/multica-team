CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS infoflow_delivery_comment_idx ON infoflow_issue_delivery(binding_id, generation, comment_id);
