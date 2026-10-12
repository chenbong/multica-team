// Real authenticated HTTP contract test against an isolated, loopback-only API.
// Never run against production. Tokens are generated in memory and never logged.
import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {randomUUID,randomBytes,createHash} from 'node:crypto';
const dbUrl=new URL(process.env.PERSONAL_RUNTIME_TEST_DATABASE_URL);
const apiUrl=new URL(process.env.PERSONAL_RUNTIME_TEST_API_URL);
assert.equal(dbUrl.hostname,'127.0.0.1');assert.equal(apiUrl.hostname,'127.0.0.1');
assert.equal(dbUrl.pathname,'/runtime_scope_http_test');
const {Client}=createRequire(`${process.env.TEST_NODE_PROJECT}/package.json`)('pg');
const db=new Client({connectionString:dbUrl.toString()});await db.connect();
const users=[];const spaces=[];const agents=[];let checks=0;
async function user(label){
 const id=randomUUID(),token='mul_'+randomBytes(20).toString('hex');
 await db.query('INSERT INTO "user"(id,name,email,onboarded_at) VALUES($1,$2,$3,now())',[id,label,`${id}@example.test`]);
 await db.query('INSERT INTO personal_access_token(user_id,name,token_hash,token_prefix) VALUES($1,$2,$3,$4)',[id,'isolated-runtime-test',createHash('sha256').update(token).digest('hex'),token.slice(0,12)]);
 const u={id,token};users.push(u);return u;
}
async function workspace(label,members){
 const id=randomUUID();await db.query('INSERT INTO workspace(id,name,slug,issue_prefix) VALUES($1,$2,$3,$4)',[id,label,'runtime-test-'+id,'RT']);spaces.push(id);
 for(const [u,role] of members)await db.query('INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,$3)',[id,u.id,role]);return id;
}
async function call(u,ws,method,path,body,expected=200){
 const res=await fetch(new URL(path,apiUrl),{method,headers:{authorization:`Bearer ${u.token}`,'content-type':'application/json','x-workspace-id':ws},body:body===undefined?undefined:JSON.stringify(body)});
 const raw=await res.text();let data;try{data=JSON.parse(raw)}catch{data={}}
 assert.ok((Array.isArray(expected)?expected:[expected]).includes(res.status),`${method} ${path}: HTTP ${res.status}, expected ${expected}; ${typeof data.error==='string'?data.error:''}`);checks++;return data;
}
try{
 const alice=await user('Runtime owner'),bob=await user('Workspace owner'),admin=await user('Workspace admin');
 const a=await workspace('Runtime HTTP A',[[alice,'owner'],[bob,'member'],[admin,'admin']]);
 const b=await workspace('Runtime HTTP B',[[alice,'member'],[bob,'owner'],[admin,'member']]);
 const profileBody={display_name:'Shared personal test',runtime_type:'codex',protocol_family:'codex',command_name:'codex',fixed_args:[]};
 const pa=await call(alice,a,'POST',`/api/workspaces/${a}/runtime-profiles`,profileBody,201);
 const daemon=randomUUID();
 const register=(ws,p)=>call(alice,ws,'POST','/api/daemon/register',{workspace_id:ws,daemon_id:daemon,device_name:'Isolated test device',cli_version:'0.6.1',runtimes:[{name:'codex',type:'codex',version:'0.200.0',profile_id:p,status:'online'}]});
 const first=await register(a,pa.id);assert.equal(first.runtimes.length,1);const ra=first.runtimes[0].id;
 const profilesB=await call(alice,b,'GET',`/api/workspaces/${b}/runtime-profiles`);
 const pb=profilesB.runtime_profiles.find(p=>p.display_name===profileBody.display_name);assert.ok(pb);
 const listedB=await call(alice,b,'GET',`/api/runtimes?workspace_id=${b}`);assert.ok(listedB.some(r=>r.daemon_id===daemon));
 const second=await register(b,pb.id);const rb=second.runtimes[0].id;assert.notEqual(ra,rb);
 await call(alice,b,'PATCH',`/api/workspaces/${b}/runtime-profiles/${pb.id}`,{fixed_args:['--test-shared']});
 const updatedA=await call(alice,a,'GET',`/api/workspaces/${a}/runtime-profiles/${pa.id}`);assert.deepEqual(updatedA.fixed_args,['--test-shared']);
 await call(alice,b,'PATCH',`/api/runtimes/${rb}`,{custom_name:'Shared name',apply_to_machine:true});
 const runtimesA=await call(alice,a,'GET',`/api/runtimes?workspace_id=${a}`);assert.equal(runtimesA.find(r=>r.id===ra).custom_name,'Shared name');
 for(const u of [bob,admin]){
  const own=await call(u,a,'GET',`/api/runtimes?workspace_id=${a}`);assert.ok(own.every(r=>r.owner_id===u.id));
  await call(u,a,'GET',`/api/workspaces/${a}/runtime-profiles/${pa.id}`,undefined,404);
  await call(u,a,'PATCH',`/api/workspaces/${a}/runtime-profiles/${pa.id}`,{command_name:'forbidden'},404);
  await call(u,a,'PATCH',`/api/runtimes/${ra}`,{custom_name:'forbidden'},[403,404]);
  await call(u,a,'GET',`/api/runtimes/${ra}/delete-machine-plan`,undefined,404);
  await call(u,a,'POST',`/api/daemon/heartbeat`,{runtime_id:ra},404);
 }
 // Non-admin users can manage their own definitions; equal names across owners
 // do not collide or expose the other owner's configuration.
 await call(bob,a,'POST',`/api/workspaces/${a}/runtime-profiles`,profileBody,201);
 const agent=await call(alice,a,'POST','/api/agents',{name:'Shared fixture',runtime_id:ra,visibility:'workspace',mcp_config:[]},201);agents.push(agent.id);
 await call(admin,a,'PUT',`/api/agents/${agent.id}`,{instructions:'not allowed'},403);
 await call(bob,a,'POST','/api/agents',{name:'Forbidden binding',runtime_id:ra,visibility:'private'},[403,404]);
 const chat=await call(bob,a,'POST','/api/chat/sessions',{agent_id:agent.id,title:'Shared invocation'},201);assert.ok(chat.id);
 // Existing sharing works without exposing the underlying computer.
 const viewed=await call(bob,a,'GET',`/api/agents/${agent.id}`);assert.equal(viewed.id,agent.id);
 await call(bob,a,'POST',`/api/chat/sessions/${chat.id}/messages`,{content:'Isolated permission test; no real runtime is connected.'},201);
 await call(admin,a,'POST',`/api/daemon/runtimes/${ra}/tasks/claim`,undefined,404);
 const claimed=await call(alice,a,'POST',`/api/daemon/runtimes/${ra}/tasks/claim`);
 assert.equal(claimed.task?.agent_id,agent.id);
 assert.ok(claimed.task.auth_token);
 const sharedTask={id:alice.id,token:claimed.task.auth_token};
 await call(sharedTask,a,'GET',`/api/runtimes?workspace_id=${a}`,undefined,403);
 await call(sharedTask,a,'POST','/api/agents',{name:'Forbidden delegated creation',runtime_id:ra},403);
 await call(sharedTask,a,'POST','/api/agent-builder/sessions',{runtime_id:ra},403);
 await call(sharedTask,a,'GET','/api/agent-builder/sessions',undefined,403);
 const audit=await db.query('SELECT originator_user_id FROM agent_task_queue WHERE id=$1',[claimed.task.id]);assert.equal(audit.rows[0].originator_user_id,bob.id);
 await call(alice,b,'DELETE',`/api/workspaces/${b}/runtime-profiles/${pb.id}`,undefined,409);
 const globalPlan=await call(alice,b,'GET',`/api/runtimes/${rb}/delete-machine-plan`);assert.ok(globalPlan.ids.includes(ra)&&globalPlan.ids.includes(rb));assert.ok(globalPlan.agents.some(x=>x.id===agent.id));
 await db.query("UPDATE agent_runtime SET status='offline' WHERE id=ANY($1::uuid[])",[globalPlan.ids]);
 await call(alice,b,'POST',`/api/runtimes/${rb}/delete-offline-machine`,{expected_runtime_ids:globalPlan.ids,expected_active_agent_ids:globalPlan.agents.map(x=>x.id)},409);
 // Complete the fake run without revoking its token, proving the terminal
 // check prevents lending rights even when token revocation is delayed.
 await db.query("UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1",[claimed.task.id]);
 await db.query("UPDATE agent SET status='idle' WHERE id=$1",[agent.id]);
 await db.query("UPDATE agent_runtime SET status='online',last_seen_at=now() WHERE id=ANY($1::uuid[])",[globalPlan.ids]);
 await call(sharedTask,a,'GET',`/api/runtimes?workspace_id=${a}`,undefined,403);
 const ownerChat=await call(alice,a,'POST','/api/chat/sessions',{agent_id:agent.id,title:'Owner delegated creation'},201);
 await call(alice,a,'POST',`/api/chat/sessions/${ownerChat.id}/messages`,{content:'Owner initiated isolated assistant test.'},201);
 const ownerClaim=await call(alice,a,'POST',`/api/daemon/runtimes/${ra}/tasks/claim`);assert.ok(ownerClaim.task?.auth_token);
 const ownerTask={id:alice.id,token:ownerClaim.task.auth_token};
 const ownerList=await call(ownerTask,a,'GET',`/api/runtimes?workspace_id=${a}`);assert.ok(ownerList.every(r=>r.owner_id===alice.id));
 const child=await call(ownerTask,a,'POST','/api/agents',{name:'Owner delegated agent',runtime_id:ra},201);agents.push(child.id);assert.equal(child.owner_id,alice.id);
 await call(ownerTask,a,'PUT',`/api/agents/${child.id}`,{description:'Owner delegated configuration'},200);
 await call(ownerTask,a,'POST','/api/agent-builder/sessions',{runtime_id:ra},201);
 await call(ownerTask,a,'PATCH',`/api/runtimes/${ra}`,{custom_name:'Account-level mutation forbidden'},403);
 await db.query("UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1",[ownerClaim.task.id]);
 await call(ownerTask,a,'GET',`/api/runtimes?workspace_id=${a}`,undefined,403);
 await call(ownerTask,a,'POST','/api/agents',{name:'Terminal task cannot create',runtime_id:ra},403);
 console.log(JSON.stringify({ok:true,checks,coverage:['real PAT authentication','cross-workspace visibility','shared configuration','global rename','owner/admin isolation','daemon heartbeat/claim isolation','member-owned profiles','shared-agent chat and owner-daemon claim','caller audit preserved','unfinished-task global deletion guard','shared-task cannot borrow host governance','owner assistant creation preserved','account runtime mutations remain human-only']}));
}finally{
 if(spaces.length)await db.query('DELETE FROM agent_task_queue WHERE agent_id IN (SELECT id FROM agent WHERE workspace_id=ANY($1::uuid[]))',[spaces]);
 if(spaces.length)await db.query('DELETE FROM workspace WHERE id=ANY($1::uuid[])',[spaces]);
 const ids=users.map(u=>u.id);
 if(ids.length){
  await db.query('DELETE FROM runtime_profile WHERE created_by=ANY($1::uuid[])',[ids]);
  await db.query('DELETE FROM personal_runtime WHERE owner_id=ANY($1::uuid[])',[ids]);
  await db.query('DELETE FROM personal_runtime_profile WHERE owner_id=ANY($1::uuid[])',[ids]);
  await db.query('DELETE FROM "user" WHERE id=ANY($1::uuid[])',[ids]);
 }
 await db.end();
}
