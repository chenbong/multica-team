CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS personal_secret_owner_name_idx ON personal_secret(owner_id,name);
