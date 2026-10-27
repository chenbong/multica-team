CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS agent_owner_read_token_idx ON agent_token_read_scope(token_hash);
