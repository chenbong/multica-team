-- Browser requests are not task bindings until fresh IM membership is verified.
CREATE TABLE IF NOT EXISTS infoflow_group_observation (
    workspace_id UUID NOT NULL,
    group_id TEXT NOT NULL,
    robot_id TEXT NOT NULL,
    agent_id UUID NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    members JSONB NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE IF NOT EXISTS infoflow_web_group_request (
    id UUID NOT NULL,
    issue_id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    group_id TEXT NOT NULL,
    requester_id UUID NOT NULL,
    generation UUID NOT NULL,
    topology JSONB NOT NULL,
    original_binding_id UUID,
    original_generation UUID,
    state TEXT NOT NULL CHECK (state IN ('queued','pending','failed','active')),
    reason TEXT NOT NULL DEFAULT '',
    lease_token UUID,
    lease_robot_id TEXT,
    lease_agent_id UUID,
    lease_until TIMESTAMPTZ,
    next_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    expires_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
