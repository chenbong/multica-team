CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS attachment_delete_identity_idx ON attachment_object_delete(storage_key);
