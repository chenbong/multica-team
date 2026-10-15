DROP TRIGGER IF EXISTS infoflow_assignment_version_trigger ON issue;
DROP FUNCTION IF EXISTS infoflow_assignment_version_bump();
ALTER TABLE issue DROP COLUMN IF EXISTS infoflow_assignment_version;
ALTER TABLE infoflow_issue_binding DROP COLUMN IF EXISTS assignment_version;
ALTER TABLE infoflow_issue_binding DROP COLUMN IF EXISTS robot_chat_id;
ALTER TABLE infoflow_issue_binding DROP COLUMN IF EXISTS robot_name;
