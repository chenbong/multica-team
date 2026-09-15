DROP INDEX CONCURRENTLY IF EXISTS idx_skill_owner;
DROP INDEX CONCURRENTLY IF EXISTS idx_skill_personal_owner_name;
DROP INDEX CONCURRENTLY IF EXISTS idx_skill_workspace_name;
ALTER TABLE skill DROP CONSTRAINT IF EXISTS skill_scope_check;
ALTER TABLE skill DROP COLUMN IF EXISTS owner_user_id;
ALTER TABLE skill DROP COLUMN IF EXISTS scope;
ALTER TABLE skill ADD CONSTRAINT skill_workspace_id_name_key UNIQUE (workspace_id,name);
