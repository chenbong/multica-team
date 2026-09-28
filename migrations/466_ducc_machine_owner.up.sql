CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS ducc_machine_owner_idx ON ducc_machine(user_id, daemon_id);
