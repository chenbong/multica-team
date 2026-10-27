import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createHash } from "node:crypto";
import { notifyUsers, mentionedReplyUsers, deliverReply, notificationText } from "./src/reply-mentions.js";
import { sendGroupExact } from "./src/issue-groups.js";
import { Bridge } from "./src/bridge.js";
import { installChatInputFixture } from "./test/chat-input-fixture.js";
import { ChatRelay } from "./src/chat.js";

const group = { kind: "group", id: "13579" };
test("only group human mentions are deduplicated with the original sender", () => {
  assert.deepEqual(mentionedReplyUsers(group, "Alice", [
    { kind: "robot", robotId: "123", name: "Bot" }, { kind: "user", userId: "BOB" },
    { kind: "user", userId: "alice" }, { kind: "user", userId: "bob" }, { kind: "unknown", userId: "mallory" },
  ]), ["alice", "bob"]);
  assert.deepEqual(notifyUsers({ kind: "private" }, "alice", ["bob"]), []);
  assert.throws(() => notifyUsers(group, "alice", ["@all"]), /invalid notification/);
  assert.equal(mentionedReplyUsers(group, "alice", Array.from({ length: 50 }, (_, i) => ({kind: "user", userId: `user${i}`}))).length, 51);
});

test("one combined Markdown and AT delivery persists its exact payload through timeout and restart", async () => {
 const wires=[],progress={};let fail=true,saved;
 const client={httpClient:{post:async(_path,wire)=>{wires.push(JSON.parse(wire).message);if(fail)throw Error("timeout");return {errcode:0,data:{messageid:"1234567890123456789"}};}}};
 const send=(c,text,options)=>sendGroupExact(client,c,text,options);
 const content="**Bold**\n\n- item\n\n**[完整对话链接](https://example.test/task)**";
 await assert.rejects(deliverReply(send,group,content,["alice","bob"],"unused",progress,()=>{saved=JSON.parse(JSON.stringify(progress))}),/timeout/);
 assert.equal(saved.bodySent,undefined);assert(saved.transport.id);
 fail=false;
 await deliverReply(send,group,content,["alice","bob"],"unused",saved);
 assert.deepEqual(wires[0],wires[1]);
 assert.equal(wires[1].header.msgtype,"MD");
 assert.deepEqual(wires[1].body[0],{type:"AT",atall:false,atuserids:["alice","bob"]});
 assert.match(wires[1].body[1].content,/@alice @bob\n\n\*\*\[完整对话链接\]/);
 assert.equal(saved.notified,true);
 await deliverReply(send,group,content,["alice","bob"],"unused",saved);
 assert.equal(wires.length,2);
});

test("private replies keep Markdown and do not add an AT message", async () => {
  const calls = [];
  await deliverReply(async (...args) => calls.push(args), { kind: "private", id: "alice" }, "**Bold**", ["alice", "bob"], "notice");
  assert.equal(calls.length, 1);
  assert.equal(calls[0][1], "**Bold**");
  assert.deepEqual(calls[0][2], {});
});

test("accepted legacy Markdown never sends its pending standalone reminder", async () => {
 const progress={bodySent:true,digest:"historical-digest"},calls=[];
 await deliverReply(async(...args)=>calls.push(args),group,"body",["alice"],"old reminder",progress);
 assert.equal(calls.length,0);assert.equal(progress.notified,true);
});

test("audiences above 50 produce one message, preserve the first requester and explain omissions", async () => {
 const users=["requester",...Array.from({length:74},(_,i)=>"person"+i)],wires=[];
 const client={httpClient:{post:async(_p,wire)=>{wires.push(JSON.parse(wire).message);return {errcode:0,data:{messageid:"123"}}}}};
 await deliverReply((c,t,o)=>sendGroupExact(client,c,t,o),group,"**Body**",users,"unused");
 assert.equal(wires.length,1);assert.equal(wires[0].body[0].atuserids.length,50);
 assert.equal(wires[0].body[0].atuserids[0],"requester");
 assert.match(wires[0].body[1].content,/25 人未被 @ 提醒/);
});

test("coalesced Chat inputs retain the union of their explicit notification audiences", async t => {
  const dir = mkdtempSync(join(tmpdir(), "coalesced-chat-notify-"));
  const config = { robotId: "r", statePath: join(dir, "state.json"), multica: { workspaceId: "ws" }, pollIntervalMs: 100000 };
  const relay = new ChatRelay(config, {
    listAgents: async () => [{ id: "a", name: "agent" }],
    createChatSession: async () => ({ id: "session" }), chatSession: async () => ({ status: "active" }),
    sendChatMessage: async () => ({ task_id: "same-task" }),
  }, async () => {}, async () => ({ paths: [], failed: [], cleanup: async () => {} }));
  t.after(() => { relay.stop(); rmSync(dir, { recursive: true, force: true }); });
  await relay.send(group, "alice", "first", [], "agent", async () => true, "", null, ["bob"]);
  await relay.send(group, "alice", "second", [], "agent", async () => true, "", null, ["carol"]);
  assert.deepEqual(relay.state.pending["same-task"].notifyUsers, ["alice", "bob", "carol"]);
});

test("Chat restart retries the original combined reply without mixing requesters", async t => {
  const dir = mkdtempSync(join(tmpdir(), "chat-notify-"));
  const config = { robotId: "r", statePath: join(dir, "state.json"), allowAllInGroups: true, pollIntervalMs: 100000,
    multica: { defaultAgent: "agent", profile: "owner", workspaceId: "ws", workspaceSlug: "ws", webUrl: "http://localhost" } };
  const calls = [], messages = [];
  let fail = true, session = 0;
  const sender = async (conversation, content, options) => {
    calls.push({ conversation, content, options });
    if (options.atUsers?.includes("alice") && fail) throw Error("send failed");
  };
  const install = bridge => Object.assign(bridge.multica, {
    listAgents: async () => [{ id: "agent-id", name: "agent" }],
    createChatSession: async () => ({ id: `session-${session++}`, status: "active" }),
    chatSession: async () => ({ status: "active" }),
    sendChatMessage: async (sessionId, content) => {
      const task_id = `task-${messages.length}`; messages.push({ sessionId, content, task_id }); return { task_id };
    },
    chatMessages: async id => messages.filter(m => m.sessionId === id).map(m => ({ role: "assistant", task_id: m.task_id, content: `**answer ${m.task_id}**` })),
  });
  const first = new Bridge(config, sender, { warn() {}, error() {} }); install(first); installChatInputFixture(first, { bypassRelay: true });
  t.after(() => { first.stop(); rmSync(dir, { recursive: true, force: true }); });
  await first.handleMessage({ conversation: group, user: "alice", text: "work one", messageId: "one", notifyUsers: ["carol", "ALICE", "carol"], acknowledge: async () => true });
  await first.handleMessage({ conversation: group, user: "bob", text: "work two", messageId: "two", notifyUsers: ["dave"], acknowledge: async () => true });
  await first.chat.poll();
  assert.equal(first.chat.state.pending["task-0"].replyProgress.bodySent, undefined);
  assert.equal(first.chat.state.pending["task-1"], undefined);
  assert.equal(calls.filter(c => c.content.includes("answer task-0")).length, 1);
  first.stop(); fail = false;
  const restored = new Bridge(config, sender, { warn() {}, error() {} }); install(restored); installChatInputFixture(restored, { bypassRelay: true }); t.after(() => restored.stop());
  await restored.chat.poll();
  assert.deepEqual(restored.chat.state.pending, {});
  assert.equal(calls.filter(c => c.content.includes("answer task-0")).length, 2);
  assert.deepEqual(calls.filter(c => c.options.atUsers).map(c => c.options.atUsers), [["alice", "carol"], ["bob", "dave"], ["alice", "carol"]]);
});
