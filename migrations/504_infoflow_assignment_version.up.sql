ALTER TABLE issue ADD COLUMN IF NOT EXISTS infoflow_assignment_version BIGINT NOT NULL DEFAULT 0;
ALTER TABLE infoflow_issue_binding ADD COLUMN IF NOT EXISTS assignment_version BIGINT NOT NULL DEFAULT 0;
ALTER TABLE infoflow_issue_binding ADD COLUMN IF NOT EXISTS robot_chat_id TEXT NOT NULL DEFAULT '';
ALTER TABLE infoflow_issue_binding ADD COLUMN IF NOT EXISTS robot_name TEXT NOT NULL DEFAULT '';

-- Fence even A -> B -> A. Never write bindings here: their transaction locks
-- precede issue locks and a cross-table trigger would invert that order.
CREATE OR REPLACE FUNCTION infoflow_assignment_version_bump() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.assignee_type IS DISTINCT FROM OLD.assignee_type
     OR NEW.assignee_id IS DISTINCT FROM OLD.assignee_id
     OR NEW.workspace_id IS DISTINCT FROM OLD.workspace_id THEN
    NEW.infoflow_assignment_version := OLD.infoflow_assignment_version + 1;
  ELSE
    NEW.infoflow_assignment_version := OLD.infoflow_assignment_version;
  END IF;
  RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS infoflow_assignment_version_trigger ON issue;
CREATE TRIGGER infoflow_assignment_version_trigger BEFORE UPDATE ON issue
FOR EACH ROW EXECUTE FUNCTION infoflow_assignment_version_bump();
