CREATE INDEX CONCURRENTLY IF NOT EXISTS infoflow_source_comment_idx ON infoflow_issue_source(comment_id) WHERE comment_id IS NOT NULL;
