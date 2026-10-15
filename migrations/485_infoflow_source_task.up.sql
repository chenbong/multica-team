CREATE INDEX CONCURRENTLY IF NOT EXISTS infoflow_source_task_idx ON infoflow_issue_source(task_id) WHERE task_id IS NOT NULL;
