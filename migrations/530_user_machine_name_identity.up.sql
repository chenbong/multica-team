CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS user_machine_name_identity_idx ON user_machine_name (user_id, machine_key);
