// Run with Node. Talks only to the loopback lab and modifies only the sandbox
// whose private marker and two-process metadata were created by lab.sh.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
const origin='http://127.0.0.1:4180';
const info=await (await fetch(origin+'/lab-api/info')).json();
assert.match(info.root,/^\/tmp\/teammesh-live-lab\.[A-Za-z0-9]+$/);
await fs.access(path.join(info.root,'.teammesh-lab'));
const call=async(query)=>{const response=await fetch(origin+'/lab-api/workspace',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({session_id:'lab-chat-one',...query})});return {status:response.status,body:await response.json()};};
const root=await call({operation:'list',path:'.'});assert.equal(root.status,200);assert.ok(root.body.binding);
assert.ok(!root.body.entries.some(entry=>entry.name==='.env'));
const binding=root.body.binding;const relative='src/auth/verification.txt';const file=path.join(info.root,'lab-chat-one',relative);
await fs.writeFile(file,'ONLY-ONE-'+Date.now(),{flag:'wx',mode:0o600});
try{
 const read=await call({operation:'read',binding,path:relative});assert.equal(read.status,200);assert.equal(read.body.content,await fs.readFile(file,'utf8'));
 const other=await call({session_id:'lab-chat-two',operation:'list',path:'.'});assert.equal(other.status,200);
 assert.notEqual(other.body.binding,binding);assert.notEqual(other.body.absolute_path,root.body.absolute_path);
 assert.equal((await call({session_id:'lab-chat-two',operation:'read',binding:other.body.binding,path:relative})).body.error,'file_unavailable');
 await fs.appendFile(file,'\nCHANGED');
 const stale=await call({operation:'context',binding,references:[{kind:'file',path:relative,revision:read.body.revision}]});assert.equal(stale.body.error,'file_changed');
 const fresh=await call({operation:'read',binding,path:relative});
 const context=await call({operation:'context',binding,references:[{kind:'file',path:relative,revision:fresh.body.revision,start_line:2,end_line:2}]});assert.equal(context.status,200);assert.match(context.body.context,/CHANGED/);assert.ok(!context.body.context.includes('ONLY-ONE-'));
 for(const name of ['../.teammesh-lab','.env','/etc/passwd'])assert.equal((await call({operation:'read',binding,path:name})).body.error,'path_denied');
 assert.equal((await call({session_id:'unauthorized-session',operation:'list',path:'.'})).status,403);
 const crossOrigin=await fetch(origin+'/lab-api/workspace',{method:'POST',headers:{Origin:'https://untrusted.example','Content-Type':'application/json'},body:'{}'});assert.equal(crossOrigin.status,403);
 assert.equal((await fetch('http://127.0.0.1:4181/lab/workspace',{method:'POST',body:'{}'})).status,401);
 console.log('PASS: live read, per-session roots, stale revision rejection, selected-line context, traversal/sensitive path denial, unknown session denial, cross-origin and unauthenticated relay rejection');
}finally{await fs.unlink(file)}
