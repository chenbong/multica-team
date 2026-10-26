-- Usage: psql -v app_role=EXISTING_API_ROLE -f deploy/grant-live-stats.sql
GRANT SELECT, INSERT ON live_dashboard_event TO :"app_role";
GRANT SELECT, UPDATE ON live_dashboard_config TO :"app_role";
GRANT SELECT, INSERT, UPDATE ON live_dashboard_cache TO :"app_role";
GRANT SELECT, INSERT ON live_dashboard_snapshot TO :"app_role";
