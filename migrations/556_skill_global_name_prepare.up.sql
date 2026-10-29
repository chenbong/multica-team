BEGIN;
CREATE OR REPLACE FUNCTION team_mesh_skill_name_trim(value text) RETURNS text
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE AS $$
 SELECT btrim(value, E' \t\n\r\f\013' || U&'\0085\00A0\1680\2000\2001\2002\2003\2004\2005\2006\2007\2008\2009\200A\2028\2029\202F\205F\3000');
$$;
CREATE OR REPLACE FUNCTION team_mesh_skill_name_key(value text) RETURNS text
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE AS $$ SELECT lower(public.team_mesh_skill_name_trim(value)); $$;
CREATE TABLE IF NOT EXISTS team_mesh_skill_rename_audit (
 skill_id uuid NOT NULL, old_name text NOT NULL, new_name text NOT NULL,
 migrated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
LOCK TABLE skill IN SHARE ROW EXCLUSIVE MODE;
CREATE TEMP TABLE skill_name_plan (skill_id uuid,old_name text,new_name text,temporary_name text) ON COMMIT DROP;
CREATE TEMP TABLE skill_name_reserved ON COMMIT DROP AS SELECT DISTINCT team_mesh_skill_name_key(name) AS key FROM skill;
DO $$
DECLARE item record; base text; candidate text; suffix integer; temporary text;
BEGIN
 FOR item IN SELECT s.*,row_number() OVER(PARTITION BY team_mesh_skill_name_key(s.name)
 ORDER BY CASE s.scope WHEN 'team' THEN 0 WHEN 'workspace' THEN 1 ELSE 2 END,s.created_at,s.id) AS ordinal
 FROM skill s ORDER BY CASE s.scope WHEN 'team' THEN 0 WHEN 'workspace' THEN 1 ELSE 2 END,s.created_at,s.id LOOP
  base:=team_mesh_skill_name_trim(item.name);
  IF item.ordinal=1 AND base<>'' THEN candidate:=base;
  ELSE
   IF base='' THEN base:='skill'; END IF;
   suffix:=2;
   LOOP
    candidate:=left(base,128-length(suffix::text)-1)||'-'||suffix::text;
    EXIT WHEN NOT EXISTS(SELECT 1 FROM skill_name_reserved WHERE key=team_mesh_skill_name_key(candidate));
    suffix:=suffix+1;
   END LOOP;
  END IF;
  temporary:='tmesh-migration-'||replace(gen_random_uuid()::text,'-','');
  WHILE EXISTS(SELECT 1 FROM skill_name_reserved WHERE key=team_mesh_skill_name_key(temporary)) LOOP
   temporary:='tmesh-migration-'||replace(gen_random_uuid()::text,'-','');
  END LOOP;
  INSERT INTO skill_name_reserved VALUES(team_mesh_skill_name_key(candidate)),(team_mesh_skill_name_key(temporary));
  INSERT INTO skill_name_plan VALUES(item.id,item.name,candidate,temporary);
 END LOOP;
 INSERT INTO team_mesh_skill_rename_audit(skill_id,old_name,new_name)
 SELECT p.skill_id,p.old_name,p.new_name FROM skill_name_plan p WHERE p.old_name<>p.new_name
 AND NOT EXISTS(SELECT 1 FROM team_mesh_skill_rename_audit a WHERE a.skill_id=p.skill_id AND a.old_name=p.old_name AND a.new_name=p.new_name);
 -- Move every changed row aside first so retained scope indexes cannot
 -- observe a transient collision while whitespace variants are normalized.
 UPDATE skill s SET name=p.temporary_name FROM skill_name_plan p WHERE s.id=p.skill_id AND p.old_name<>p.new_name;
 UPDATE skill s SET name=p.new_name FROM skill_name_plan p WHERE s.id=p.skill_id AND p.old_name<>p.new_name;
END $$;
CREATE OR REPLACE FUNCTION team_mesh_skill_name_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 NEW.name:=public.team_mesh_skill_name_trim(NEW.name);
 IF NEW.name='' THEN RAISE EXCEPTION 'skill name is required' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS team_mesh_skill_name_guard ON skill;
CREATE TRIGGER team_mesh_skill_name_guard BEFORE INSERT OR UPDATE OF name ON skill
FOR EACH ROW EXECUTE FUNCTION team_mesh_skill_name_guard();
COMMIT;
