CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS infoflow_conversation_notice_event_idx ON infoflow_conversation_notice(binding_id,generation,kind);
