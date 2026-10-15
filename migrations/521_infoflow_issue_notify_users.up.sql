-- Old source receipts retain their original request hash.
-- Legacy group sources recover their sender through delivery ancestry.
-- If the relay schema is absent, rerun this DDL after 477 before enabling relay.
ALTER TABLE IF EXISTS infoflow_issue_source
    ADD COLUMN IF NOT EXISTS notify_users TEXT[] NOT NULL DEFAULT '{}';
-- NULL means a legacy delivery has not yet snapshotted its safe source audience.
-- An empty array is an intentional, frozen audience (private/web-native output).
ALTER TABLE IF EXISTS infoflow_issue_delivery
    ADD COLUMN IF NOT EXISTS notify_users TEXT[];
