CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS infoflow_binding_issue_group_idx ON infoflow_issue_binding(issue_id) WHERE conversation_kind='group' AND issue_id IS NOT NULL;
