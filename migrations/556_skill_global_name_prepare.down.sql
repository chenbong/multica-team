BEGIN;
LOCK TABLE skill IN SHARE ROW EXCLUSIVE MODE;
DROP TRIGGER IF EXISTS team_mesh_skill_name_guard ON skill;
-- Restore only names that have not subsequently been edited by a person.
CREATE TEMP TABLE skill_name_rollback ON COMMIT DROP AS
SELECT s.id,a.old_name,'tmesh-rollback-'||replace(gen_random_uuid()::text,'-','') AS temporary_name
FROM skill s JOIN team_mesh_skill_rename_audit a ON a.skill_id=s.id AND a.new_name=s.name;
UPDATE skill s SET name=p.temporary_name FROM skill_name_rollback p WHERE s.id=p.id;
UPDATE skill s SET name=p.old_name FROM skill_name_rollback p WHERE s.id=p.id;
DROP FUNCTION IF EXISTS team_mesh_skill_name_guard();
DROP FUNCTION IF EXISTS team_mesh_skill_name_key(text);
DROP FUNCTION IF EXISTS team_mesh_skill_name_trim(text);
COMMIT;
