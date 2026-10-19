-- Fail atomically if long URL-derived names remain: rename those panels
-- explicitly before downgrading, rather than truncating or deleting user data.
DO $$
BEGIN
    IF to_regclass('resource_web_panel') IS NOT NULL THEN
        ALTER TABLE resource_web_panel DROP CONSTRAINT IF EXISTS resource_web_panel_name_check;
        ALTER TABLE resource_web_panel ADD CONSTRAINT resource_web_panel_name_check
            CHECK (char_length(name) BETWEEN 1 AND 120);
    END IF;
END $$;
