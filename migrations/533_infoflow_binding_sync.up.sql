ALTER TABLE IF EXISTS infoflow_issue_binding
    ADD COLUMN IF NOT EXISTS sync_enabled boolean NOT NULL DEFAULT true;
ALTER TABLE IF EXISTS infoflow_issue_delivery
    ADD COLUMN IF NOT EXISTS cancelled_at timestamptz;
