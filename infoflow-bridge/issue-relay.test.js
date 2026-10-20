import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, rmSync, writeFileSync, readFileSync, readdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createHash } from "node:crypto";
import { Bridge } from "./src/bridge.js";
import { IssueRelay, relayOperation, splitMessage } from "./src/issue-relay.js";
import { withReplyMode } from "./src/reply-format.js";
import { sendGroupExact } from "./src/issue-groups.js";

const agent = "00000000-0000-4000-8000-000000000001";
const binding = { binding_id: "binding-1", issue_id: "issue", identifier: "TEST-1", workspace_slug: "team", generation: "generation-1" };
function fixture(t) {
  const dir = mkdtempSync(join(tmpdir(), "infoflow-binding-test-"));
  const config = { robotId: "robot", issueRelay: { enabled: true }, statePath: join(dir, "state.json"),
    multica: { cli: "/does-not-exist", defaultAgent: agent, workspaceId: "workspace", workspaceSlug: "team", serverUrl: "http://127.0.0.1:9", webUrl: "http://localhost:8004" },
    allowUsers: [], allowAllInGroups: true, pollIntervalMs: 100000 };
  const sent = [], requests = [], warnings = [];
  const bridge = new Bridge(config, async (conversation, content, options) => { sent.push({ conversation, content, options }); return { messageid: "123" }; },
    { log() {}, error() {}, warn(message) { warnings.push(message); } });
  bridge.issueRelay.request = async (path, body) => { requests.push({ path, body }); return { handled: true, silent: false, can_comment: true, binding }; };
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

test("group reply audiences are persisted with the original input and excluded from private requests", async t => {
  const f = fixture(t);
  await f.send("请处理并通知指定成员", { notifyUsers: ["BOB", "alice", "bob"], acknowledge: async () => true });
  const request = f.requests.find(x => x.path === "issue-relay");
  assert.deepEqual(request.body.notify_users, ["alice", "bob"]);
  assert.equal(request.body.content, "请处理并通知指定成员");
  f.requests.length = 0;
  await f.send("私聊内容", { conversation: { kind: "private", id: "alice" }, messageId: "private-one", notifyUsers: ["bob"], acknowledge: async () => true });
  assert.equal(f.requests.find(x => x.path === "issue-relay").body.notify_users, undefined);
});

test("inline human labels reach comments while task selection uses canonical text and a pinned issue", async t => {
  const f = fixture(t);
  await f.send("请处理，然后通知", { displayText: "请处理，然后通知 @TEST-999", notifyUsers: ["bob"] });
  assert.equal(f.requests[0].body.content, "请处理，然后通知");
  assert.equal(f.requests[1].body.content, "请处理，然后通知 @TEST-999");
  assert.equal(f.requests[1].body.operation, "message");
  assert.equal(f.requests[1].body.issue_ref, binding.issue_id);
  assert.equal(f.requests[1].body.expected_binding_id, binding.binding_id);
  assert.deepEqual(f.requests[1].body.notify_users, ["alice", "bob"]);
});

test("human labels do not alter help or status commands or become notification identities", async t => {
  const f = fixture(t);
  for (const command of ["help", "status"]) {
    await f.send("/" + command, { messageId: command, displayText: "/" + command + " @TEST-999" });
    const request = f.requests.at(-1).body;
    assert.equal(request.operation, "binding");
    assert.equal(request.command, command);
    assert.equal(request.issue_ref, undefined);
    assert.equal(request.content, "");
    assert.equal(request.notify_users, undefined);
  }
});

test("human display labels cannot resolve an otherwise ambiguous task selection", async t => {
  const f = fixture(t);
  f.bridge.issueRelay.request = async (path, body) => {
    f.requests.push({ path, body });
    assert.equal(body.operation, "binding");
    assert.equal(body.content, "继续");
    return { handled: true, silent: false, notice: "请指定任务编号" };
  };
  await f.send("继续", { displayText: "继续 @TEST-2" });
  assert.equal(f.requests.length, 1);
  assert.match(f.sent[0].content, /请指定任务编号/);
});

test("new issue instructions keep inline mentions after stripping the actual creation command", async t => {
  const f = fixture(t);
  await f.send("/issue 检查，然后通知", { displayText: "/issue 检查，然后通知 @小明" });
  assert.equal(f.requests.at(-1).body.operation, "create");
  assert.equal(f.requests.at(-1).body.content, "检查，然后通知 @小明");
});

for (const legacy of [false, true]) test(`display text survives durable retry without rewriting ${legacy ? "legacy" : "new"} prepared content`, async t => {
  const f = fixture(t);
  const calls = [];
  let failed = false;
  f.bridge.issueRelay.request = async (_path, body) => {
    calls.push(structuredClone(body));
    if (body.operation === "message" && !failed) { failed = true; throw new Error("lost response"); }
    return { handled: true, silent: false, can_comment: true, binding };
  };
  await f.send("通知", { displayText: "通知 @小明" });
  const [id] = Object.keys(f.bridge.issueRelay.inbox);
  const record = f.bridge.issueRelay.inbox[id];
  assert.equal(record.input.displayText, "通知 @小明");
  assert.equal(JSON.parse(readFileSync(f.bridge.issueRelay.inboxPath))[id].input.displayText, "通知 @小明");
  if (legacy) {
    record.preparedBody.content = "通知";
    delete record.preparedBody.issue_ref;
    f.bridge.issueRelay.saveInbox();
  }
  const prepared = structuredClone(record.preparedBody);
  f.bridge.stop();
  const restored = new IssueRelay(f.config, {}, async () => {}, { warn() {} });
  t.after(() => restored.stop());
  restored.request = f.bridge.issueRelay.request;
  await restored.handle({ ...record.input, displayText: "不得覆盖原请求 @other" });
  assert.deepEqual(calls.at(-1), prepared);
  assert.deepEqual(restored.inbox, {});
});

test("unbound Chat fallback also retains inline human labels without changing its routing input", async t => {
  const f = fixture(t), chat = [];
  f.bridge.issueRelay.request = async () => ({ handled: false, silent: false });
  f.bridge.chat.send = async (...args) => chat.push(args);
  await f.send("请通知", { displayText: "请通知 @小明", notifyUsers: ["bob"] });
  assert.equal(chat.length, 1);
  assert.equal(chat[0][2], "请通知 @小明");
  assert.deepEqual(chat[0][8], ["alice", "bob"]);
});

test("combined issue reply checkpoints once across send failure, restart and failed ACK", async t => {
  const f = fixture(t), calls = [];
  const item = { id: "mention-delivery", content: "**Reply**\n\n- Original content", comment_id: "c1", conversation: { kind: "group", id: "42" }, binding, notify_users: ["alice", "bob"] };
  let failAt = true, failAck = true, acked = false, acks = 0;
  const sender = async (conversation, content, options) => {
    calls.push({ conversation, content, options });
    if (failAt) throw Error("combined transport failed");
    return { messageid: "1234567890123456789" };
  };
  const request = async path => {
    if (path.endsWith("/ack")) { acks++; if (failAck) throw Error("ACK failed"); acked = true; return {}; }
    return { deliveries: acked ? [] : [item] };
  };
  const relay = f.bridge.issueRelay; relay.send = sender; relay.request = request;
  await relay.poll();
  assert.equal(relay.sentParts[item.id].next, 0);
  assert.deepEqual(relay.sentParts[item.id].notifyUsers, ["alice", "bob"]);
  assert.equal(acks, 0);
  f.bridge.stop(); failAt = false;
  const restored = new IssueRelay(f.config, {}, sender, { warn() {} }); restored.request = request;
  t.after(() => restored.stop());
  await restored.poll();
  assert.equal(restored.sentParts[item.id].notified, true);
  assert.equal(acks, 1);
  failAck = false; await restored.poll();
  assert.equal(acks, 2);
  assert.equal(calls.filter(x => !x.options.notificationOnly).length, 2);
  assert.equal(calls.filter(x => x.options.notificationOnly).length, 0);
  assert.deepEqual(calls[1].options.atUsers, ["alice", "bob"]);
  assert.match(calls[0].content, /\*\*Reply\*\*/);
  assert.equal(item.content, "**Reply**\n\n- Original content");
});

test("changed issue notification audiences pause retries instead of notifying a different person", async t => {
  const f = fixture(t);
  const item = { id: "changed-audience", content: "answer", conversation: { kind: "group", id: "42" }, binding, notify_users: ["alice"] };
  let bodies = 0, notices = 0;
  f.bridge.issueRelay.send = async (_c, _text, options) => {
    if (options.notificationOnly) { notices++; throw Error("unexpected TEXT"); }
    bodies++; throw Error("combined send failed");
  };
  f.bridge.issueRelay.request = async () => ({ deliveries: [item] });
  await f.bridge.issueRelay.poll();
  item.notify_users = ["mallory"]; await f.bridge.issueRelay.poll();
  assert.equal(bodies, 1); assert.equal(notices, 0);
});

test("issue combined delivery preserves ordinary links and ACK retry never resends", async t => {
  const f = fixture(t), wire = [];
  const original = "**结果**\n\n- [@小明](mention://member/member-id)\n- [文档](https://example.test/docs)";
  const item = { id: "plain-mention-delivery", content: original, comment_id: "c1", conversation: { kind: "group", id: "42" }, binding, notify_users: ["alice", "bob"] };
  const client = { httpClient: { post: async (_path, data) => {
    wire.push(JSON.parse(data).message);
    return { errcode: 0, data: { messageid: String(100 + wire.length) } };
  } } };
  f.bridge.issueRelay.send = (...args) => sendGroupExact(client, ...args);
  let failedAck = false, acked = false;
  f.bridge.issueRelay.request = async path => {
    if (path.endsWith("/ack")) {
      if (!failedAck) { failedAck = true; throw Error("lost ACK response"); }
      acked = true; return {};
    }
    return { deliveries: acked ? [] : [item] };
  };
  await f.bridge.issueRelay.poll();
  assert.equal(wire.length, 1);
  assert.equal(wire[0].header.msgtype, "MD");
  assert.match(wire[0].body[1].content, /\*\*结果\*\*\n\n- @小明\n- \[文档\]\(https:\/\/example.test\/docs\)/);
  assert.doesNotMatch(wire[0].body[1].content, /mention:\/\//);
  assert.match(wire[0].body[1].content, /\[完整对话链接\]\(http:\/\/localhost:8004\/team\/issues\/TEST-1#comment-c1\)/);
  assert.deepEqual(wire[0].body[0], { type: "AT", atall: false, atuserids: ["alice", "bob"] });
  assert.match(wire[0].body[1].content, /@alice @bob/);
  assert.equal(item.content, original);
  await f.bridge.issueRelay.poll();
  assert.equal(acked, true);
  assert.equal(wire.length, 1);
});

test("denied workspace user gets received reaction and owner notice but never starts Chat", async t => {
  const f = fixture(t);
  f.bridge.issueRelay.request = async () => ({ handled: true, silent: false, binding,
    notice: "没有工作区权限，无法评论，请向 workspace-owner 申请工作区权限" });
  let received = 0;
  await f.send("运行任务", { acknowledge: async () => { received++; return true; } });
  assert.equal(received, 1);
  assert.equal(f.sent.length, 1);
  assert.match(f.sent[0].content, /没有工作区权限/);
  assert.match(f.sent[0].content, /完整对话链接\]\(http:\/\/localhost:8004\/team\/issues\/TEST-1\)\*\*$/);
  assert.deepEqual(f.sent[0].options.atUsers || [], []);
});

test("comment-only result reports no invoke permission and never says run started", async t => {
  const f = fixture(t);
  f.bridge.issueRelay.request = async (_path, body) => body.operation === "binding" ? { handled: true, silent: false, can_comment: true, binding } : ({ handled: true, silent: false, binding, comment_id: "c1",
    notice: "没有智能体权限，当前仅新增评论，但无法调用智能体，请向 agent-owner 申请智能体权限" });
  await f.send("测试");
  assert.match(f.sent[0].content, /当前仅新增评论/);
  assert.doesNotMatch(f.sent[0].content, /正在回复|开始执行|@alice/);
});

test("both-permissions input sends exact real sender and source without owner credentials", async t => {
  const f = fixture(t);
  await f.send("请继续", { replyToMessageId: "99" });
  assert.equal(f.sent.length, 0);
  assert.equal(f.requests[0].body.operation, "binding");
  assert.deepEqual(f.requests[1].body, { robot_id: "robot", agent_id: agent, workspace_id: "workspace",
    conversation: { kind: "group", id: "42" }, sender: "alice", message_id: "100", operation: "message", content: "请继续", reply_to_message_id: "99",
    expected_binding_id: binding.binding_id, expected_generation: binding.generation });
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
  const mutations = f.requests.filter(request => request.body.operation === "message");
  assert.equal(mutations.length, 2);
  assert.deepEqual(mutations[0].body, mutations[1].body);
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
  assert.ok(f.sent[0].content.startsWith(withReplyMode(content, "issue", binding.identifier)));
  assert.deepEqual(f.sent[0].options.atUsers || [], []);
});

test("user mirror uses trusted username, original comment link and one 8192-unit summary without forced AT", async t => {
  const f = fixture(t);
  f.config.multica.issueWebUrl = "http://127.0.0.1:8004";
  const item = { id: "user-mirror", author_type: "user", author_name: "alice", author_agent_id: agent,
    content: "中文🙂".repeat(900), comment_id: "user-comment", binding, conversation: { kind: "private", id: "bob" } };
  const acks = [];
  f.bridge.issueRelay.request = async (path, body) => path.endsWith("/ack") ? (acks.push(body), {}) : { deliveries: [item] };
  await f.bridge.issueRelay.poll();
  assert.equal(f.sent.length, 1);
  assert.ok(f.sent[0].content.startsWith("**[任务模式] TEST-1**\n\n@alice：中文"));
  assert.ok(f.sent[0].content.endsWith("**[完整对话链接](http://127.0.0.1:8004/team/issues/TEST-1#comment-user-comment)**"));
  assert.ok(f.sent[0].content.length <= 8192);
  assert.deepEqual(f.sent[0].options.atUsers || [], []);
  assert.equal(acks.length, 1);
  assert.equal(item.content.startsWith("@"), false);
});

test("user mirror username is validated and pinned by digest across ACK retries", async t => {
  const f = fixture(t);
  const item = { id: "user-mirror", author_type: "user", author_name: "alice", content: "hello", binding,
    conversation: { kind: "private", id: "bob" } };
  let acks = 0;
  f.bridge.issueRelay.request = async path => {
    if (path.endsWith("/ack")) { acks++; throw new Error("fake ACK failure"); }
    return { deliveries: [item] };
  };
  await f.bridge.issueRelay.poll();
  item.author_name = "bob"; await f.bridge.issueRelay.poll();
  item.author_name = "forged\nusername"; await f.bridge.issueRelay.poll();
  item.author_type = "unknown"; await f.bridge.issueRelay.poll();
  assert.equal(f.sent.length, 1);
  assert.equal(acks, 1);
});

for (const [text, operation] of [["/bind TEST-1", "bind"], ["/issue Test", "create"], ["/unbind TEST-1", "unbind"]]) {
  test(`explicit durable ${operation} success suppresses legacy IM notice only`, async t => {
    const f = fixture(t);
    let suppressed = true;
    f.bridge.issueRelay.request = async (_path, body) => ({ handled: true, silent: false, can_comment: true, binding,
      notice: body.operation === "binding" ? "" : "mutation succeeded", suppress_im_notice: suppressed });
    await f.send(text, { conversation: { kind: "private", id: "alice" } });
    assert.equal(f.sent.length, 0);
    suppressed = false;
    await f.send(text, { conversation: { kind: "private", id: "alice" }, messageId: "101" });
    assert.equal(f.sent.length, 1);
    assert.match(f.sent[0].content, /mutation succeeded/);
  });
}

test("suppression never hides permission errors, pending admission or read-only binding status", async t => {
  const f = fixture(t);
  f.bridge.issueRelay.request = async () => ({ handled: true, silent: false, can_comment: false, binding,
    notice: "permission denied", suppress_im_notice: true });
  await f.send("/bind TEST-1");
  assert.match(f.sent[0].content, /permission denied/);
  f.bridge.issueRelay.request = async () => ({ handled: true, silent: false, can_comment: true,
    binding: { ...binding, group: { state: "pending" } }, notice: "admission pending", suppress_im_notice: true });
  await f.send("/bind TEST-1", { messageId: "101" });
  assert.match(f.sent[1].content, /admission pending/);
  f.bridge.issueRelay.request = async () => ({ handled: true, silent: false, can_comment: true, binding,
    notice: "current task binding", suppress_im_notice: true });
  await f.send("/binding", { messageId: "102" });
  assert.match(f.sent[2].content, /current task binding/);
});

test("ack failure and process restart do not resend a checkpointed single summary", async t => {
  const f = fixture(t);
  const item = { id: "delivery", content: "🙂中文".repeat(500), conversation: { kind: "private", id: "alice" }, binding };
  f.bridge.issueRelay.request = async path => { if (path.endsWith("/ack")) throw new Error("ack network failure"); return { deliveries: [item] }; };
  await f.bridge.issueRelay.poll();
  const sentCount = f.sent.length;
  assert.equal(sentCount, 1);
  assert.ok(f.sent.every(r => r.content.startsWith("**[任务模式] TEST-1**\n\n") && r.content.includes("**[完整对话链接](") && r.content.length <= 8192));
  assert.equal(f.bridge.issueRelay.sentParts.delivery.format, "combined-v2");
  f.bridge.issueRelay.stop();
  const restored = new IssueRelay(f.config, {}, async () => { throw new Error("unexpected duplicate send"); }, { warn() {} });
  t.after(() => restored.stop());
  let acked = false;
  restored.request = async path => { if (path.endsWith("/ack")) { acked = true; return {}; } return { deliveries: [item] }; };
  await restored.poll();
  assert.equal(acked, true);
  assert.deepEqual(restored.sentParts, {});
});

for (const kind of ["private", "group"]) test(`${kind} failed first summary send stays pending and restarts with one retry`, async t => {
  const f = fixture(t), acknowledgements = [];
  const item = { id: "delivery", content: "中文🙂".repeat(700), comment_id: "c1", binding,
    conversation: { kind, id: kind === "private" ? "alice" : "42" } };
  const request = async (path, body) => {
    if (path.endsWith("/ack")) { acknowledgements.push(body); return {}; }
    return { deliveries: [item] };
  };
  let attempts = 0;
  f.bridge.issueRelay.request = request;
  f.bridge.issueRelay.send = async () => { attempts++; throw new Error("send failed"); };
  await f.bridge.issueRelay.poll();
  assert.equal(attempts, 1);
  assert.equal(acknowledgements.length, 0);
  assert.equal(f.bridge.issueRelay.sentParts.delivery.next, 0);
  assert.equal(f.bridge.issueRelay.sentParts.delivery.format, "combined-v2");
  f.bridge.stop();
  const restored = new IssueRelay(f.config, {}, async (_c, text, options) => {
    attempts++; assert.ok(text.length <= 8192); assert.deepEqual(options.atUsers || [], []);
    assert.ok(text.endsWith("**[完整对话链接](http://localhost:8004/team/issues/TEST-1#comment-c1)**"));
    return kind === "private" ? { msgkey: "1836339350307196087" } : { messageid: "1836339350307196087" };
  }, { warn() {} });
  t.after(() => restored.stop()); restored.request = request;
  await restored.poll();
  assert.equal(attempts, 2);
  assert.equal(acknowledgements.length, 1);
  assert.deepEqual(acknowledgements[0].message_ids, ["1836339350307196087"]);
  assert.deepEqual(restored.sentParts, {});
});

test("summary checkpoint hashes the undisplayed tail and never acknowledges changed content", async t => {
  const f = fixture(t);
  const original = "中文🙂".repeat(800);
  const item = { id: "delivery", content: original, comment_id: "c1", binding, conversation: { kind: "private", id: "alice" } };
  let acks = 0;
  f.bridge.issueRelay.request = async path => {
    if (path.endsWith("/ack")) { acks++; throw new Error("ack unavailable"); }
    return { deliveries: [item] };
  };
  await f.bridge.issueRelay.poll();
  const checkpoint = structuredClone(f.bridge.issueRelay.sentParts.delivery);
  item.content += "未发尾部发生变化";
  await f.bridge.issueRelay.poll();
  assert.equal(f.sent.length, 1);
  assert.equal(acks, 1);
  assert.deepEqual(f.bridge.issueRelay.sentParts.delivery, checkpoint);
  item.content = original;
  await f.bridge.issueRelay.poll();
  assert.equal(f.sent.length, 1);
  assert.equal(acks, 2);
});

test("completed legacy checkpoint retries only Ack with every original exact message ID", async t => {
  const f = fixture(t);
  const item = { id: "delivery", content: "中文🙂".repeat(900), binding, conversation: { kind: "private", id: "alice" } };
  const parts = f.bridge.issueRelay.replyParts(item.content, binding);
  const ids = parts.map((_, index) => String(1836339350307196087n + BigInt(index)));
  const checkpoint = { next: parts.length, digest: createHash("sha256").update(parts.join("")).digest("hex"), messageIds: ids };
  f.bridge.issueRelay.sentParts.delivery = checkpoint; f.bridge.issueRelay.save();
  f.bridge.stop();
  const restored = new IssueRelay(f.config, {}, async () => assert.fail("completed legacy chunks must not resend"), { warn() {} });
  t.after(() => restored.stop());
  let ack;
  restored.request = async (path, body) => {
    if (path.endsWith("/ack")) { ack = body; throw new Error("ack failed"); }
    return { deliveries: [item] };
  };
  await restored.poll();
  assert.deepEqual(ack.message_ids, ids);
  assert.equal(ack.message_id, ids.at(-1));
  assert.deepEqual(restored.sentParts.delivery, checkpoint);
});

test("oversized fresh link never sends or acknowledges a reply that cannot fit its footer", async t => {
  const f = fixture(t);
  f.config.multica.issueWebUrl = "https://entry.example.test/" + "path".repeat(2500);
  const item = { id: "delivery", content: "正文", binding, conversation: { kind: "private", id: "alice" } };
  let acks = 0;
  f.bridge.issueRelay.request = async path => {
    if (path.endsWith("/ack")) { acks++; return {}; }
    return { deliveries: [item] };
  };
  await f.bridge.issueRelay.poll();
  assert.deepEqual(f.sent, []); assert.equal(acks, 0);
  assert.equal(f.bridge.issueRelay.sentParts.delivery.next, 0);
});

test("UTF-8 chunks reconstruct original content without splitting surrogate pairs", () => {
  const text = "# 标题\n🙂𝌆日本語\n".repeat(300);
  const parts = splitMessage(text);
  assert.equal(parts.join(""), text);
  assert.ok(parts.every(part => Buffer.byteLength(part) <= 1800 && !part.includes("�")));
});

test("private SDK msgkey is retained for quote routing of the single summary", async t => {
  const f = fixture(t), acknowledgements = [];
  const item = { id: "delivery", content: "中文🙂".repeat(500), conversation: { kind: "private", id: "alice" }, binding };
  const ids = ["1836339350307196087", "1836339350307196088", "1836339350307196089", "1836339350307196090"];
  let next = 0;
  f.bridge.issueRelay.send = async () => ({ msgkey: ids[next++] });
  f.bridge.issueRelay.request = async (path, body) => {
    if (path.endsWith("/ack")) { acknowledgements.push(body); return {}; }
    return { deliveries: [item] };
  };
  await f.bridge.issueRelay.poll();
  assert.equal(next, 1);
  assert.deepEqual(acknowledgements[0].message_ids, ids.slice(0, next));
  assert.equal(acknowledgements[0].message_id, ids[next - 1]);
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
  f.bridge.issueRelay.request = async (_path, body) => {
    if (body.operation === "binding") return { handled: true, silent: true, can_comment: true, binding };
    handled++; return { handled: true, silent: true };
  };
  await f.send("good", { conversation: { kind: "group", id: "43" }, messageId: "2000" });
  assert.equal(handled, 1);
});
test("a stale private delivery is checked again before sending and cannot block another target", async t => {
  const f=fixture(t);
  const stale={id:"stale-private",content:"private result",conversation:{kind:"private",id:"alice"},binding,requires_check:true};
  const valid={...stale,id:"valid-private",conversation:{kind:"private",id:"bob"}};
  const sent=[],acks=[];
  f.bridge.issueRelay.send=async(c,text)=>{sent.push(c.id);return {msgkey:"123"}};
  f.bridge.issueRelay.request=async path=>{
    if(path.endsWith("/check"))return {allowed:path.includes("valid-private")};
    if(path.endsWith("/ack")){acks.push(path);return {}}
    return {deliveries:[stale,valid]};
  };
  await f.bridge.issueRelay.poll();
  assert.deepEqual(sent,["bob"]);
  assert.equal(acks.length,1);
});
