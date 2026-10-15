ALTER TABLE IF EXISTS infoflow_issue_delivery ADD COLUMN IF NOT EXISTS external_message_ids TEXT[] NOT NULL DEFAULT '{}';
