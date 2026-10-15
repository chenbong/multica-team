-- Data-only backfill is deliberately retained on rollback: a later owner edit
-- cannot safely be distinguished from an initial default. Never remove or
-- overwrite somebody's environment as part of an application rollback.
SELECT 1;
