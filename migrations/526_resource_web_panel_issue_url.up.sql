CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS resource_web_panel_issue_url_idx ON resource_web_panel (issue_id, digest(url, 'sha256')) WHERE issue_id IS NOT NULL;
