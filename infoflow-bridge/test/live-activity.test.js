import test from "node:test";
import assert from "node:assert/strict";
import {IssueRelay} from "../src/issue-relay.js";
const workspace="00000000-0000-0000-0000-000000000001";
const config={robotId:"fixture-robot",statePath:"/nonexistent/live-test",issueRelay:{enabled:true},multica:{workspaceId:workspace,serverUrl:"https://api.example.test"}};
function relay(fetchImpl,overrides={}){const r=new IssueRelay({...config,...overrides},{},()=>{},console,{fetchImpl,token:"x".repeat(32)});clearInterval(r.timer);clearInterval(r.noticeTimer);clearInterval(r.directoryTimer);return r;}
test("accepted chat reports real sender and no content, handles 204",async()=>{
 let request;const r=relay(async(url,options)=>{request={url,options};return new Response(null,{status:204})});
 await r.recordLiveActivity("actual-sender","1234");r.stop();
 assert.equal(request.url,"https://api.example.test/api/integrations/infoflow/live-activity");assert.deepEqual(JSON.parse(request.options.body),{sender:"actual-sender",message_id:"1234",robot_id:"fixture-robot",workspace_id:workspace});
});
test("optional metrics cannot fail business delivery on network or old API",async()=>{
 for(const fetchImpl of [async()=>{throw Error("network failure")},async()=>new Response(null,{status:404})]){const r=relay(fetchImpl);await assert.doesNotReject(r.recordLiveActivity("sender","1234"));r.stop();}
});
test("disabled or unidentified chats emit nothing",async()=>{
 let calls=0;const r=relay(async()=>{calls++},{issueRelay:{enabled:false}});await r.recordLiveActivity("sender","1234");r.stop();assert.equal(calls,0);
 const active=relay(async()=>{calls++});await active.recordLiveActivity("sender",null);active.stop();await active.recordLiveActivity("sender","1234");assert.equal(calls,0);
});
