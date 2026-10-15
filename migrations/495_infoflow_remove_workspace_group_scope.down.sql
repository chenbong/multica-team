CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS infoflow_binding_group_scope_idx ON infoflow_issue_binding(workspace_id,conversation_id) WHERE conversation_kind='group';
