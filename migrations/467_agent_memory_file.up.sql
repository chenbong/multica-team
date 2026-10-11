-- Agent memory: Markdown/text files owned by one agent and delivered with
-- every task it runs. Rows are written by the agent memory tab (manual edit,
-- browser upload, or import from the runtime machine) and replaced as a set.
-- source_runtime_id intentionally has no foreign key: deleting a machine must
-- not delete memory that was imported from it.
CREATE TABLE IF NOT EXISTS agent_memory_file (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    path TEXT NOT NULL,
    content TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT 'manual',
    source_runtime_id UUID,
    source_path TEXT NOT NULL DEFAULT '',
    -- Disabled files stay on the Memory tab but are left out of task claims.
    enabled BOOLEAN NOT NULL DEFAULT true,
    updated_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT agent_memory_file_source_check CHECK (source IN ('manual', 'upload', 'runtime')),
    CONSTRAINT agent_memory_file_agent_path_key UNIQUE (agent_id, path)
);
