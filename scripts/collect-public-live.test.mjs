import {test} from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import {validateSection,collectSection,intervals} from './collect-public-live.mjs';

const observed_at='2026-10-22T12:00:00+00:00';
const realtime={observed_at,window_start:'2026-10-22T11:00:00+00:00',active_users:null,working_agents:2,working_issues:1,working_chats:1,online_machines:3,events:[{kind:'completed',at:observed_at,count:1,members:['@ex***e']}],unavailable_metrics:['active_users']};
test('two-tier collection schedule and one-hour boundary are explicit',()=>{
 assert.deepEqual(intervals,{realtime:10,recent:10,history:300});
 assert.throws(()=>validateSection('realtime',{...realtime,window_start:'2026-10-22T10:00:00+00:00'}));
 assert.equal(validateSection('realtime',{...realtime,scale:{users:1,issues:2,agents:3,machines:4}}).scale.users,1);
});
test('public projection strips all extra raw fields and preserves unknown values',()=>{
 const raw={...realtime,email:'fixture@example.test',events:[{...realtime.events[0],user_id:'private-fixture-id'}]};
 const value=validateSection('realtime',raw),out=JSON.stringify(value);
 assert.equal(value.active_users,null);assert(!out.includes('fixture@example.test'));assert(!out.includes('private-fixture-id'));assert.deepEqual(value.events[0].members,['@ex***e']);
});
test('full account names and URLs cannot be published as usernames',()=>{
 for(const label of ['@full-account','employee@example.test','https://example.test','@ab***c extra'])assert.throws(()=>validateSection('realtime',{...realtime,events:[{...realtime.events[0],members:[label]}]}));
});
test('requires genuine rolling 24-hour boundaries and numeric counts',()=>{
 const recent={observed_at,window_start:'2026-10-21T12:00:00+00:00',active_users:null,page_views:null,messages:5,created_issues:2,completed_issues:1};
 assert.equal(validateSection('recent',recent).messages,5);
 assert.throws(()=>validateSection('recent',{...recent,window_start:'2026-10-22T00:00:00+00:00'}));
 assert.throws(()=>validateSection('recent',{...recent,messages:'5'}));
});
test('history consists of seven complete Beijing calendar days',()=>{
 const raw={observed_at,days:Array.from({length:7},(_,i)=>({date:`2026-10-${15+i}`,active_users:null,page_views:null,messages:i,created_issues:i,completed_issues:i})),scale:{users:1,issues:2,agents:3,machines:4},capabilities:{multi_agent_issues:1,infoflow_inputs:2,wakeup_triggers:3}};
 assert.equal(validateSection('history',raw).days.length,7);
 assert.throws(()=>validateSection('history',{...raw,days:raw.days.slice(1)}));
 assert.throws(()=>validateSection('history',{...raw,days:raw.days.map((d,i)=>({...d,date:`2026-10-${16+i}`}))}));
});
test('all production SQL is bounded and executed in read-only transactions',async()=>{
 for(const key of ['realtime','recent','history']){
  const sql=await fs.readFile(new URL(`../stats/live/${key}.sql`,import.meta.url),'utf8');
  assert.match(sql,/BEGIN TRANSACTION.*READ ONLY;/);assert.match(sql,/SET LOCAL statement_timeout/);assert.match(sql,/SET LOCAL lock_timeout/);
  assert(!/^\s*(INSERT|UPDATE|DELETE|DROP|ALTER|CREATE|TRUNCATE|COPY|CALL)\b/im.test(sql));
 }
});
test('unknown sections and invalid source configuration fail before executing SSH',async()=>{
 await assert.rejects(collectSection('sql',{}));
 await assert.rejects(collectSection('recent',{host:'-bad host',port:22,docker:'/bin/docker',container:'pg',database:'test'}));
});
