DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM infoflow_issue_binding WHERE conversation_kind='group' AND issue_id IS NOT NULL GROUP BY issue_id HAVING count(*)>1) THEN
        RAISE EXCEPTION 'An issue is bound to multiple groups; resolve bindings explicitly before upgrading';
    END IF;
END $$;
