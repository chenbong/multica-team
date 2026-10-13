-- Agent memory files (team migrations 467_agent_memory_file, 468_agent_memory_writeback).

-- name: ListAgentMemoryFiles :many
SELECT id, agent_id, path, content, source, source_runtime_id, source_path, enabled, updated_by, created_at, updated_at, auto_sync, sync_owner_id, synced_at
FROM agent_memory_file
WHERE agent_id = $1
ORDER BY path ASC;

-- name: UpsertAgentMemoryFile :one
INSERT INTO agent_memory_file (agent_id, path, content, source, source_runtime_id, source_path, enabled, updated_by, auto_sync, sync_owner_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (agent_id, path) DO UPDATE SET
    content = EXCLUDED.content,
    source = EXCLUDED.source,
    source_runtime_id = EXCLUDED.source_runtime_id,
    source_path = EXCLUDED.source_path,
    enabled = EXCLUDED.enabled,
    auto_sync = EXCLUDED.auto_sync,
    sync_owner_id = CASE
        WHEN NOT EXCLUDED.auto_sync THEN NULL
        ELSE COALESCE(EXCLUDED.sync_owner_id, agent_memory_file.sync_owner_id)
    END,
    updated_by = CASE
        WHEN agent_memory_file.content IS DISTINCT FROM EXCLUDED.content
            OR agent_memory_file.enabled IS DISTINCT FROM EXCLUDED.enabled THEN EXCLUDED.updated_by
        ELSE agent_memory_file.updated_by
    END,
    updated_at = CASE
        WHEN agent_memory_file.content IS DISTINCT FROM EXCLUDED.content
            OR agent_memory_file.enabled IS DISTINCT FROM EXCLUDED.enabled THEN now()
        ELSE agent_memory_file.updated_at
    END
RETURNING id, agent_id, path, content, source, source_runtime_id, source_path, enabled, updated_by, created_at, updated_at, auto_sync, sync_owner_id, synced_at;

-- name: DeleteAgentMemoryFilesExcept :exec
DELETE FROM agent_memory_file
WHERE agent_id = $1
  AND NOT (path = ANY(@keep_paths::text[]));
