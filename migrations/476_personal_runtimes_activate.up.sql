-- Final stage: all ON CONFLICT arbiters must be valid before activation.
-- If a concurrent build failed, IF NOT EXISTS alone cannot repair its INVALID
-- index. Inspect pg_index, DROP INDEX CONCURRENTLY the failed named index,
-- rerun its individual migration, then retry 476. Do not start the new Go
-- handlers until the entire 467-476 manifest succeeds.
--
-- Application lock contract (READ COMMITTED):
-- * If an operation takes workspace/member fences, take them before personal
--   locks. Explicit owner/admin teardown similarly locks its user first.
-- * Lock required personal_runtime_profile rows, then personal_runtime rows,
--   then runtime_profile aliases, then agent_runtime aliases. Each multi-row
--   set is locked ORDER BY id. Skip sets an operation does not need.
-- * Canonical edits/rename MUST take FOR UPDATE before their UPDATE, even for
--   non-key fields, and propagate all aliases before committing. A plain
--   non-key UPDATE only takes NO KEY UPDATE, compatible with the KEY SHARE
--   locks used by materialization, so it is not an equivalent write fence.
-- * Projection INSERTs take canonical (and custom profile alias) KEY SHARE
--   NOWAIT locks. A legacy registration already holding aliases must abort
--   and retry the WHOLE transaction on 40001, releasing advisory/row locks
--   first. Do not retry inside a savepoint while retaining those locks.
-- * Projection UPDATEs deliberately do not take canonical locks. Heartbeats
--   must not add an alias -> canonical blocking lock dependency.
-- * Teardown/legacy merge/GC lock all affected canonical rows before aliases,
--   finish agent/task handling, delete aliases, then delete canonical rows.
--   Canonical delete guards refuse remaining dependents; they never cascade.
--   Profile-alias dependency validation is deferred until COMMIT, so publish
--   success/notifications only after a successful commit.
-- * Identity/owner/daemon/profile links are immutable. Merge via the existing
--   new-identity + reassign + teardown lifecycle, not in-place link rewrites.
--   Legacy merge retires the old canonical with retired_at under FOR UPDATE;
--   materialization excludes it while existing workspace aliases drain.
--   Status or metadata alone is NOT retirement. Workspace-only alias deletion
--   retains canonical rows; legacy per-workspace TTL GC must skip personal
--   aliases. Delete a retired canonical only after its final alias is removed.
-- * INSERT/UPSERT of an existing retired identity raises terminal 55000 (map
--   to a client conflict, not transaction retry). Existing alias heartbeat
--   UPDATEs remain legal while they drain. Once the retired canonical itself
--   is deleted there is no tombstone; permanent denial of that old daemon ID
--   needs retained identity evidence or an application authorization fence.
BEGIN;
SET LOCAL lock_timeout = '5s';
DO $migration$
BEGIN
IF to_regclass('personal_runtime') IS NULL OR to_regclass('personal_runtime_profile') IS NULL THEN
    RAISE EXCEPTION 'personal runtime migration: apply schema migration 467 first';
END IF;
IF EXISTS (
    SELECT 1 FROM unnest(ARRAY['personal_runtime_profile_id_key','personal_runtime_id_key','personal_runtime_profile_owner_name_key','personal_runtime_identity_key','runtime_profile_workspace_owner_name_key','runtime_profile_workspace_personal_key','agent_runtime_personal_idx']) expected(name)
    WHERE NOT EXISTS (
        SELECT 1 FROM pg_index i WHERE i.indexrelid=to_regclass(expected.name)
          AND i.indisvalid AND i.indisready
          AND (expected.name='agent_runtime_personal_idx' OR i.indisunique)
    )
) THEN
    RAISE EXCEPTION 'personal runtime migration: missing or invalid index; complete or repair concurrent migrations 468-474 before activation';
END IF;
-- Re-running an activated migration never imports stale aliases again. This
-- also resumes after a partial rollback: restore 468-474 first, then remove
-- the legacy-only uniqueness that 475.down may have rebuilt.
IF to_regprocedure('team_mesh_personal_identity_guard()') IS NOT NULL THEN
    ALTER TABLE runtime_profile DROP CONSTRAINT IF EXISTS runtime_profile_workspace_id_display_name_key;
    DROP INDEX IF EXISTS runtime_profile_workspace_id_display_name_key;
    RETURN;
END IF;
LOCK TABLE personal_runtime_profile, personal_runtime, runtime_profile, agent_runtime, agent IN ACCESS EXCLUSIVE MODE;
IF EXISTS (SELECT 1 FROM personal_runtime_profile) OR EXISTS (SELECT 1 FROM personal_runtime) THEN
    RAISE EXCEPTION 'personal runtime migration: unexpected canonical data before activation; inspect instead of overwriting it';
END IF;
IF EXISTS (
    SELECT 1 FROM runtime_profile p WHERE p.created_by IS NOT NULL
    AND NOT EXISTS(SELECT 1 FROM "user" u WHERE u.id=p.created_by)
) THEN
    RAISE EXCEPTION 'personal runtime migration: profile owner no longer exists; resolve explicitly before migrating';
END IF;
IF EXISTS (
    SELECT 1 FROM agent_runtime r LEFT JOIN runtime_profile p ON p.id=r.profile_id
    WHERE r.profile_id IS NOT NULL AND
      (p.id IS NULL OR p.workspace_id<>r.workspace_id OR p.created_by IS DISTINCT FROM r.owner_id OR r.owner_id IS NULL)
) OR EXISTS (
    SELECT 1 FROM agent a JOIN agent_runtime r ON r.id=a.runtime_id
    WHERE r.owner_id IS NOT NULL AND r.daemon_id IS NOT NULL AND
      (a.workspace_id<>r.workspace_id OR a.owner_id IS DISTINCT FROM r.owner_id)
) THEN
    RAISE EXCEPTION 'personal runtime migration: cross-owner, cross-workspace or dangling binding; resolve explicitly before migrating';
END IF;
DO $$ BEGIN
  IF EXISTS (
    SELECT 1 FROM runtime_profile WHERE created_by IS NOT NULL
    GROUP BY created_by, display_name
    HAVING count(DISTINCT (protocol_family,runtime_type,command_name,fixed_args,enabled,description)) > 1
  ) THEN
    RAISE EXCEPTION 'personal runtime migration: conflicting profile configurations; resolve explicitly before migrating';
  END IF;
END $$;
INSERT INTO personal_runtime_profile(id,owner_id,display_name,protocol_family,runtime_type,command_name,description,fixed_args,enabled,created_at,updated_at)
SELECT DISTINCT ON (created_by,display_name) id,created_by,display_name,protocol_family,runtime_type,command_name,description,fixed_args,enabled,created_at,updated_at
FROM runtime_profile WHERE created_by IS NOT NULL
ORDER BY created_by,display_name,created_at,id
ON CONFLICT (owner_id,display_name) DO NOTHING;
UPDATE runtime_profile p SET personal_profile_id=c.id, visibility='private'
FROM personal_runtime_profile c WHERE c.owner_id=p.created_by AND c.display_name=p.display_name;
DO $$ BEGIN
  IF EXISTS (
    SELECT 1 FROM agent_runtime r LEFT JOIN runtime_profile p ON p.id=r.profile_id
    WHERE r.owner_id IS NOT NULL AND r.daemon_id IS NOT NULL
    GROUP BY r.owner_id,r.daemon_id,r.provider,p.personal_profile_id
    HAVING count(DISTINCT r.custom_name) > 1
  ) THEN
    RAISE EXCEPTION 'personal runtime migration: conflicting computer names; resolve explicitly before migrating';
  END IF;
END $$;
INSERT INTO personal_runtime(id,owner_id,daemon_id,provider,personal_profile_id,custom_name,name,runtime_mode,device_info,metadata,created_at,updated_at)
SELECT DISTINCT ON (r.owner_id,r.daemon_id,r.provider,p.personal_profile_id)
 r.id,r.owner_id,r.daemon_id,r.provider,p.personal_profile_id,
 (SELECT max(x.custom_name) FROM agent_runtime x LEFT JOIN runtime_profile xp ON xp.id=x.profile_id
  WHERE x.owner_id=r.owner_id AND x.daemon_id=r.daemon_id AND x.provider=r.provider AND xp.personal_profile_id IS NOT DISTINCT FROM p.personal_profile_id),
 r.name,r.runtime_mode,r.device_info,r.metadata,r.created_at,r.updated_at
FROM agent_runtime r LEFT JOIN runtime_profile p ON p.id=r.profile_id
WHERE r.owner_id IS NOT NULL AND r.daemon_id IS NOT NULL
ORDER BY r.owner_id,r.daemon_id,r.provider,p.personal_profile_id,r.created_at,r.id
ON CONFLICT DO NOTHING;
UPDATE agent_runtime r SET personal_runtime_id=c.id,custom_name=c.custom_name,visibility='private'
FROM personal_runtime c WHERE c.owner_id=r.owner_id AND c.daemon_id=r.daemon_id AND c.provider=r.provider
AND c.personal_profile_id IS NOT DISTINCT FROM (SELECT personal_profile_id FROM runtime_profile WHERE id=r.profile_id);
ALTER TABLE runtime_profile DROP CONSTRAINT IF EXISTS runtime_profile_workspace_id_display_name_key;
-- Also covers a standalone index left by a partially completed rollback.
DROP INDEX IF EXISTS runtime_profile_workspace_id_display_name_key;
ALTER TABLE runtime_profile ADD CONSTRAINT runtime_profile_personal_presence_check
    CHECK ((personal_profile_id IS NULL) = (created_by IS NULL));
ALTER TABLE agent_runtime ADD CONSTRAINT agent_runtime_personal_presence_check
    CHECK ((personal_runtime_id IS NOT NULL) = (owner_id IS NOT NULL AND daemon_id IS NOT NULL));
ALTER TABLE agent_runtime ADD CONSTRAINT agent_runtime_profile_owner_check
    CHECK (profile_id IS NULL OR owner_id IS NOT NULL);

-- No new FKs or cascades. Account/admin cleanup is an application transaction:
-- lock the user first, then profiles, runtimes and aliases; explicitly tear
-- down dependents before deleting canonical rows and finally the user.
CREATE OR REPLACE FUNCTION team_mesh_personal_identity_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='UPDATE' THEN
    IF NEW.id IS DISTINCT FROM OLD.id OR NEW.owner_id IS DISTINCT FROM OLD.owner_id THEN
      RAISE EXCEPTION 'personal runtime ownership and identity are immutable' USING ERRCODE='23514';
    END IF;
    IF TG_TABLE_NAME='personal_runtime_profile' THEN
      IF NEW.protocol_family IS DISTINCT FROM OLD.protocol_family OR NEW.runtime_type IS DISTINCT FROM OLD.runtime_type THEN
        RAISE EXCEPTION 'personal runtime protocol is immutable' USING ERRCODE='23514';
      END IF;
    ELSE
      IF NEW.daemon_id IS DISTINCT FROM OLD.daemon_id OR NEW.provider IS DISTINCT FROM OLD.provider OR
         NEW.personal_profile_id IS DISTINCT FROM OLD.personal_profile_id THEN
        RAISE EXCEPTION 'personal runtime identity is immutable' USING ERRCODE='23514';
      END IF;
    END IF;
    -- The identity did not change. Taking parent locks from an existing
    -- canonical runtime UPDATE would invert profile -> runtime ordering.
    RETURN NEW;
  END IF;
  PERFORM id FROM "user" WHERE id=NEW.owner_id FOR KEY SHARE NOWAIT;
  IF NOT FOUND THEN RAISE EXCEPTION 'personal runtime owner does not exist' USING ERRCODE='23514'; END IF;
  IF TG_TABLE_NAME='personal_runtime' THEN
    IF NEW.personal_profile_id IS NOT NULL THEN
      PERFORM id FROM personal_runtime_profile WHERE id=NEW.personal_profile_id AND owner_id=NEW.owner_id FOR KEY SHARE NOWAIT;
      IF NOT FOUND THEN RAISE EXCEPTION 'runtime profile belongs to another user or no longer exists' USING ERRCODE='23514'; END IF;
    END IF;
  END IF;
  RETURN NEW;
EXCEPTION WHEN lock_not_available THEN
  RAISE EXCEPTION 'personal runtime changed concurrently; retry the transaction' USING ERRCODE='40001';
END $$;
CREATE TRIGGER team_mesh_personal_profile_identity BEFORE INSERT OR UPDATE ON personal_runtime_profile
    FOR EACH ROW EXECUTE FUNCTION team_mesh_personal_identity_guard();
CREATE TRIGGER team_mesh_personal_runtime_identity BEFORE INSERT OR UPDATE ON personal_runtime
    FOR EACH ROW EXECUTE FUNCTION team_mesh_personal_identity_guard();

-- Relationship guards refuse a delete; they never clean up dependent data.
-- INSERT projections hold canonical KEY SHARE until commit. Canonical DELETE
-- owns the conflicting row lock before these fresh dependency reads.
CREATE OR REPLACE FUNCTION team_mesh_personal_delete_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_TABLE_NAME='personal_runtime_profile' THEN
    IF EXISTS(SELECT 1 FROM runtime_profile WHERE personal_profile_id=OLD.id) OR
       EXISTS(SELECT 1 FROM personal_runtime WHERE personal_profile_id=OLD.id) THEN
      RAISE EXCEPTION 'personal profile still has projections or runtimes; explicit teardown required' USING ERRCODE='23514';
    END IF;
  ELSIF TG_TABLE_NAME='personal_runtime' THEN
    IF EXISTS(SELECT 1 FROM agent_runtime WHERE personal_runtime_id=OLD.id) THEN
      RAISE EXCEPTION 'personal runtime still has projections; explicit teardown required' USING ERRCODE='23514';
    END IF;
  ELSE
    IF EXISTS(SELECT 1 FROM agent_runtime WHERE profile_id=OLD.id) THEN
      RAISE EXCEPTION 'profile projection still has runtimes; explicit teardown required' USING ERRCODE='23514';
    END IF;
  END IF;
  RETURN OLD;
END $$;
CREATE TRIGGER team_mesh_personal_profile_delete BEFORE DELETE ON personal_runtime_profile
    FOR EACH ROW EXECUTE FUNCTION team_mesh_personal_delete_guard();
CREATE TRIGGER team_mesh_personal_runtime_delete BEFORE DELETE ON personal_runtime
    FOR EACH ROW EXECUTE FUNCTION team_mesh_personal_delete_guard();
-- Upstream workspace cleanup deletes runtimes and profiles in sibling CTEs,
-- whose execution order is unspecified. Validate the final transaction state
-- so that cleanup remains legal without permitting a dangling relationship.
CREATE CONSTRAINT TRIGGER team_mesh_profile_alias_delete AFTER DELETE ON runtime_profile
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION team_mesh_personal_delete_guard();

-- Legacy registration inserts receive the canonical definition. Edits must
-- go through the personal row first; projections cannot diverge.
CREATE OR REPLACE FUNCTION team_mesh_profile_projection() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE c personal_runtime_profile;
BEGIN
  IF TG_OP='UPDATE' THEN
    IF NEW.id IS DISTINCT FROM OLD.id OR NEW.workspace_id IS DISTINCT FROM OLD.workspace_id OR
       NEW.created_by IS DISTINCT FROM OLD.created_by OR NEW.personal_profile_id IS DISTINCT FROM OLD.personal_profile_id THEN
      RAISE EXCEPTION 'personal runtime profile ownership and identity are immutable' USING ERRCODE='23514';
    END IF;
    IF OLD.personal_profile_id IS NULL THEN RETURN NEW; END IF;
    -- UPDATE already owns an alias lock. Never acquire a canonical lock here:
    -- canonical edits/deletes lock in the opposite order. Their propagation
    -- updates this alias after this transaction releases it.
    SELECT * INTO STRICT c FROM personal_runtime_profile WHERE id=OLD.personal_profile_id;
  ELSE
    IF NEW.created_by IS NULL THEN
      IF NEW.personal_profile_id IS NOT NULL THEN
        RAISE EXCEPTION 'personal runtime profile requires an owner' USING ERRCODE='23514';
      END IF;
      RETURN NEW;
    END IF;
    IF NEW.personal_profile_id IS NOT NULL THEN
      SELECT * INTO STRICT c FROM personal_runtime_profile
      WHERE id=NEW.personal_profile_id AND owner_id=NEW.created_by FOR KEY SHARE NOWAIT;
    ELSE
      SELECT * INTO c FROM personal_runtime_profile
      WHERE owner_id=NEW.created_by AND display_name=NEW.display_name FOR KEY SHARE NOWAIT;
      IF NOT FOUND THEN
        INSERT INTO personal_runtime_profile(owner_id,display_name,protocol_family,runtime_type,command_name,description,fixed_args,enabled)
        VALUES(NEW.created_by,NEW.display_name,NEW.protocol_family,NEW.runtime_type,NEW.command_name,NEW.description,NEW.fixed_args,NEW.enabled)
        ON CONFLICT(owner_id,display_name) DO NOTHING;
        SELECT * INTO STRICT c FROM personal_runtime_profile
        WHERE owner_id=NEW.created_by AND display_name=NEW.display_name FOR KEY SHARE NOWAIT;
      END IF;
    END IF;
    NEW.personal_profile_id=c.id;
  END IF;
  NEW.display_name=c.display_name; NEW.protocol_family=c.protocol_family; NEW.runtime_type=c.runtime_type;
  NEW.command_name=c.command_name; NEW.description=c.description; NEW.fixed_args=c.fixed_args;
  NEW.enabled=c.enabled; NEW.visibility='private';
  RETURN NEW;
EXCEPTION WHEN lock_not_available THEN
  -- Legacy registration can enter with an alias lock already held. Fail the
  -- whole transaction for retry instead of introducing a lock-order cycle.
  RAISE EXCEPTION 'personal runtime changed concurrently; retry the transaction' USING ERRCODE='40001';
END $$;
DROP TRIGGER IF EXISTS team_mesh_profile_projection ON runtime_profile;
CREATE TRIGGER team_mesh_profile_projection BEFORE INSERT OR UPDATE ON runtime_profile FOR EACH ROW EXECUTE FUNCTION team_mesh_profile_projection();

CREATE OR REPLACE FUNCTION team_mesh_runtime_projection() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE c personal_runtime; profile_key uuid;
BEGIN
  IF TG_OP='UPDATE' THEN
    IF ROW(NEW.id,NEW.workspace_id,NEW.owner_id,NEW.daemon_id,NEW.provider,NEW.profile_id,NEW.personal_runtime_id)
       IS DISTINCT FROM ROW(OLD.id,OLD.workspace_id,OLD.owner_id,OLD.daemon_id,OLD.provider,OLD.profile_id,OLD.personal_runtime_id) THEN
      RAISE EXCEPTION 'personal runtime ownership and identity are immutable' USING ERRCODE='23514';
    END IF;
    -- Legacy/cloud rows without a personal identity may still receive status
    -- updates, but cannot be silently adopted by another owner/daemon. That
    -- would invalidate existing agent bindings without firing their guard.
    IF OLD.personal_runtime_id IS NULL THEN RETURN NEW; END IF;
    SELECT * INTO STRICT c FROM personal_runtime WHERE id=OLD.personal_runtime_id;
    NEW.custom_name=c.custom_name; NEW.visibility='private';
    RETURN NEW;
  END IF;
  IF NEW.owner_id IS NULL OR NEW.daemon_id IS NULL THEN
    IF NEW.personal_runtime_id IS NOT NULL THEN
      RAISE EXCEPTION 'personal runtime requires owner and daemon' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
  END IF;
  IF NEW.profile_id IS NOT NULL THEN
    SELECT personal_profile_id INTO STRICT profile_key FROM runtime_profile
    WHERE id=NEW.profile_id AND created_by=NEW.owner_id AND workspace_id=NEW.workspace_id;
    -- The legacy Go registration path holds the profile ALIAS first. NOWAIT
    -- is essential until it adopts canonical-profile -> canonical-runtime ->
    -- alias ordering (and retries SQLSTATE 40001).
    PERFORM id FROM personal_runtime_profile WHERE id=profile_key FOR KEY SHARE NOWAIT;
    IF NOT FOUND THEN RAISE EXCEPTION 'personal runtime profile no longer exists' USING ERRCODE='23503'; END IF;
  END IF;
  IF NEW.personal_runtime_id IS NOT NULL THEN
    SELECT * INTO STRICT c FROM personal_runtime WHERE id=NEW.personal_runtime_id
      AND owner_id=NEW.owner_id AND daemon_id=NEW.daemon_id AND provider=NEW.provider
      AND personal_profile_id IS NOT DISTINCT FROM profile_key FOR KEY SHARE NOWAIT;
  ELSE
    SELECT * INTO c FROM personal_runtime WHERE owner_id=NEW.owner_id AND daemon_id=NEW.daemon_id AND provider=NEW.provider
      AND personal_profile_id IS NOT DISTINCT FROM profile_key FOR KEY SHARE NOWAIT;
    IF NOT FOUND THEN
      INSERT INTO personal_runtime(owner_id,daemon_id,provider,personal_profile_id,custom_name,name,runtime_mode,device_info,metadata)
      VALUES(NEW.owner_id,NEW.daemon_id,NEW.provider,profile_key,NEW.custom_name,NEW.name,NEW.runtime_mode,NEW.device_info,NEW.metadata)
      ON CONFLICT DO NOTHING;
      SELECT * INTO STRICT c FROM personal_runtime WHERE owner_id=NEW.owner_id AND daemon_id=NEW.daemon_id AND provider=NEW.provider
        AND personal_profile_id IS NOT DISTINCT FROM profile_key FOR KEY SHARE NOWAIT;
    END IF;
  END IF;
  -- Even an ON CONFLICT upsert executes BEFORE INSERT. Do not allow an old
  -- daemon to renew or create a registration for a retired canonical; only
  -- UPDATEs of existing aliases remain available for their drain lifecycle.
  IF c.retired_at IS NOT NULL THEN
    RAISE EXCEPTION 'personal runtime identity is retired' USING ERRCODE='55000';
  END IF;
  -- Protect the alias relationship too, after canonical locks, without
  -- reintroducing an alias -> canonical wait cycle.
  IF NEW.profile_id IS NOT NULL THEN
    PERFORM id FROM runtime_profile WHERE id=NEW.profile_id AND workspace_id=NEW.workspace_id
      AND created_by=NEW.owner_id AND personal_profile_id=profile_key FOR KEY SHARE NOWAIT;
    IF NOT FOUND THEN RAISE EXCEPTION 'runtime profile projection no longer exists' USING ERRCODE='23514'; END IF;
  END IF;
  NEW.personal_runtime_id=c.id; NEW.custom_name=c.custom_name; NEW.visibility='private';
  RETURN NEW;
EXCEPTION WHEN lock_not_available THEN
  RAISE EXCEPTION 'personal runtime changed concurrently; retry the transaction' USING ERRCODE='40001';
END $$;
DROP TRIGGER IF EXISTS team_mesh_runtime_projection ON agent_runtime;
CREATE TRIGGER team_mesh_runtime_projection BEFORE INSERT OR UPDATE ON agent_runtime FOR EACH ROW EXECUTE FUNCTION team_mesh_runtime_projection();

CREATE OR REPLACE FUNCTION team_mesh_agent_runtime_owner() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE r agent_runtime;
BEGIN
  IF NEW.runtime_id IS NULL THEN RETURN NEW; END IF;
  SELECT * INTO r FROM agent_runtime WHERE id=NEW.runtime_id;
  IF r.personal_runtime_id IS NOT NULL AND
     (NEW.owner_id IS DISTINCT FROM r.owner_id OR NEW.workspace_id IS DISTINCT FROM r.workspace_id) THEN
    RAISE EXCEPTION 'personal runtime binding requires the same owner and workspace' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER team_mesh_agent_runtime_owner BEFORE INSERT OR UPDATE OF runtime_id,owner_id,workspace_id ON agent
    FOR EACH ROW EXECUTE FUNCTION team_mesh_agent_runtime_owner();

-- Called only after application membership/ownership checks. Newly visible
-- registrations stay offline until a daemon actually acknowledges that scope.
CREATE OR REPLACE FUNCTION team_mesh_ensure_runtime_workspace(p_owner uuid,p_workspace uuid) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
  -- Workspace first, matching its deletion/task-write fence; hold membership
  -- through materialization so removal cannot race the authorization check.
  PERFORM id FROM workspace WHERE id=p_workspace FOR KEY SHARE;
  PERFORM id FROM member WHERE user_id=p_owner AND workspace_id=p_workspace FOR KEY SHARE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'workspace membership required';
  END IF;
  PERFORM id FROM personal_runtime_profile WHERE owner_id=p_owner ORDER BY id FOR KEY SHARE;
  PERFORM id FROM personal_runtime WHERE owner_id=p_owner AND retired_at IS NULL ORDER BY id FOR KEY SHARE;
  INSERT INTO runtime_profile(workspace_id,display_name,protocol_family,runtime_type,command_name,description,fixed_args,visibility,created_by,enabled,personal_profile_id)
  SELECT p_workspace,display_name,protocol_family,runtime_type,command_name,description,fixed_args,'private',owner_id,enabled,id
  FROM personal_runtime_profile WHERE owner_id=p_owner
  ORDER BY id
  ON CONFLICT(workspace_id,personal_profile_id) WHERE personal_profile_id IS NOT NULL DO NOTHING;
  INSERT INTO agent_runtime(workspace_id,daemon_id,name,runtime_mode,provider,status,device_info,metadata,owner_id,profile_id,custom_name,visibility,personal_runtime_id)
  SELECT p_workspace,c.daemon_id,c.name,c.runtime_mode,c.provider,'offline',coalesce(c.device_info,''),c.metadata,c.owner_id,p.id,c.custom_name,'private',c.id
  FROM personal_runtime c LEFT JOIN runtime_profile p ON p.personal_profile_id=c.personal_profile_id AND p.workspace_id=p_workspace
  WHERE c.owner_id=p_owner AND c.retired_at IS NULL AND (c.personal_profile_id IS NULL OR p.id IS NOT NULL)
  ORDER BY c.id
  ON CONFLICT DO NOTHING;
END $$;

-- The first rollback stage validates representability before any index is
-- removed. The final schema stage repeats it under locks. Quiesce application
-- writers for the complete multi-file rollback; never resume the old server
-- until 467.down completes. On an index-build failure, canonical data and
-- triggers remain in place; repair/rebuild the index before continuing.
CREATE OR REPLACE FUNCTION team_mesh_prepare_personal_runtime_rollback() RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 LOCK TABLE personal_runtime_profile, personal_runtime, runtime_profile, agent_runtime, agent IN ACCESS EXCLUSIVE MODE;
 IF EXISTS(SELECT 1 FROM personal_runtime WHERE retired_at IS NOT NULL) THEN
   RAISE EXCEPTION 'personal runtime rollback: retired identities still present; finish explicit merge/teardown before rollback; no data has been removed';
 END IF;
 IF EXISTS(SELECT 1 FROM personal_runtime_profile c WHERE NOT EXISTS(SELECT 1 FROM runtime_profile p WHERE p.personal_profile_id=c.id))
    OR EXISTS(SELECT 1 FROM personal_runtime c WHERE NOT EXISTS(SELECT 1 FROM agent_runtime r WHERE r.personal_runtime_id=c.id)) THEN
   RAISE EXCEPTION 'personal runtime rollback: unprojected personal configuration; materialize or explicitly resolve it before rollback; no data has been removed';
 END IF;
 UPDATE runtime_profile p SET display_name=c.display_name,protocol_family=c.protocol_family,
   runtime_type=c.runtime_type,command_name=c.command_name,description=c.description,
   fixed_args=c.fixed_args,enabled=c.enabled,updated_at=c.updated_at
 FROM personal_runtime_profile c WHERE p.personal_profile_id=c.id;
 -- These are the only authoritative user-editable runtime fields. Per-alias
 -- status, timestamps, daemon metadata and discovery names must stay intact.
 UPDATE agent_runtime r SET custom_name=c.custom_name FROM personal_runtime c WHERE r.personal_runtime_id=c.id;
 IF EXISTS(SELECT 1 FROM runtime_profile GROUP BY workspace_id,display_name HAVING count(*)>1) THEN
  RAISE EXCEPTION 'rollback needs explicit resolution of equal profile names across users; no data has been removed';
 END IF;

END $$;
END $migration$;
COMMIT;
