ALTER TABLE IF EXISTS infoflow_issue_binding ADD COLUMN IF NOT EXISTS last_polled_at TIMESTAMPTZ;
