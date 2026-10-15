import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createHash } from "node:crypto";
import { notifyUsers, mentionedReplyUsers, deliverReply, notificationText } from "./src/reply-mentions.js";
import { sendGroupExact } from "./src/issue-groups.js";
import { Bridge } from "./src/bridge.js";
import { ChatRelay } from "./src/chat.js";

const group = { kind: "group", id: "13579" };
test("only group human mentions are deduplicated with the original sender", () => {
  assert.deepEqual(mentionedReplyUsers(group, "Alice", [
    { kind: "robot", robotId: "123", name: "Bot" }, { kind: "user", userId: "BOB" },
    { kind: "user", userId: "alice" }, { kind: "user", userId: "bob" }, { kind: "unknown", userId: "mallory" },
  ]), ["alice", "bob"]);
  assert.deepEqual(notifyUsers({ kind: "private" }, "alice", ["bob"]), []);
  assert.throws(() => notifyUsers(group, "alice", ["@all"]), /invalid notification/);
  assert.throws(() => mentionedReplyUsers(group, "alice", Array.from({ length: 50 }, (_, i) => ({kind: "user", userId: `user${i}`}))), /too many/);
});

test("one untouched Markdown body plus one native AT; retry after AT failure skips the body", async () => {
  const bodies = [], progress = {};
  let fail = true, saved;
  const client = { httpClient: { async post(_path, wire) {
    const payload = JSON.parse(wire); bodies.push(payload.message);
    if (payload.message.header.msgtype === "TEXT" && fail) throw Error("AT send failed");
    return { errcode: 0, data: { messageid: "1234567890123456789" } };
  } } };
  const send = (conversation, content, options) => sendGroupExact(client, conversation, content, options);
  const content = "**Bold**\n\n- item\n\n```js\nlet n = 1;\n```";
  const notice = notificationText("https://example.test/issues/TEST-1", "TEST-1");
  await assert.rejects(deliverReply(send, group, content, ["alice", "bob"], notice, progress,
    () => { saved = JSON.parse(JSON.stringify(progress)); }), /AT send failed/);
  assert.equal(saved.bodySent, true);
  assert.equal(saved.notified, undefined);
  fail = false;
  await deliverReply(send, group, content, ["alice", "bob"], notice, saved);
  assert.equal(bodies.length, 3);
  assert.equal(bodies.filter(m => m.header.msgtype === "MD").length, 1);
  assert.deepEqual(bodies[0].body, [{ type: "MD", content }]);
  assert.deepEqual(bodies[2].body[0], { type: "AT", atall: false, atuserids: ["alice", "bob"] });
  assert.equal(bodies[2].body.length, 2);
  assert.equal(bodies[2].body.some(block => block.type === "LINK"), false);
  assert.doesNotMatch(bodies[2].body[1].content, /https?:|完整对话链接/);
  assert.equal(saved.notified, true);
  await deliverReply(send, group, content, ["alice", "bob"], notice, saved);
  assert.equal(bodies.length, 3);
  await assert.rejects(deliverReply(send, group, content, ["mallory"], notice, saved), /changed during/);
});

test("private replies keep Markdown and do not add an AT message", async () => {
  const calls = [];
  await deliverReply(async (...args) => calls.push(args), { kind: "private", id: "alice" }, "**Bold**", ["alice", "bob"], "notice");
  assert.equal(calls.length, 1);
  assert.equal(calls[0][1], "**Bold**");
  assert.deepEqual(calls[0][2], {});
});

test("new reminders omit the URL while an already sent body's legacy reminder can finish safely", async () => {
  const body = "**Body**", users = ["alice"], oldNotice = "[回复提醒]\n回复已更新，请查看上一条消息。\n完整对话链接：https://example.test/chat";
  const progress = { bodySent: true, digest: createHash("sha256").update(JSON.stringify([body, users, oldNotice])).digest("hex") };
  const calls = [];
  await deliverReply(async (_c, text, options) => calls.push({ text, options }), group, body, users,
    notificationText("https://example.test/chat"), progress, () => {}, oldNotice);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].options.notificationOnly, true);
  assert.equal(calls[0].text, oldNotice);
  assert.doesNotMatch(notificationText("https://example.test/chat"), /https?:|完整对话链接/);
});

test("coalesced audiences over 50 are notified in checkpointed batches without repeating Markdown", async () => {
  const users = Array.from({ length: 75 }, (_, i) => `person${i}`), progress = {}, calls = [];
  let failSecondBatch = true;
  const send = async (_conversation, _text, options) => {
    calls.push(options);
    if (options.notificationOnly && options.atUsers[0] === "person50" && failSecondBatch) throw Error("second batch failed");
    return { messageid: `12345678901234567${calls.length}` };
  };
  await assert.rejects(deliverReply(send, group, "**Body**", users, "notice", progress), /second batch/);
  assert.equal(progress.bodySent, true); assert.equal(progress.notificationNext, 50);
  assert.equal(progress.notificationMessageIds.length, 1);
  failSecondBatch = false;
  await deliverReply(send, group, "**Body**", users, "notice", progress);
  assert.equal(calls.filter(c => !c.notificationOnly).length, 1);
  assert.deepEqual(calls.filter(c => c.notificationOnly).map(c => c.atUsers.length), [50, 25, 25]);
  assert.equal(progress.notificationMessageIds.length, 2);
  assert.equal(progress.notified, true);
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

test("Chat restart retries only the original task's AT and never mixes concurrent requesters", async t => {
  const dir = mkdtempSync(join(tmpdir(), "chat-notify-"));
  const config = { robotId: "r", statePath: join(dir, "state.json"), allowAllInGroups: true, pollIntervalMs: 100000,
    multica: { defaultAgent: "agent", profile: "owner", workspaceId: "ws", workspaceSlug: "ws", webUrl: "http://localhost" } };
  const calls = [], messages = [];
  let fail = true, session = 0;
  const sender = async (conversation, content, options) => {
    calls.push({ conversation, content, options });
    if (options.notificationOnly && options.atUsers.includes("alice") && fail) throw Error("AT failed");
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
  const first = new Bridge(config, sender, { warn() {}, error() {} }); install(first);
  t.after(() => { first.stop(); rmSync(dir, { recursive: true, force: true }); });
  await first.handleMessage({ conversation: group, user: "alice", text: "work one", messageId: "one", notifyUsers: ["carol", "ALICE", "carol"], acknowledge: async () => true });
  await first.handleMessage({ conversation: group, user: "bob", text: "work two", messageId: "two", notifyUsers: ["dave"], acknowledge: async () => true });
  await first.chat.poll();
  assert.equal(first.chat.state.pending["task-0"].replyProgress.bodySent, true);
  assert.equal(first.chat.state.pending["task-1"], undefined);
  assert.equal(calls.filter(c => c.content.includes("answer task-0")).length, 1);
  first.stop(); fail = false;
  const restored = new Bridge(config, sender, { warn() {}, error() {} }); install(restored); t.after(() => restored.stop());
  await restored.chat.poll();
  assert.deepEqual(restored.chat.state.pending, {});
  assert.equal(calls.filter(c => c.content.includes("answer task-0")).length, 1);
  assert.deepEqual(calls.filter(c => c.options.notificationOnly).map(c => c.options.atUsers), [["alice", "carol"], ["bob", "dave"], ["alice", "carol"]]);
});
