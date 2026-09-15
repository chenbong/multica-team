CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_skill_personal_owner_name ON skill(owner_user_id,name) WHERE scope='personal';
