-- Team rows must be reassigned or removed before rolling this migration back.
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM skill WHERE scope = 'team') THEN
    RAISE EXCEPTION 'cannot remove team scope while team skills exist';
  END IF;
END $$;
DROP INDEX CONCURRENTLY IF EXISTS idx_skill_team_name;
ALTER TABLE skill DROP CONSTRAINT IF EXISTS skill_scope_check;
ALTER TABLE skill ADD CONSTRAINT skill_scope_check
  CHECK (scope IN ('personal', 'workspace'));
ALTER TABLE skill ALTER COLUMN workspace_id SET NOT NULL;
