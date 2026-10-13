-- Agent memory write-back and source sync (team migration 468_agent_memory_writeback).

-- name: GetAgentMemorySetting :one
SELECT agent_id, writeback, capture_runtime_memory, updated_by, updated_at
FROM agent_memory_setting
WHERE agent_id = $1;

-- name: UpsertAgentMemorySetting :one
INSERT INTO agent_memory_setting (agent_id, writeback, capture_runtime_memory, updated_by)
VALUES ($1, $2, $3, $4)
ON CONFLICT (agent_id) DO UPDATE SET
    writeback = EXCLUDED.writeback,
    capture_runtime_memory = EXCLUDED.capture_runtime_memory,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING agent_id, writeback, capture_runtime_memory, updated_by, updated_at;

-- name: SupersedePendingAgentMemoryProposals :exec
UPDATE agent_memory_proposal
SET status = 'superseded', resolved_at = now()
WHERE agent_id = $1 AND path = $2 AND status = 'pending';

-- name: CreateAgentMemoryProposal :one
INSERT INTO agent_memory_proposal (agent_id, task_id, path, action, content, base_content, origin)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, agent_id, task_id, path, action, content, base_content, origin, status, note, created_at, resolved_at, resolved_by;

-- name: ListAgentMemoryProposals :many
SELECT id, agent_id, task_id, path, action, content, base_content, origin, status, note, created_at, resolved_at, resolved_by
FROM agent_memory_proposal
WHERE agent_id = $1 AND (@status::text = '' OR status = @status::text)
ORDER BY created_at DESC
LIMIT 100;

-- name: GetAgentMemoryProposalForUpdate :one
SELECT id, agent_id, task_id, path, action, content, base_content, origin, status, note, created_at, resolved_at, resolved_by
FROM agent_memory_proposal
WHERE id = $1 AND agent_id = $2
FOR UPDATE;

-- name: ResolveAgentMemoryProposal :one
UPDATE agent_memory_proposal
SET status = $2, note = $3, resolved_at = now(), resolved_by = $4
WHERE id = $1
RETURNING id, agent_id, task_id, path, action, content, base_content, origin, status, note, created_at, resolved_at, resolved_by;

-- name: SetAgentMemoryProposalNote :exec
UPDATE agent_memory_proposal SET note = $2 WHERE id = $1;

-- name: CountPendingAgentMemoryProposals :one
SELECT count(*) FROM agent_memory_proposal WHERE agent_id = $1 AND status = 'pending';

-- name: DeleteAgentMemoryFile :exec
DELETE FROM agent_memory_file WHERE agent_id = $1 AND path = $2;

-- name: ListAgentMemorySyncKeys :many
SELECT DISTINCT f.source_path
FROM agent_memory_file f
JOIN agent_runtime r ON r.id = f.source_runtime_id
WHERE f.auto_sync AND f.source_runtime_id = $1 AND f.source_path <> ''
  AND f.sync_owner_id IS NOT NULL AND r.owner_id = f.sync_owner_id
ORDER BY f.source_path
LIMIT 32;

-- name: ApplyAgentMemorySync :many
UPDATE agent_memory_file f
SET content = @content::text,
    updated_at = CASE WHEN f.content IS DISTINCT FROM @content::text THEN now() ELSE f.updated_at END,
    synced_at = now()
FROM agent_runtime r
WHERE r.id = f.source_runtime_id
  AND f.auto_sync AND f.source_runtime_id = @runtime_id AND f.source_path = @source_path::text
  AND f.sync_owner_id IS NOT NULL AND r.owner_id = f.sync_owner_id
RETURNING f.agent_id, f.path;
