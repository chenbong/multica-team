-- Transfer history is operational audit data, not a user-facing history feed.
-- No cascading foreign keys: retain attribution if an account/skill is deleted.
CREATE TABLE IF NOT EXISTS team_mesh_skill_transfer_audit (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 skill_id uuid NOT NULL,
 skill_name text NOT NULL,
 from_user_id uuid NOT NULL,
 to_user_id uuid NOT NULL,
 transferred_at timestamptz NOT NULL DEFAULT now(),
 CHECK (from_user_id <> to_user_id)
);

-- Operations may apply overlays as a maintenance role, while the API uses
-- the role that owns the existing Skill table. Match that ownership rather
-- than hard-coding a deployment role or granting access to PUBLIC.
DO $$
DECLARE skill_owner text;
BEGIN
 SELECT pg_get_userbyid(relowner) INTO skill_owner
 FROM pg_class WHERE oid='skill'::regclass;
 EXECUTE format('ALTER TABLE team_mesh_skill_transfer_audit OWNER TO %I',skill_owner);
END
$$;
