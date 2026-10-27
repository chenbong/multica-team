CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS project_working_directory_identity_idx ON project_resource (project_id) WHERE resource_type = 'working_directory';
