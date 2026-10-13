-- Refuse rollback while a worker could still complete an accepted request.
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM infoflow_web_group_request WHERE state IN ('queued','pending') AND expires_at>clock_timestamp()) THEN
    RAISE EXCEPTION 'pending web group binding requests: finish or explicitly cancel before rollback';
  END IF;
END $$;
DROP TABLE IF EXISTS infoflow_web_group_request;
DROP TABLE IF EXISTS infoflow_group_observation;
