-- Container deployment default, matching normal user-agent creation.
-- Add only absent keys on the two known product-defined agent identities.
-- Explicit values (including 0/false/empty/null) and other keys are retained.
-- JSON null / SQL NULL maps are treated as an absent environment map.
UPDATE agent
SET custom_env = COALESCE(NULLIF(custom_env, 'null'::jsonb), '{}'::jsonb)
                 || '{"IS_SANDBOX":"1"}'::jsonb,
    updated_at = now()
WHERE ((kind = 'user' AND system_key = 'mika')
       OR (kind = 'system' AND system_key LIKE 'agent_builder:%'))
  AND (custom_env IS NULL OR custom_env = 'null'::jsonb
       OR (jsonb_typeof(custom_env) = 'object'
           AND NOT (custom_env ? 'IS_SANDBOX')));
