CREATE INDEX CONCURRENTLY IF NOT EXISTS live_dashboard_event_time_idx ON live_dashboard_event(observed_at) INCLUDE(user_id,is_page_view);
