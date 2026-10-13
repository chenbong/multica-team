CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS infoflow_binding_group_global_idx ON infoflow_issue_binding(conversation_id) WHERE conversation_kind='group' AND issue_id IS NOT NULL;
