-- Invoked by application transactions; no trigger, FK or cascade is installed.
CREATE OR REPLACE FUNCTION team_mesh_ensure_issue_managers(p_issue uuid) RETURNS integer LANGUAGE plpgsql AS $$
DECLARE i issue%ROWTYPE; effective integer; source_run agent_task_queue%ROWTYPE; initialized boolean;
BEGIN
 SELECT * INTO i FROM issue WHERE id=p_issue FOR UPDATE;
 IF NOT FOUND THEN RETURN 0; END IF;
 SELECT count(*) INTO effective FROM issue_manager im JOIN member m ON m.user_id=im.user_id AND m.workspace_id=i.workspace_id WHERE im.issue_id=i.id;
 SELECT EXISTS(SELECT 1 FROM issue_manager_state WHERE issue_id=i.id) INTO initialized;
 IF effective>0 THEN
  IF NOT initialized THEN INSERT INTO issue_manager_state(issue_id) VALUES(i.id); END IF;
  RETURN effective;
 END IF;
 IF NOT initialized THEN
  IF i.creator_type='agent' AND i.origin_id IS NOT NULL AND i.origin_type IN ('agent_create','quick_create') THEN
   SELECT * INTO source_run FROM agent_task_queue WHERE id=i.origin_id AND agent_id=i.creator_id;
  END IF;
  WITH candidates(user_id,priority) AS (
   SELECT i.creator_id,0 WHERE i.creator_type='member'
   UNION ALL
   SELECT im.user_id,1 FROM issue_manager im WHERE i.creator_type='agent' AND i.parent_issue_id IS NOT NULL AND im.issue_id=i.parent_issue_id
   UNION ALL
   SELECT source_run.originator_user_id,2 WHERE i.creator_type='agent' AND i.parent_issue_id IS NULL AND source_run.originator_user_id IS NOT NULL
   UNION ALL
   SELECT im.user_id,3 FROM issue_manager im WHERE i.creator_type='agent' AND i.parent_issue_id IS NULL AND source_run.issue_id IS NOT NULL AND im.issue_id=source_run.issue_id
   UNION ALL
   SELECT a.owner_id,4 FROM agent a WHERE i.creator_type='agent' AND a.id=i.creator_id
   UNION ALL
   SELECT m.user_id,5 FROM member m WHERE m.workspace_id=i.workspace_id AND m.role='owner'
  ), eligible AS (
   SELECT DISTINCT c.user_id,c.priority FROM candidates c JOIN member m ON m.user_id=c.user_id AND m.workspace_id=i.workspace_id
  )
  INSERT INTO issue_manager(issue_id,user_id)
  SELECT i.id,user_id FROM eligible WHERE priority=(SELECT min(priority) FROM eligible) ON CONFLICT DO NOTHING;
 ELSE
  INSERT INTO issue_manager(issue_id,user_id)
  SELECT i.id,m.user_id FROM member m WHERE m.workspace_id=i.workspace_id AND m.role='owner' ORDER BY m.created_at,m.id LIMIT 1 ON CONFLICT DO NOTHING;
 END IF;
 SELECT count(*) INTO effective FROM issue_manager im JOIN member m ON m.user_id=im.user_id AND m.workspace_id=i.workspace_id WHERE im.issue_id=i.id;
 IF effective=0 THEN RAISE EXCEPTION 'issue has no eligible administrator'; END IF;
 IF NOT initialized THEN INSERT INTO issue_manager_state(issue_id) VALUES(i.id); END IF;
 RETURN effective;
END $$;
DO $$ DECLARE r record; BEGIN
 FOR r IN SELECT id FROM issue WHERE NOT EXISTS(SELECT 1 FROM issue_manager_state WHERE issue_id=issue.id) ORDER BY created_at,id LOOP
  PERFORM team_mesh_ensure_issue_managers(r.id);
 END LOOP;
END $$;
