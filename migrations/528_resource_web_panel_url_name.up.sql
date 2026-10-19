-- Preserve full URL-derived names without relaxing the custom-name limit.
DO $$
BEGIN
    IF to_regclass('resource_web_panel') IS NOT NULL THEN
        ALTER TABLE resource_web_panel DROP CONSTRAINT IF EXISTS resource_web_panel_name_check;
        ALTER TABLE resource_web_panel ADD CONSTRAINT resource_web_panel_name_check
            CHECK (char_length(name) BETWEEN 1 AND 120 OR
                   (name = url AND octet_length(name) BETWEEN 1 AND 4096));
    END IF;
END $$;
