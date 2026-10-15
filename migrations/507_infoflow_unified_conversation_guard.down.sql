DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM infoflow_issue_binding WHERE conversation_kind='private'
      GROUP BY robot_id,workspace_id,agent_id,conversation_kind,conversation_id HAVING count(*)>1) THEN
    RAISE EXCEPTION 'multiple private binding rows: explicitly reconcile retained histories before rollback';
  END IF;
  IF EXISTS (SELECT 1 FROM issue WHERE infoflow_assignment_version<>0)
      OR EXISTS (SELECT 1 FROM infoflow_issue_binding WHERE assignment_version<>0) THEN
    RAISE EXCEPTION 'assignment version history: explicitly reconcile before rollback';
  END IF;
END $$;
