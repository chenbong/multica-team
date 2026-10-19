ALTER TABLE resource_web_panel
    ADD COLUMN IF NOT EXISTS created_by_agent_id UUID,
    ADD COLUMN IF NOT EXISTS source_task_id UUID;
