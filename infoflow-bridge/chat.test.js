import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Bridge } from "./src/bridge.js";
import { issueRequest, ChatRelay } from "./src/chat.js";

test("explicit creation only; negation, quotes and progress remain chat", () => {
  for (const text of ["/issue 修复故障", "帮我创建一个任务：修复故障", "请帮我新建一个issue 修复故障"])
    assert.equal(issueRequest(text), "修复故障");
  for (const text of ["你好", "帮我优化性能", "不要创建任务", "如何创建任务？", "CBH-15 进展怎么样？", "> 帮我创建任务：修复故障", "他说创建任务：修复故障"])
    assert.equal(issueRequest(text), null);
});

function fixture(t) {
  const dir = mkdtempSync(join(tmpdir(), "bridge-chat-test-"));
  const replies = [], sessions = [], messages = [], issues = [];
  const config = { robotId: "r", statePath: join(dir, "state.json"), allowUsers: ["alice", "bob"], allowAllInGroups: true,
    multica: { cli: "unused", defaultAgent: "agent", profile: "owner", workspaceId: "ws", webUrl: "http://localhost", workspaceSlug: "ws" },
    pollIntervalMs: 100000, pollTimeoutMs: 1 };
  const bridge = new Bridge(config, async (conversation, content, options) => replies.push({ conversation, content, ...options }));
  Object.assign(bridge.multica, {
    listAgents: async () => [{ id: "a", name: "agent" }],
    createChatSession: async (id, title) => { const session = { id: "s" + sessions.length, status: "active" }; sessions.push({ ...session, title }); return session; },
    chatSession: async (id) => sessions.find((s) => s.id === id),
    sendChatMessage: async (sessionId, content) => { const task_id = "t" + messages.length; messages.push({ sessionId, content, task_id }); return { task_id }; },
    chatMessages: async (id) => messages.filter((m) => m.sessionId === id).map((m) => ({ role: "assistant", task_id: m.task_id, content: "answer:" + m.task_id })),
    chatPending: async () => ({}),
    createIssue: async (data) => { issues.push(data); return { id: "i", identifier: "TEST-1" }; },
    runs: async () => [{ status: "completed", id: "run", result: { output: "done" } }],
    comments: async () => [],
    getIssue: async () => ({ id: "i", identifier: "TEST-1", title: "test" }),
  });
  t.after(() => { bridge.stop(); rmSync(dir, { recursive: true, force: true }); });
  const send = (text, user = "alice", id = "private") => bridge.handleMessage({ text, user, conversation: { kind: "private", id: id + user }, messageId: "m" + Math.random() });
  return { bridge, config, replies, sessions, messages, issues, send };
}

test("concurrent messages reuse a session; users separated; reset starts a new session", async (t) => {
  const f = fixture(t);
  await Promise.all([f.send("你好"), f.send("你记住了吗")]);
  assert.equal(f.sessions.length, 1);
  assert.equal(f.messages.length, 2);
  await f.send("你好", "bob");
  assert.equal(f.sessions.length, 2);
  await f.send("/new");
  await f.send("新对话");
  assert.equal(f.sessions.length, 3);
  await f.bridge.chat.poll();
  assert.equal(f.replies.filter((r) => r.content.startsWith("answer:")).length, 4);
  assert.equal(f.issues.length, 0);
});

test("explicit issues, status, help and unknown slash commands do not start chat", async (t) => {
  const f = fixture(t);
  f.config.pollIntervalMs = 1;
  f.config.pollTimeoutMs = 1000;
  await f.send("/issue 修复故障");
  assert.equal(f.issues.length, 1);
  await f.send("/status TEST-1");
  await f.send("TEST-1 进度");
  await f.send("/help");
  await f.send("/unknown");
  await f.send("/issue");
  assert.equal(f.messages.length, 0);
  assert.equal(f.issues.length, 1);
});

test("restart restores same conversation and pending replies, by exact task id", async (t) => {
  const f = fixture(t);
  await f.send("你好");
  f.bridge.chat.stop();
  const restored = new ChatRelay(f.config, f.bridge.multica, async (c, u, text) => f.replies.push({ content: text }), async () => ({ paths: [], failed: [], cleanup: async () => {} }));
  t.after(() => restored.stop());
  await restored.poll();
  assert.equal(f.replies.filter((r) => r.content === "answer:t0").length, 1);
  assert.equal(Object.keys(restored.state.pending).length, 0);
  assert.equal(Object.values(restored.state.threads)[0].id, "s0");
});

test("redelivered message is not submitted twice", async (t) => {
  const f = fixture(t);
  const message = { text: "你好", user: "alice", conversation: { kind: "private", id: "alice" }, messageId: "same-id" };
  await f.bridge.handleMessage(message);
  await f.bridge.handleMessage(message);
  assert.equal(f.messages.length, 1);
});

test("failed outbound reply stays pending for retry", async (t) => {
  const f = fixture(t);
  await f.send("你好");
  const original = f.bridge.chat.reply;
  f.bridge.chat.reply = async () => { throw new Error("network unavailable"); };
  await f.bridge.chat.poll();
  assert.equal(Object.keys(f.bridge.chat.state.pending).length, 1);
  f.bridge.chat.reply = original;
  await f.bridge.chat.poll();
  assert.equal(Object.keys(f.bridge.chat.state.pending).length, 0);
});

test("images are uploaded and attached to chat without creating issues", async (t) => {
  const f = fixture(t);
  let cleaned = false;
  f.bridge.chat.stageImages = async () => ({ paths: ["image.png"], failed: [], cleanup: async () => { cleaned = true; } });
  f.bridge.multica.uploadChatImage = async (id, path) => { assert.equal(id, "s0"); assert.equal(path, "image.png"); return { id: "att" }; };
  f.bridge.multica.sendChatMessage = async (id, content, ids) => {
    assert.deepEqual(ids, ["att"]);
    assert.match(content, /引用消息/);
    return { task_id: "image-task", attachment_ids: ids };
  };
  await f.send("引用消息：请看这张图片");
  assert.equal(cleaned, true);
  assert.equal(f.issues.length, 0);
});

test("group chat stays separate from private chat", async (t) => {
  const f = fixture(t);
  await f.send("私聊");
  await f.bridge.handleMessage({ text: "群聊", user: "alice", conversation: { kind: "group", id: "g" }, messageId: "group" });
  assert.equal(f.sessions.length, 2);
  await f.bridge.handleMessage({ text: "/status OTHER-2", user: "alice", conversation: { kind: "group", id: "g" }, messageId: "group-status" });
  assert.match(f.replies.at(-1).content, /群内仅能查询/);
});
