CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS live_dashboard_snapshot_day_uq ON live_dashboard_snapshot(day,definition_version);
