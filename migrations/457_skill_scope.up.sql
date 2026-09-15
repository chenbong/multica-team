ALTER TABLE skill ADD COLUMN IF NOT EXISTS scope TEXT NOT NULL DEFAULT 'workspace';
ALTER TABLE skill ADD COLUMN IF NOT EXISTS owner_user_id UUID;
UPDATE skill SET scope='workspace' WHERE scope IS NULL OR scope='';
DO $$ BEGIN ALTER TABLE skill ADD CONSTRAINT skill_scope_check CHECK (scope IN ('personal','workspace')); EXCEPTION WHEN duplicate_object THEN NULL; END $$;
ALTER TABLE skill DROP CONSTRAINT IF EXISTS skill_workspace_id_name_key;
