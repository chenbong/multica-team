#!/usr/bin/env python3
"""Exercise global skill-name migration in a disposable loopback database."""
from pathlib import Path
import json, os, subprocess, uuid
from urllib.parse import urlsplit, urlunsplit

root=Path(__file__).resolve().parents[1]
base=os.environ.get('DATABASE_URL','')
url=urlsplit(base)
if url.hostname not in ('127.0.0.1','localhost') or not url.path.endswith('_test'):
 raise SystemExit('Requires an explicit loopback test DATABASE_URL')
name='skill_global_test_'+uuid.uuid4().hex[:16]
target=urlunsplit(url._replace(path='/'+name))
def sql(query,dsn=target,ok=True):
 result=subprocess.run(['psql',dsn,'-X','-Atq','-v','ON_ERROR_STOP=1'],input=query,text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
 if ok and result.returncode:raise RuntimeError('Migration fixture SQL failed: '+result.stderr[-1000:])
 if not ok and not result.returncode:raise AssertionError('Invalid insert was accepted')
 return result.stdout.strip()
def migration(filename):
 result=subprocess.run(['psql',target,'-X','-q','-v','ON_ERROR_STOP=1','-f',str(root/'migrations'/filename)],stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
 if result.returncode:raise RuntimeError('Migration failed: '+result.stderr[-1500:])
created=False
try:
 sql('CREATE DATABASE '+name,base);created=True
 sql('''CREATE TABLE skill(id uuid,name text,scope text,workspace_id uuid,created_at timestamptz,updated_at timestamptz,content text,config jsonb);
 INSERT INTO skill VALUES
 ('00000000-0000-4000-8000-000000000001','foo','team',NULL,'2026-10-20','2026-10-20','one','{}'),
 ('00000000-0000-4000-8000-000000000002',' FoO ','workspace','11111111-1111-4111-8111-111111111111','2026-10-01','2026-10-01','two','{}'),
 ('00000000-0000-4000-8000-000000000003','foo','personal','11111111-1111-4111-8111-111111111111','2026-09-01','2026-09-01','three','{}'),
 ('00000000-0000-4000-8000-000000000004','foo-2','workspace','11111111-1111-4111-8111-111111111111','2026-01-01','2026-01-01','four','{}'),
 ('00000000-0000-4000-8000-000000000005','FOO-3','team',NULL,'2026-01-01','2026-01-01','five','{}'),
 ('00000000-0000-4000-8000-000000000006',U&'\\00a0bar\\0009','workspace','11111111-1111-4111-8111-111111111111','2026-01-01','2026-01-01','six','{}'),
 ('00000000-0000-4000-8000-000000000007','vov','team',NULL,'2026-01-01','2026-01-01','seven','{}');''')
 sql("CREATE UNIQUE INDEX CONCURRENTLY legacy_workspace_names ON skill(workspace_id,name) WHERE scope='workspace'")
 before=sql("SELECT md5(jsonb_agg(to_jsonb(s) ORDER BY id)::text) FROM skill s")
 body=sql("SELECT md5(jsonb_agg(to_jsonb(s)-'name' ORDER BY id)::text) FROM skill s")
 migration('556_skill_global_name_prepare.up.sql');migration('557_skill_global_name_index.up.sql')
 rows=json.loads(sql('SELECT json_object_agg(id,name) FROM skill'))
 assert rows['00000000-0000-4000-8000-000000000001']=='foo'
 assert rows['00000000-0000-4000-8000-000000000002']=='FoO-4'
 assert rows['00000000-0000-4000-8000-000000000003']=='foo-5'
 assert rows['00000000-0000-4000-8000-000000000006']=='bar'
 assert rows['00000000-0000-4000-8000-000000000007']=='vov'
 assert body==sql("SELECT md5(jsonb_agg(to_jsonb(s)-'name' ORDER BY id)::text) FROM skill s")
 assert sql("SELECT count(*) FROM team_mesh_skill_rename_audit")=='3'
 sql("INSERT INTO skill(id,name,scope) VALUES(gen_random_uuid(),' FOO ','personal')",ok=False)
 sql("INSERT INTO skill(id,name,scope) VALUES(gen_random_uuid(),E' \\t','team')",ok=False)
 migration('556_skill_global_name_prepare.up.sql');migration('557_skill_global_name_index.up.sql')
 assert sql("SELECT count(*) FROM team_mesh_skill_rename_audit")=='3'
 migration('557_skill_global_name_index.down.sql');migration('556_skill_global_name_prepare.down.sql')
 assert before==sql("SELECT md5(jsonb_agg(to_jsonb(s) ORDER BY id)::text) FROM skill s")
 print('Global name migration: priority, reserved suffixes, Unicode trim, concurrency constraint, replay and name rollback passed.')
finally:
 if created:sql('DROP DATABASE '+name+' WITH (FORCE)',base)
