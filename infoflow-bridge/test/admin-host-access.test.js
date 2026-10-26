import assert from 'node:assert/strict';
import test from 'node:test';
import {request} from 'node:http';
import {once} from 'node:events';
import {mkdtemp,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {startAdminServer} from '../src/admin.js';

test('admin accepts all reachable hostnames while preserving session and login gates',async(t)=>{
 const dir=await mkdtemp(join(tmpdir(),'bridge-host-test-'));
 const previous=process.env.INFOFLOW_ADMIN_SESSIONS_PATH;
 process.env.INFOFLOW_ADMIN_SESSIONS_PATH=join(dir,'sessions.json');
 let sent=0;
 const runtime={config:{admin:{port:0,host:'127.0.0.1',publicHosts:['old.example.test'],allowedDomains:['example.test'],allowEmails:[]},robots:[],multica:{webUrl:'https://app.example.test'}},sendVerification:async()=>{sent++;},verificationRobot:()=>null};
 const server=startAdminServer({runtime,logger:{log(){},warn(){},error(){}}});
 t.after(async()=>{await new Promise(r=>server.close(r));if(previous===undefined)delete process.env.INFOFLOW_ADMIN_SESSIONS_PATH;else process.env.INFOFLOW_ADMIN_SESSIONS_PATH=previous;await rm(dir,{recursive:true,force:true});});
 await once(server,'listening');
 const port=server.address().port;
 const call=(host,path='/',method='GET',input,session)=>new Promise((resolve,reject)=>{
  const req=request({hostname:'127.0.0.1',port,path,method,agent:false,headers:{Host:host,'Content-Type':'application/json',...(session?{Cookie:'bridge_admin='+session}:{})}},res=>{const chunks=[];res.on('data',c=>chunks.push(c));res.on('end',()=>resolve({status:res.statusCode,headers:res.headers,body:Buffer.concat(chunks).toString()}));});
  req.on('error',reject);req.end(input?JSON.stringify(input):undefined);
 });
 for(const host of ['127.0.0.1:'+port,'old.example.test:'+port,'new.example.test:'+port,'arbitrary.example.test']){
  const page=await call(host);assert.equal(page.status,200);assert.match(page.headers['content-type'],/text\/html/);assert(!page.body.includes('only reachable from this machine'));
  assert.equal((await call(host,'/api/data')).status,401);
  assert.equal((await call(host,'/api/data','GET',undefined,'forged-session')).status,401);
  assert.equal((await call(host,'/api/robot','POST',{appId:'not-real'})).status,401);
  assert.equal((await call(host,'/api/login/code','POST',{email:'someone@outside.test'})).status,400);
 }
 assert.equal(sent,0,'must not send verification messages during access checks');
});
