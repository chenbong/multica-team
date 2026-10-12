CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS runtime_profile_workspace_personal_key ON runtime_profile(workspace_id,personal_profile_id) WHERE personal_profile_id IS NOT NULL;
