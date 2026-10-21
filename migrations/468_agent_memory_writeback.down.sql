DROP TABLE IF EXISTS agent_memory_report;
DROP TABLE IF EXISTS agent_memory_proposal;
DROP TABLE IF EXISTS agent_memory_setting;
DELETE FROM agent_memory_file WHERE source = 'agent';
ALTER TABLE agent_memory_file DROP CONSTRAINT IF EXISTS agent_memory_file_source_check;
ALTER TABLE agent_memory_file ADD CONSTRAINT agent_memory_file_source_check
    CHECK (source IN ('manual', 'upload', 'runtime'));
