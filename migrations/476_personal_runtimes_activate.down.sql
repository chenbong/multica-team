-- Begin rollback while all personal indexes/functions are still available.
-- Keep writers stopped until 467.down succeeds. A refusal here changes nothing.
BEGIN;
SET LOCAL lock_timeout = '5s';
DO $migration$
BEGIN
 IF to_regprocedure('team_mesh_prepare_personal_runtime_rollback()') IS NOT NULL THEN
   PERFORM team_mesh_prepare_personal_runtime_rollback();
 END IF;
END $migration$;
COMMIT;
