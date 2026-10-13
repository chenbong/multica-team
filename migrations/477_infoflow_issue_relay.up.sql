-- No foreign keys: relay transactions validate workspace and entity scope.
CREATE TABLE IF NOT EXISTS infoflow_issue_binding (
    id UUID NOT NULL,
    robot_id TEXT NOT NULL,
    workspace_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    conversation_kind TEXT NOT NULL CHECK (conversation_kind IN ('private', 'group')),
    conversation_id TEXT NOT NULL,
    issue_id UUID,
    binder_id UUID,
    generation UUID NOT NULL,
    bound_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE IF NOT EXISTS infoflow_issue_source (
    id UUID NOT NULL,
    binding_id UUID NOT NULL,
    generation UUID NOT NULL,
    message_id TEXT NOT NULL,
    sender TEXT NOT NULL,
    user_id UUID NOT NULL,
    request_hash TEXT NOT NULL,
    issue_id UUID,
    comment_id UUID,
    task_id UUID,
    response JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE IF NOT EXISTS infoflow_issue_delivery (
    id UUID NOT NULL,
    binding_id UUID NOT NULL,
    generation UUID NOT NULL,
    source_id UUID NOT NULL,
    comment_id UUID NOT NULL,
    content TEXT NOT NULL,
    external_message_id TEXT,
    acked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
