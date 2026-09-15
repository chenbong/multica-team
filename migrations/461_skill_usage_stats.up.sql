CREATE TABLE IF NOT EXISTS skill_usage_stats (
    skill_id UUID PRIMARY KEY,
    usage_count BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
