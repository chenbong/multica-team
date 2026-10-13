-- Run before restoring any old index. Never discard associations or receipts
-- to make a rollback fit the previous one-task-per-group data model.
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM infoflow_issue_binding WHERE conversation_kind='group' GROUP BY workspace_id,conversation_id HAVING count(*)>1)
       OR EXISTS (SELECT 1 FROM infoflow_issue_binding WHERE conversation_kind='group' AND issue_id IS NOT NULL GROUP BY conversation_id HAVING count(*)>1)
       OR EXISTS (SELECT 1 FROM infoflow_issue_binding GROUP BY robot_id,workspace_id,agent_id,conversation_kind,conversation_id HAVING count(*)>1) THEN
        RAISE EXCEPTION 'Multiple task bindings or retained histories cannot use the old group model; reconcile or restore a pre-upgrade backup before rollback';
    END IF;
END $$;
