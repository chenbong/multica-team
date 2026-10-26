CREATE TABLE IF NOT EXISTS live_dashboard_event (
    event_id UUID NOT NULL,
    user_id UUID NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    is_page_view BOOLEAN NOT NULL DEFAULT false
);
CREATE TABLE IF NOT EXISTS live_dashboard_config (
    id INTEGER NOT NULL CHECK (id = 1),
    collection_started_at TIMESTAMPTZ
);
INSERT INTO live_dashboard_config(id, collection_started_at)
SELECT 1, NULL WHERE NOT EXISTS(SELECT 1 FROM live_dashboard_config WHERE id=1);
CREATE TABLE IF NOT EXISTS live_dashboard_cache (
    section TEXT NOT NULL CHECK(section IN ('realtime','recent','history')),
    payload JSONB NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL,
    definition_version INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE IF NOT EXISTS live_dashboard_snapshot (
    day DATE NOT NULL,
    definition_version INTEGER NOT NULL,
    payload JSONB NOT NULL,
    captured_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
