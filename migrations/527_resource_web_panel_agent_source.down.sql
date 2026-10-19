ALTER TABLE resource_web_panel
    DROP COLUMN IF EXISTS source_task_id,
    DROP COLUMN IF EXISTS created_by_agent_id;
