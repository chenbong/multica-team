-- Remove transport receipts only; platform comments are never deleted.
DELETE FROM infoflow_issue_delivery WHERE source_id IS NULL;
ALTER TABLE infoflow_issue_delivery ALTER COLUMN source_id SET NOT NULL;
