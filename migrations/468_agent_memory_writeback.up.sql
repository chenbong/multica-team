-- Agent memory write-back and source sync (team migration, follows 467).
-- Re-run on every start, so every statement is idempotent.

-- Files can now also be created by the agent itself (accepted write-back).
ALTER TABLE agent_memory_file DROP CONSTRAINT IF EXISTS agent_memory_file_source_check;
ALTER TABLE agent_memory_file ADD CONSTRAINT agent_memory_file_source_check
    CHECK (source IN ('manual', 'upload', 'runtime', 'agent'));

-- auto_sync keeps a runtime-imported file mirrored from its source file on
-- that machine. sync_owner_id is the user who turned it on; sync only runs
-- while that user still owns the source runtime.
ALTER TABLE agent_memory_file ADD COLUMN IF NOT EXISTS auto_sync BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE agent_memory_file ADD COLUMN IF NOT EXISTS sync_owner_id UUID;
ALTER TABLE agent_memory_file ADD COLUMN IF NOT EXISTS synced_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS agent_memory_file_sync_idx
    ON agent_memory_file (source_runtime_id) WHERE auto_sync;

-- Per-agent write-back policy. A missing row means the defaults below.
CREATE TABLE IF NOT EXISTS agent_memory_setting (
    agent_id UUID PRIMARY KEY REFERENCES agent(id) ON DELETE CASCADE,
    writeback TEXT NOT NULL DEFAULT 'review',
    capture_runtime_memory BOOLEAN NOT NULL DEFAULT true,
    updated_by UUID,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT agent_memory_setting_writeback_check CHECK (writeback IN ('off', 'review', 'auto'))
);

-- Memory changes reported by the daemon after a run. At most one pending
-- proposal per (agent, path): a newer one supersedes the older.
CREATE TABLE IF NOT EXISTS agent_memory_proposal (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    -- No foreign key: task rows are garbage-collected, proposals are history.
    task_id UUID,
    path TEXT NOT NULL,
    action TEXT NOT NULL,
    content TEXT NOT NULL DEFAULT '',
    -- What the agent started from; NULL when the file did not exist.
    base_content TEXT,
    origin TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    note TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ,
    resolved_by UUID,
    CONSTRAINT agent_memory_proposal_action_check CHECK (action IN ('upsert', 'delete')),
    CONSTRAINT agent_memory_proposal_origin_check CHECK (origin IN ('agent', 'runtime')),
    CONSTRAINT agent_memory_proposal_status_check CHECK (status IN ('pending', 'accepted', 'rejected', 'superseded'))
);
CREATE INDEX IF NOT EXISTS agent_memory_proposal_agent_idx
    ON agent_memory_proposal (agent_id, status, created_at DESC);

-- One write-back report per task: a run reports its memory changes once,
-- right after it finishes, and later replays are refused.
CREATE TABLE IF NOT EXISTS agent_memory_report (
    task_id UUID PRIMARY KEY,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    reported_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
