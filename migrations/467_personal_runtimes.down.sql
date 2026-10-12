-- Final rollback stage. 475.down restores the old unique index concurrently
-- BEFORE any personal index is removed. Attach that existing index without
-- building an index in this transaction. Never discard unprojected settings.
BEGIN;
SET LOCAL lock_timeout = '5s';
DO $migration$
BEGIN
 IF to_regclass('personal_runtime') IS NULL AND to_regclass('personal_runtime_profile') IS NULL THEN RETURN; END IF;
 IF to_regclass('personal_runtime') IS NULL OR to_regclass('personal_runtime_profile') IS NULL THEN
   RAISE EXCEPTION 'personal runtime rollback: incomplete schema; inspect before retrying';
 END IF;
 IF to_regprocedure('team_mesh_prepare_personal_runtime_rollback()') IS NOT NULL THEN
   PERFORM team_mesh_prepare_personal_runtime_rollback();
 ELSE
   LOCK TABLE personal_runtime_profile, personal_runtime, runtime_profile, agent_runtime, agent IN ACCESS EXCLUSIVE MODE;
   IF EXISTS(SELECT 1 FROM personal_runtime_profile) OR EXISTS(SELECT 1 FROM personal_runtime) THEN
     RAISE EXCEPTION 'personal runtime rollback: unexpected staged data; inspect before removing schema';
   END IF;
 END IF;
 IF NOT EXISTS (
    SELECT 1 FROM pg_index WHERE indexrelid=to_regclass('runtime_profile_workspace_id_display_name_key')
      AND indisvalid AND indisready AND indisunique
 ) THEN
   RAISE EXCEPTION 'personal runtime rollback: complete or repair concurrent rollback index 475 first';
 END IF;
 DROP TRIGGER IF EXISTS team_mesh_profile_projection ON runtime_profile;
 DROP TRIGGER IF EXISTS team_mesh_runtime_projection ON agent_runtime;
 DROP TRIGGER IF EXISTS team_mesh_agent_runtime_owner ON agent;
 DROP TRIGGER IF EXISTS team_mesh_profile_alias_delete ON runtime_profile;
 DROP FUNCTION IF EXISTS team_mesh_ensure_runtime_workspace(uuid,uuid);
 DROP FUNCTION IF EXISTS team_mesh_profile_projection();
 DROP FUNCTION IF EXISTS team_mesh_runtime_projection();
 DROP FUNCTION IF EXISTS team_mesh_agent_runtime_owner();
 DROP FUNCTION IF EXISTS team_mesh_prepare_personal_runtime_rollback();
 ALTER TABLE agent_runtime DROP CONSTRAINT IF EXISTS agent_runtime_personal_presence_check;
 ALTER TABLE agent_runtime DROP CONSTRAINT IF EXISTS agent_runtime_profile_owner_check;
 ALTER TABLE runtime_profile DROP CONSTRAINT IF EXISTS runtime_profile_personal_presence_check;
 ALTER TABLE agent_runtime DROP COLUMN IF EXISTS personal_runtime_id;
 ALTER TABLE runtime_profile DROP COLUMN IF EXISTS personal_profile_id;
 DROP TABLE IF EXISTS personal_runtime;
 DROP TABLE IF EXISTS personal_runtime_profile;
 DROP FUNCTION IF EXISTS team_mesh_personal_identity_guard();
 DROP FUNCTION IF EXISTS team_mesh_personal_delete_guard();
 IF NOT EXISTS (
    SELECT 1 FROM pg_constraint WHERE conrelid='runtime_profile'::regclass AND conname='runtime_profile_workspace_id_display_name_key'
 ) THEN
   ALTER TABLE runtime_profile ADD CONSTRAINT runtime_profile_workspace_id_display_name_key
     UNIQUE USING INDEX runtime_profile_workspace_id_display_name_key;
 END IF;
END $migration$;
COMMIT;
