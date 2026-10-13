CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS infoflow_binding_scope_idx ON infoflow_issue_binding(robot_id,workspace_id,agent_id,conversation_kind,conversation_id);
