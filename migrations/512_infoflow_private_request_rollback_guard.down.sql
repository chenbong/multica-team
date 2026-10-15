DO $$ BEGIN
  IF EXISTS(SELECT 1 FROM infoflow_web_private_request WHERE state IN ('queued','pending') AND expires_at>clock_timestamp()) THEN
    RAISE EXCEPTION 'pending private binding requests: finish or explicitly cancel before rollback';
  END IF;
END $$;
