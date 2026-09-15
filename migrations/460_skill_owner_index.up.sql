CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_skill_owner ON skill(owner_user_id) WHERE scope='personal';
