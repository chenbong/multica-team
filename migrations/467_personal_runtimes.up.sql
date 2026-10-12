-- Stage 1/10. No inline PK/UNIQUE declarations: their indexes are built
-- CONCURRENTLY in 468-474. Activate the feature only after 476 commits.
-- An earlier, unsplit draft of 467 must be rolled back/recreated first.
BEGIN;
SET LOCAL lock_timeout = '5s';
DO $migration$
BEGIN
 IF to_regclass('personal_runtime') IS NOT NULL AND
    obj_description(to_regclass('personal_runtime'),'pg_class') IS DISTINCT FROM 'team-mesh staged personal runtime schema 467-476' THEN
   RAISE EXCEPTION 'personal runtime migration: earlier unsplit schema; restore the disposable database or use its original rollback before applying 467-476';
 END IF;
END $migration$;
CREATE TABLE IF NOT EXISTS personal_runtime_profile (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    owner_id uuid NOT NULL,
    display_name text NOT NULL,
    protocol_family text NOT NULL,
    runtime_type text NOT NULL,
    command_name text NOT NULL,
    description text,
    fixed_args jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(fixed_args)='array'),
    enabled boolean NOT NULL DEFAULT true,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision>0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS personal_runtime (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    owner_id uuid NOT NULL,
    daemon_id text NOT NULL,
    provider text NOT NULL,
    personal_profile_id uuid,
    custom_name text,
    retired_at timestamptz,
    name text NOT NULL,
    runtime_mode text NOT NULL CHECK (runtime_mode IN ('local','cloud')),
    device_info text NOT NULL DEFAULT '',
    metadata jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE personal_runtime IS 'team-mesh staged personal runtime schema 467-476';
ALTER TABLE personal_runtime ADD COLUMN IF NOT EXISTS retired_at timestamptz;
ALTER TABLE runtime_profile ADD COLUMN IF NOT EXISTS personal_profile_id uuid;
ALTER TABLE agent_runtime ADD COLUMN IF NOT EXISTS personal_runtime_id uuid;
COMMIT;
