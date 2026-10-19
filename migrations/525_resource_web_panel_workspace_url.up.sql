CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS resource_web_panel_workspace_url_idx ON resource_web_panel (workspace_id, digest(url, 'sha256')) WHERE workspace_id IS NOT NULL;
