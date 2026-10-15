-- Group ownership no longer depends on the robot that first received /bind.
-- Refuse ambiguous pre-release data instead of silently dropping bindings or receipts.
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM infoflow_issue_binding WHERE conversation_kind='group'
             GROUP BY workspace_id,conversation_id HAVING count(*)>1) THEN
    RAISE EXCEPTION 'duplicate group bindings: explicitly reconcile before migration 488';
  END IF;
  IF EXISTS (SELECT 1 FROM infoflow_issue_binding WHERE conversation_kind='group' AND issue_id IS NOT NULL
             GROUP BY conversation_id HAVING count(*)>1) THEN
    RAISE EXCEPTION 'group bound across workspaces: explicitly reconcile before migration 488';
  END IF;
END $$;
ALTER TABLE IF EXISTS infoflow_issue_binding
  ADD COLUMN IF NOT EXISTS group_topology JSONB,
  ADD COLUMN IF NOT EXISTS group_state TEXT NOT NULL DEFAULT 'pending',
  ADD COLUMN IF NOT EXISTS leader_robot_id TEXT,
  ADD COLUMN IF NOT EXISTS leader_verified_at TIMESTAMPTZ;
