-- Team skills are global across users and workspaces. Project skills keep
-- their owning workspace so the list can show "A project", "B project", etc.
ALTER TABLE skill ALTER COLUMN workspace_id DROP NOT NULL;

ALTER TABLE skill DROP CONSTRAINT IF EXISTS skill_scope_check;
ALTER TABLE skill ADD CONSTRAINT skill_scope_check
  CHECK (scope IN ('personal', 'workspace', 'team'));

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_skill_team_name
  ON skill(name) WHERE scope = 'team';
