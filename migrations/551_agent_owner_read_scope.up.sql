-- Per-token owner snapshots preserve the distinction between runtime owner,
-- agent owner and human invoker. Existing runs receive no new capability.
CREATE TABLE IF NOT EXISTS agent_token_read_scope (
 token_hash text NOT NULL,
 task_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 agent_id uuid NOT NULL,
 owner_id uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
