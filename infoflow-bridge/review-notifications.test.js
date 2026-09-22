import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { ReviewNotifications } from "./src/review-notifications.js";

function setup(t) {
  const dir = mkdtempSync(join(tmpdir(), "review-notify-"));
  const issue = {id:"i",identifier:"TEST-1",title:"test",assignee_type:"agent",assignee_id:"a",revision:1};
  const timeline = [{id:"old",action:"status_changed",created_at:new Date(1000).toISOString(),details:{from:"todo",to:"in_review"}}];
  const sent=[];
  const m={listAgents:async()=>[{id:"a",name:"agent"}],issueUrl:()=>"http://localhost/test/issues/TEST-1",
    request:async path=>path==="/api/me"?{email:"binder@example.com"}:path.startsWith("/api/issue-statuses")?{statuses:[{key:"custom_review",category:"in_review"},{key:"custom_blocked",category:"blocked"}]}:path.endsWith("/timeline")?timeline:{issues:[issue],total:1}};
  const robot={id:"r",appId:"im",profile:"p",workspaceId:"w",agent:"agent",purpose:"task",enabled:true};
  const runtime={config:{statePath:join(dir,"state.json")},instances:new Map([["r",{robot,bridge:{multica:m}}]]),logger:{warn() {},log() {}},sendDirect:async(id,user,text)=>{sent.push({id,user,text});}};
  const watcher=new ReviewNotifications(runtime,{now:()=>5000,intervalMs:999999});
  t.after(()=>{watcher.stop();rmSync(dir,{recursive:true,force:true});});
  const review=(id="new",to="in_review",from="in_progress")=>{issue.revision++;timeline.push({id,action:"status_changed",created_at:new Date(6000).toISOString(),details:{from,to}});};
  return {runtime,watcher,sent,review,issue,timeline,robot};
}

test("skip history; notify binding owner once across polls and restart",async t=>{
  const f=setup(t);await f.watcher.poll();assert.equal(f.sent.length,0);
  f.review();await f.watcher.poll();await f.watcher.poll();assert.equal(f.sent.length,1);assert.equal(f.sent[0].user,"binder");
  f.watcher.stop();const restored=new ReviewNotifications(f.runtime,{now:()=>9000,intervalMs:999999});t.after(()=>restored.stop());
  await restored.poll();assert.equal(f.sent.length,1);
  f.review("again","custom_review");await restored.poll();assert.equal(f.sent.length,2);
});
test("failed delivery retries without losing notification",async t=>{
  const f=setup(t);await f.watcher.poll();const sender=f.runtime.sendDirect;f.runtime.sendDirect=async()=>{throw Error("offline")};
  f.review();await f.watcher.poll();assert.equal(f.sent.length,0);
  f.runtime.sendDirect=sender;await f.watcher.poll();assert.equal(f.sent.length,1);
});
test("duplicate binding entries for same robot do not double notify",async t=>{
  const f=setup(t);f.runtime.instances.set("copy",{robot:{...f.robot,id:"copy"},bridge:f.runtime.instances.get("r").bridge});
  await f.watcher.poll();f.review();await f.watcher.poll();assert.equal(f.sent.length,1);
});
test("chat/run completion alone and unbound agents do not notify",async t=>{
  const f=setup(t);await f.watcher.poll();f.issue.revision++;f.timeline.push({id:"run",action:"task_completed",created_at:new Date(6000).toISOString(),details:{}});
  await f.watcher.poll();assert.equal(f.sent.length,0);
  f.issue.assignee_id="other";f.review();await f.watcher.poll();assert.equal(f.sent.length,0);
});
test("identity mismatch blocks notification",async t=>{
  const f=setup(t);f.robot.boundByEmail="someoneelse@example.com";await f.watcher.poll();f.review();await f.watcher.poll();assert.equal(f.sent.length,0);
});

test("blocked transition notifies and is distinct from review",async t=>{
  const f=setup(t); await f.watcher.poll();
  f.review("blocked", "blocked"); await f.watcher.poll();
  assert.equal(f.sent.length, 1);
  assert.match(f.sent[0].text, /任务已阻塞/);
  f.review("review-after-block", "in_review", "blocked"); await f.watcher.poll();
  assert.equal(f.sent.length, 2);
  assert.match(f.sent[1].text, /任务已进入审核中/);
});
