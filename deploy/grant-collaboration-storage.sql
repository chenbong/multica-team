-- Run after migrations as the schema owner, passing the existing API role.
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE
  issue_manager, issue_manager_state, attachment_storage_alias,
  attachment_object_delete, agent_token_read_scope TO :"application_role";
GRANT EXECUTE ON FUNCTION team_mesh_ensure_issue_managers(uuid) TO :"application_role";
