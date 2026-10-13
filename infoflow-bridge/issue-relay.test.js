import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, rmSync, writeFileSync, readFileSync, readdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Bridge } from "./src/bridge.js";
import { IssueRelay, relayOperation, splitMessage } from "./src/issue-relay.js";
import { withReplyMode } from "./src/reply-format.js";

const agent = "00000000-0000-4000-8000-000000000001";
const binding = { issue_id: "issue", identifier: "TEST-1", workspace_slug: "team", generation: 1 };
function fixture(t) {
  const dir = mkdtempSync(join(tmpdir(), "infoflow-binding-test-"));
  const config = { robotId: "robot", issueRelay: { enabled: true }, statePath: join(dir, "state.json"),
    multica: { cli: "/does-not-exist", defaultAgent: agent, workspaceId: "workspace", workspaceSlug: "team", serverUrl: "http://127.0.0.1:9", webUrl: "http://localhost:8004" },
    allowUsers: [], allowAllInGroups: true, pollIntervalMs: 100000 };
  const sent = [], requests = [], warnings = [];
  const bridge = new Bridge(config, async (conversation, content, options) => { sent.push({ conversation, content, options }); return { messageid: "123" }; },
    { log() {}, error() {}, warn(message) { warnings.push(message); } });
  bridge.issueRelay.request = async (path, body) => { requests.push({ path, body }); return { handled: true, silent: false, binding }; };
  bridge.chat.send = async () => { throw new Error("unexpected Chat fallback"); };
  t.after(() => { bridge.stop(); rmSync(dir, { recursive: true, force: true }); });
  return { bridge, config, dir, sent, requests, warnings,
    send: (text, extra = {}) => bridge.handleMessage({ conversation: { kind: "group", id: "42" }, user: "alice", text, messageId: "100", ...extra }) };
}

test("relay command parsing keeps explicit issue creation and fixed binding operations", () => {
  assert.equal(relayOperation("/bind TEST-1").issue_ref, "TEST-1");
  assert.equal(relayOperation("/binding").operation, "binding");
  assert.equal(relayOperation("/unbind").operation, "unbind");
  assert.equal(relayOperation("/issue 修复").operation, "create");
  assert.equal(relayOperation("帮我创建一个任务：修复").content, "修复");
  assert.equal(relayOperation("不要创建任务").operation, "message");
  assert.equal(relayOperation("/agent private-agent").operation, "binding");
});

test("denied workspace user gets owner notice and issue footer, no receipt or Chat", async t => {
  const f = fixture(t);
  f.bridge.issueRelay.request = async () => ({ handled: true, silent: false, binding,
    notice: "没有工作区权限，无法评论，请向 workspace-owner 申请工作区权限" });
  await f.send("运行任务", { acknowledge: () => { throw new Error("must not acknowledge"); } });
  assert.equal(f.sent.length, 1);
  assert.match(f.sent[0].content, /没有工作区权限/);
  assert.match(f.sent[0].content, /完整任务对话：http:\/\/localhost:8004\/team\/issues\/TEST-1$/);
  assert.deepEqual(f.sent[0].options, {});
});

test("comment-only result reports no invoke permission and never says run started", async t => {
  const f = fixture(t);
  f.bridge.issueRelay.request = async () => ({ handled: true, silent: false, binding, comment_id: "c1",
    notice: "没有智能体权限，当前仅新增评论，但无法调用智能体，请向 agent-owner 申请智能体权限" });
  await f.send("测试");
  assert.match(f.sent[0].content, /当前仅新增评论/);
  assert.doesNotMatch(f.sent[0].content, /正在回复|开始执行|@alice/);
});

test("both-permissions input sends exact real sender and source without owner credentials", async t => {
  const f = fixture(t);
  await f.send("请继续", { replyToMessageId: "99" });
  assert.equal(f.sent.length, 0);
  assert.deepEqual(f.requests[0].body, { robot_id: "robot", agent_id: agent, workspace_id: "workspace",
    conversation: { kind: "group", id: "42" }, sender: "alice", message_id: "100", operation: "message", content: "请继续", reply_to_message_id: "99" });
});

test("private user scope and group scope are forwarded independently", async t => {
  const f = fixture(t);
  await f.send("/bind TEST-1");
  await f.send("/bind TEST-2", { conversation: { kind: "private", id: "alice" }, messageId: "101" });
  assert.deepEqual(f.requests.map(r => r.body.conversation), [{ kind: "group", id: "42" }, { kind: "private", id: "alice" }]);
});

test("timeouts and malformed permissions fail closed without legacy fallthrough", async t => {
  const f = fixture(t);
  for (const result of [null, {}, { handled: false }, { handled: true, silent: "false" }]) {
    f.bridge.issueRelay.request = async () => result;
    await f.send("/issue never-create");
  }
  f.bridge.issueRelay.request = async () => { throw new Error("timeout"); };
  await f.send("hello");
  assert.equal(f.sent.length, 0);
  assert.equal(f.warnings.length, 5);
});

test("unbound normal messages fall back to Chat while explicit binding lookup is answered", async t => {
  const f = fixture(t);
  f.bridge.issueRelay.request = async () => ({ handled: false, silent: false });
  const chat = [];
  f.bridge.chat.send = async (...args) => chat.push(args);
  await f.send("普通消息");
  await f.send("/binding", { messageId: "101" });
  assert.equal(chat.length, 1);
  assert.match(f.sent[0].content, /未绑定任务/);
});

test("retry reuses the event ID rather than pre-marking it as handled locally", async t => {
  const f = fixture(t);
  await f.send("同一条消息");
  await f.send("同一条消息");
  assert.equal(f.requests.length, 2);
  assert.equal(f.requests[0].body.message_id, f.requests[1].body.message_id);
});

test("missing and unsafe inbound message IDs do not write or fall through", async t => {
  const f = fixture(t);
  await f.send("hello", { messageId: undefined });
  await f.send("hello", { messageId: 1836339350307196087 });
  assert.equal(f.requests.length, 0);
});

test("media is not downloaded when permission preflight denies commenting", async t => {
  const f = fixture(t);
  f.bridge.issueRelay.stageImages = async () => { throw new Error("must not download"); };
  f.bridge.issueRelay.request = async () => ({ handled: true, silent: false, can_comment: false, binding, notice: "没有工作区权限" });
  await f.send("图片", { images: [{ content: "image" }] });
  assert.match(f.sent[0].content, /没有工作区权限/);
});

test("authorized media is encoded for the attributed backend upload and always cleaned", async t => {
  const f = fixture(t);
  const path = join(f.dir, "picture.png");
  writeFileSync(path, Buffer.from([1, 2, 3]));
  let cleaned = false;
  f.bridge.issueRelay.stageImages = async () => ({ paths: [path], failed: [], cleanup: async () => { cleaned = true; } });
  f.bridge.issueRelay.request = async (path, body) => { f.requests.push({ path, body }); return { handled: true, silent: false, can_comment: true, binding }; };
  await f.send("图片", { images: [{}] });
  assert.equal(f.requests[0].body.operation, "binding");
  assert.deepEqual(f.requests[1].body.attachments, [{ filename: "picture.png", content_type: "image/png", data_base64: "AQID" }]);
  assert.equal(cleaned, true);
});

test("new issue media requires an explicit creation preflight even in an unbound chat", async t => {
  const f = fixture(t);
  f.bridge.issueRelay.stageImages = async () => { throw new Error("unauthorized create must not download media"); };
  f.bridge.issueRelay.request = async (_path, body) => {
    assert.equal(body.operation, "binding");
    assert.equal(body.command, "create");
    return { handled: true, silent: false, can_comment: false, notice: "没有工作区权限，无法评论" };
  };
  await f.send("/issue 分析图片", { images: [{}] });
  assert.match(f.sent[0].content, /没有工作区权限/);
});

test("outbound replies preserve content, append footer, have no @ and ack only after send", async t => {
  const f = fixture(t);
  const content = "原始回复\n**结果**：✅\n";
  const item = { id: "delivery", content, conversation: { kind: "group", id: "42" }, binding };
  let acked = false;
  f.bridge.issueRelay.request = async (path) => {
    if (path.endsWith("/ack")) { assert.equal(f.sent.length, 1); acked = true; return {}; }
    return { deliveries: acked ? [] : [item] };
  };
  await f.bridge.issueRelay.poll();
  await f.bridge.issueRelay.poll();
  assert.equal(f.sent.length, 1);
  assert.ok(f.sent[0].content.startsWith(withReplyMode(content, "issue")));
  assert.deepEqual(f.sent[0].options, {});
});

test("ack failure and process restart do not re-send already checkpointed chunks", async t => {
  const f = fixture(t);
  const item = { id: "delivery", content: "🙂中文".repeat(500), conversation: { kind: "private", id: "alice" }, binding };
  f.bridge.issueRelay.request = async path => { if (path.endsWith("/ack")) throw new Error("ack network failure"); return { deliveries: [item] }; };
  await f.bridge.issueRelay.poll();
  const sentCount = f.sent.length;
  assert.ok(sentCount > 1);
  assert.ok(f.sent.every(r => r.content.startsWith("[任务模式]\n\n") && r.content.includes("完整任务对话：") && Buffer.byteLength(r.content) <= 1800 + Buffer.byteLength(withReplyMode("", "issue"))));
  f.bridge.issueRelay.stop();
  const restored = new IssueRelay(f.config, {}, async () => { throw new Error("unexpected duplicate send"); }, { warn() {} });
  t.after(() => restored.stop());
  let acked = false;
  restored.request = async path => { if (path.endsWith("/ack")) { acked = true; return {}; } return { deliveries: [item] }; };
  await restored.poll();
  assert.equal(acked, true);
  assert.deepEqual(restored.sentParts, {});
});

test("UTF-8 chunks reconstruct original content without splitting surrogate pairs", () => {
  const text = "# 标题\n🙂𝌆日本語\n".repeat(300);
  const parts = splitMessage(text);
  assert.equal(parts.join(""), text);
  assert.ok(parts.every(part => Buffer.byteLength(part) <= 1800 && !part.includes("�")));
});

test("empty reply still delivers the task link instead of silently acknowledging nothing", t => {
  const f = fixture(t);
  assert.deepEqual(f.bridge.issueRelay.replyParts("", binding), ["完整任务对话：http://localhost:8004/team/issues/TEST-1"]);
});

test("service client requires explicit credential, rejects redirects and never uses profile token", async t => {
  const f = fixture(t);
  let called = 0;
  const relay = new IssueRelay(f.config, {}, () => {}, console, { token: "x".repeat(32), fetchImpl: async (url, opts) => {
    called++;
    assert.equal(url, "http://127.0.0.1:9/api/integrations/infoflow/issue-relay");
    assert.equal(opts.redirect, "error");
    assert.equal(opts.headers.Authorization, "Bearer " + "x".repeat(32));
    return { ok: true, json: async () => ({ handled: true, silent: true }) };
  } });
  t.after(() => relay.stop());
  await relay.request("issue-relay", {});
  relay.token = "";
  await assert.rejects(relay.request("issue-relay", {}), /credential/);
  assert.equal(called, 1);
});

test("recovered Chat handoff failure stays pending and never poisons message deduplication", async t => {
  const f = fixture(t);
  f.bridge.issueRelay.request = async path => path === "issue-relay" ? { handled: false, silent: false } : { deliveries: [] };
  f.bridge.chat.send = async () => { throw new Error("temporary read failure before submission"); };
  await f.send("恢复普通聊天");
  assert.equal(Object.keys(f.bridge.issueRelay.inbox).length, 1);
  let attempts = 0;
  f.bridge.chat.send = async () => { attempts++; };
  await f.bridge.issueRelay.poll();
  assert.equal(attempts, 1);
  assert.equal(Object.keys(f.bridge.issueRelay.inbox).length, 0);
  await f.send("恢复普通聊天");
  assert.equal(attempts, 1);
});

test("uncertain Chat submission is retained for review and not automatically resubmitted", async t => {
  const f = fixture(t);
  f.bridge.issueRelay.request = async path => path === "issue-relay" ? { handled: false, silent: false } : { deliveries: [] };
  let submissions = 0;
  f.bridge.chat.send = async (...args) => { args[7].beforeSubmit(); submissions++; throw new Error("transport lost after POST"); };
  await f.send("不重复启动任务");
  await f.bridge.issueRelay.poll();
  await f.send("不重复启动任务");
  assert.equal(submissions, 1);
  const files = readdirSync(f.bridge.issueRelay.quarantinePath);
  assert.equal(files.length, 1);
  assert.equal(JSON.parse(readFileSync(join(f.bridge.issueRelay.quarantinePath, files[0]), "utf8")).input.text, "不重复启动任务");
  assert.equal(Object.keys(f.bridge.issueRelay.inbox).length, 0);
});

test("a failed Chat receipt after task persistence does not resubmit or quarantine the task", async t => {
  const f = fixture(t);
  f.bridge.issueRelay.request = async path => path === "issue-relay" ? { handled: false, silent: false } : { deliveries: [] };
  let submissions = 0;
  f.bridge.chat.send = async (...args) => { args[7].beforeSubmit(); submissions++; args[7].afterSubmit(); throw new Error("receipt send failed"); };
  await f.send("已经接受的任务");
  await f.bridge.issueRelay.poll();
  await f.send("已经接受的任务");
  assert.equal(submissions, 1);
  assert.equal(Object.keys(f.bridge.issueRelay.inbox).length, 0);
});

test("more than 500 permanently invalid inputs do not occupy the normal inbox capacity", async t => {
  const f = fixture(t);
  f.bridge.issueRelay.request = async () => { const error = new Error("conflicting source"); error.status = 409; throw error; };
  for (let i = 0; i < 501; i++) await f.send("bad", { messageId: String(1000 + i) });
  assert.equal(Object.keys(f.bridge.issueRelay.inbox).length, 0);
  assert.equal(readdirSync(f.bridge.issueRelay.quarantinePath).length, 501);
  let handled = 0;
  f.bridge.issueRelay.request = async () => { handled++; return { handled: true, silent: true }; };
  await f.send("good", { conversation: { kind: "group", id: "43" }, messageId: "2000" });
  assert.equal(handled, 1);
});
