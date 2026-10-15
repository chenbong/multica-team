CREATE TABLE IF NOT EXISTS infoflow_robot_directory (
    robot_id TEXT NOT NULL,
    agent_id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    robot_name TEXT NOT NULL DEFAULT '',
    robot_chat_id TEXT NOT NULL DEFAULT '',
    private_enabled BOOLEAN NOT NULL,
    available BOOLEAN NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
