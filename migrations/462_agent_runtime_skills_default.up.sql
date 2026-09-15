-- Preserve the existing opt-in behavior for agents created before this
-- feature. User-authored agents created by CreateAgent explicitly set the
-- column to FALSE; the default remains TRUE for compatibility with rows and
-- creation paths that predate the feature.
ALTER TABLE agent
    ADD COLUMN IF NOT EXISTS runtime_skills_default_enabled BOOLEAN NOT NULL DEFAULT TRUE;
