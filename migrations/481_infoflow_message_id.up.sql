CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS infoflow_message_id_idx ON infoflow_issue_source(binding_id, message_id);
