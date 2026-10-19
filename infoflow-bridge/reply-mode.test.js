import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { createHash } from "node:crypto";
import { Bridge } from "./src/bridge.js";
import { withReplyMode, noticeReplyMode } from "./src/reply-format.js";

const agent = "00000000-0000-4000-8000-000000000001";
const binding = { binding_id: "binding", generation: "generation", issue_id: "issue", identifier: "TASK-1", workspace_slug: "team" };
const answer = "已完成分析。\n\n**结论**：需要调整批量大小。";
const footer = "\n\n**[完整对话链接](https://team.example.test/team/issues/TASK-1)**";
const chatFooter = "\n\n**[完整对话链接](https://team.example.test/team/chat?session=chat)**";

function fixture(t, kind, mode) {
  const dir = mkdtempSync(join(tmpdir(), "reply-mode-test-"));
  const sent = [], apiInputs = [], receipts = new Set();
  const config = { robotId: "robot", issueRelay: { enabled: true }, statePath: join(dir, "state.json"),
    allowUsers: ["alice"], allowAllInGroups: true, pollIntervalMs: 100000,
    multica: { cli: "/missing-fixture-cli", defaultAgent: agent, workspaceId: "workspace", workspaceSlug: "team", webUrl: "https://team.example.test", serverUrl: "https://api.example.test" } };
  const bridge = new Bridge(config, async (conversation, content, options) => { sent.push({ conversation, content, options }); return { messageid: "456" }; }, { log() {}, warn() {}, error() {} });
  const conversation = { kind, id: kind === "group" ? "42" : "alice" };
  const delivery = { id: "d1", content: answer, comment_id: "c1", binding, conversation, notify_users: kind === "group" ? ["alice"] : [] };
  Object.assign(bridge.multica, {
    listAgents: async () => [{ id: agent, name: "fixture-agent" }],
    createChatSession: async () => ({ id: "chat", status: "active" }),
    chatSession: async () => ({ id: "chat", status: "active" }),
    sendChatMessage: async (_id, content) => { apiInputs.push(content); return { task_id: "t1" }; },
    chatMessages: async () => [{ role: "assistant", task_id: "t1", content: answer }],
  });
  bridge.issueRelay.request = async (path, body) => {
    if (path === "issue-relay") {
      if (mode === "issue") {
        if (body.operation === "message" && !receipts.has(body.message_id)) {
          receipts.add(body.message_id); apiInputs.push(body.content);
        }
        return { handled: true, silent: false, can_comment: true, binding, conversation_mode: "issue" };
      }
      return { handled: false, silent: false, conversation_mode: "chat" };
    }
    if (path === "issue-deliveries") return { deliveries: [delivery] };
    return { handled: true, silent: true };
  };
  t.after(() => { bridge.stop(); rmSync(dir, { recursive: true, force: true }); });
  return { bridge, sent, apiInputs, conversation, delivery };
}

for (const kind of ["private", "group"]) {
  for (const mode of ["chat", "issue"]) {
    test(`${kind} x ${mode}: IM label only, original platform content unchanged`, async t => {
      const f = fixture(t, kind, mode);
      await f.bridge.handleMessage({ conversation: f.conversation, user: "alice", text: "请分析问题", messageId: "123" });
      if (mode === "chat") await f.bridge.chat.poll();
      else await f.bridge.issueRelay.poll();
      const expected = withReplyMode(answer + (mode === "issue" ? footer.replace("TASK-1)", "TASK-1#comment-c1)") : chatFooter), mode, binding.identifier);
      assert.ok(f.sent.some(message => message.content === expected));
      assert.ok(f.sent.filter(message => !message.options.notificationOnly).every(message => message.content.startsWith(mode === "chat" ? "**[聊天模式]**\n\n" : "**[任务模式] TASK-1**\n\n")));
      assert.ok(f.sent.every(message => message.options.atUser === undefined));
      assert.deepEqual(f.apiInputs, ["请分析问题"]);
      assert.equal(f.delivery.content, answer);
    });
  }
}

test("mode comes from route metadata, not model-written labels", () => {
  assert.equal(withReplyMode("[聊天模式]\n原始引用", "issue"), "**[任务模式]**\n\n[聊天模式]\n原始引用");
  assert.throws(() => withReplyMode("text", "unknown"), /unknown/);
  assert.equal(noticeReplyMode({}), "chat");
  assert.equal(noticeReplyMode({ binding }), "issue");
  assert.equal(noticeReplyMode({ binding, conversation_mode: "chat" }), "chat");
  assert.equal(noticeReplyMode({ conversation_mode: "issue" }), "issue");
});

for (const kind of ["private", "group"]) for (const authorType of ["agent", "user"]) {
  test(`${kind} ${authorType} task delivery includes its authoritative title`, async t => {
    const f = fixture(t, kind, "issue");
    f.delivery.binding = { ...binding, title: "任务标题" };
    f.delivery.author_type = authorType;
    f.delivery.author_name = "alice";
    await f.bridge.issueRelay.poll();
    const body = f.sent.find(item => !item.options.notificationOnly);
    assert.ok(body.content.startsWith("**[任务模式] TASK-1:任务标题**\n\n"));
    assert.ok(body.content.includes(answer));
    if (authorType === "user") assert.ok(body.content.includes("@alice："));
    assert.equal(f.delivery.content, answer);
  });
}

test("renaming after Markdown was accepted retries only the existing AT reminder", async t => {
  const f = fixture(t, "group", "issue"), relay = f.bridge.issueRelay;
  f.delivery.binding = { ...binding, title: "旧任务名" };
  const send = relay.send;
  let failAt = true;
  relay.send = async (conversation, content, options) => {
    if (options.notificationOnly && failAt) throw new Error("fixture AT failure");
    return send(conversation, content, options);
  };
  await relay.poll();
  assert.equal(f.sent.filter(item => !item.options.notificationOnly).length, 1);
  f.delivery.binding.title = "改名后的任务";
  failAt = false;
  await relay.poll();
  assert.equal(f.sent.filter(item => !item.options.notificationOnly).length, 1);
  assert.equal(f.sent.filter(item => item.options.notificationOnly).length, 1);
  assert.deepEqual(relay.sentParts, {});
});

test("bind, permission notices and successful unbind show the correct current mode", async t => {
  const f = fixture(t, "group", "issue");
  for (const notice of ["已绑定任务", "没有工作区权限，无法评论", "没有智能体权限，当前仅新增评论", "只有管理员可以解除绑定"]) {
    await f.bridge.issueRelay.sendNotice(f.conversation, { notice, binding, conversation_mode: "issue" });
    assert.equal(f.sent.at(-1).content, withReplyMode(notice + footer, "issue", binding.identifier));
  }
  await f.bridge.issueRelay.sendNotice(f.conversation, { notice: "已解除任务绑定", binding, conversation_mode: "chat" });
  assert.equal(f.sent.at(-1).content, withReplyMode("已解除任务绑定" + footer, "chat"));
  await f.bridge.issueRelay.sendNotice(f.conversation, { notice: "未绑定任务", conversation_mode: "chat" });
  assert.equal(f.sent.at(-1).content, withReplyMode("未绑定任务\n\n**[完整对话链接](https://team.example.test/team/chat)**", "chat"));
});

test("label is added to every long reply part without invalidating an older send checkpoint", async t => {
  const f = fixture(t, "group", "issue");
  f.delivery.content = "中文🙂".repeat(600);
  const rawParts = f.bridge.issueRelay.replyParts(f.delivery.content, binding);
  assert.ok(rawParts.length > 2);
  f.bridge.issueRelay.sentParts.d1 = { next: 1, digest: createHash("sha256").update(rawParts.join("")).digest("hex"), messageIds: ["123"] };
  await f.bridge.issueRelay.poll();
  assert.deepEqual(f.sent.map(message => message.content), rawParts.slice(1).map(part => withReplyMode(f.bridge.issueRelay.presentReplyPart(part, binding), "issue", binding.identifier)));
  assert.ok(f.sent.every(message => Buffer.byteLength(message.content) <= 1900));
  assert.deepEqual(f.bridge.issueRelay.sentParts, {});
});

test("pre-upgrade golden checkpoint digest resumes exactly the remaining three of four chunks", async t => {
  const f = fixture(t, "private", "issue");
  f.delivery.content = "中文🙂".repeat(600);
  // Golden digest from the pre-upgrade 1800-byte algorithm and canonical URL,
  // not recomputed by the helper under test. comment_id was not in that digest.
  const digest = "d9fa3efea1ac0d7f737a2e452a269240253f74d01732e8b01089a7d4c3332604";
  f.bridge.issueRelay.sentParts.d1 = { next: 1, digest, messageIds: ["111"] };
  let ack;
  const request = f.bridge.issueRelay.request;
  f.bridge.issueRelay.request = async (path, body) => {
    if (path.endsWith("/ack")) { ack = body; throw new Error("Ack unavailable"); }
    return request(path, body);
  };
  await f.bridge.issueRelay.poll();
  assert.equal(f.sent.length, 3);
  assert.deepEqual(f.bridge.issueRelay.sentParts.d1, { next: 4, digest, messageIds: ["111", "456", "456", "456"] });
  assert.deepEqual(ack.message_ids, ["111", "456", "456", "456"]);
  await f.bridge.issueRelay.poll();
  assert.equal(f.sent.length, 3);
});

test("delayed Chat replies keep Chat mode after the conversation is bound", async t => {
  const f = fixture(t, "private", "chat");
  await f.bridge.handleMessage({ conversation: f.conversation, user: "alice", text: "先前的聊天", messageId: "1" });
  await f.bridge.issueRelay.sendNotice(f.conversation, { notice: "已绑定任务", binding, conversation_mode: "issue" });
  await f.bridge.chat.poll();
  assert.equal(f.sent.at(-1).content, withReplyMode(answer + chatFooter, "chat"));
});

test("configured task entry uses a bold clickable IP link and preserves legacy checkpoint digests", async t => {
  const f = fixture(t, "private", "issue");
  f.bridge.config.multica.issueWebUrl = "http://192.0.2.17:8004/";
  f.delivery.content = "中文🙂".repeat(500);
  const raw = f.bridge.issueRelay.replyParts(f.delivery.content, binding);
  const digest = createHash("sha256").update(raw.join("")).digest("hex");
  f.bridge.issueRelay.sentParts.d1 = { next: 1, digest, messageIds: ["123"] };
  await f.bridge.issueRelay.poll();
  assert.equal(f.sent.length, raw.length - 1);
  assert.ok(f.sent.every(s => s.content.startsWith("**[任务模式] TASK-1**\n\n") &&
    s.content.endsWith("**[完整对话链接](http://192.0.2.17:8004/team/issues/TASK-1)**")));
  assert.deepEqual(f.bridge.issueRelay.sentParts, {});
  assert.ok(raw.every(p => p.endsWith("完整任务对话：https://team.example.test/team/issues/TASK-1")));
  assert.equal(f.delivery.content, "中文🙂".repeat(500));
});

test("legacy oversized link configuration keeps the canonical target with a bold footer", t => {
  const f = fixture(t, "private", "issue");
  f.bridge.config.multica.issueWebUrl = "https://web.example.test/" + "long-path".repeat(80);
  const part = f.bridge.issueRelay.replyParts("正文", binding)[0];
  assert.equal(f.bridge.issueRelay.presentReplyPart(part, binding), "正文" + footer);
  f.bridge.config.multica.issueWebUrl = "https://user:password@web.example.test";
  assert.throws(() => f.bridge.issueRelay.presentReplyPart(part, binding), /invalid reply link URL/);
});

for (const kind of ["private", "group"]) {
  for (const mode of ["chat", "issue"]) {
    test(`${kind} x ${mode}: long fresh reply sends once with a precise link and full platform body`, async t => {
      const f = fixture(t, kind, mode);
      const content = "中文🙂🚀𝌆原文\n".repeat(700);
      const input = "请分析：" + content + "尾部";
      f.bridge.config.multica.issueWebUrl = "https://entry.example.test";
      f.delivery.content = content;
      f.bridge.multica.chatMessages = async () => [{ role: "assistant", task_id: "t1", content }];
      let received = 0;
      const message = { conversation: f.conversation, user: "alice", text: input, messageId: "123", acknowledge: async () => { received++; return true; } };
      await f.bridge.handleMessage(message);
      await f.bridge.handleMessage(message);
      const before = f.sent.length;
      if (mode === "chat") await f.bridge.chat.poll();
      else await f.bridge.issueRelay.poll();
      assert.equal(f.sent.length - before, kind === "group" ? 2 : 1);
      const reply = f.sent.slice(before).find(message => !message.options.notificationOnly);
      assert.ok(reply.content.startsWith(withReplyMode("中文🙂🚀𝌆原文\n", mode, binding.identifier)));
      assert.match(reply.content, /回复较长，全文请在下方链接查看/);
      const target = mode === "chat" ? "team/chat?session=chat" : "team/issues/TASK-1#comment-c1";
      assert.ok(reply.content.endsWith(`**[完整对话链接](https://entry.example.test/${target})**`));
      assert.ok(Buffer.byteLength(reply.content) <= 1800);
      assert.equal(Buffer.from(reply.content).toString("utf8"), reply.content);
      assert.deepEqual(reply.options, {});
      assert.deepEqual(f.apiInputs, [input]);
      assert.equal(received, 1);
      assert.equal(f.delivery.content, content);
    });
  }
}

test("comment-only permission notice links to its newly created comment", async t => {
  const f = fixture(t, "private", "issue");
  await f.bridge.issueRelay.sendNotice(f.conversation, { notice: "没有智能体权限，当前仅新增评论", binding, comment_id: "c2", conversation_mode: "issue" });
  assert.equal(f.sent.length, 1);
  assert.ok(f.sent[0].content.endsWith("**[完整对话链接](https://team.example.test/team/issues/TASK-1#comment-c2)**"));
});

test("Chat result retains its original session even after a reset changes the current thread", async t => {
  const f = fixture(t, "private", "chat");
  await f.bridge.handleMessage({ conversation: f.conversation, user: "alice", text: "先前的聊天", messageId: "1", acknowledge: async () => true });
  const key = f.bridge.chat.key(f.conversation, "alice");
  f.bridge.chat.state.threads[key] = { id: "different-session", agentId: agent };
  await f.bridge.chat.poll();
  assert.ok(f.sent.at(-1).content.endsWith(chatFooter.trimStart()));
  assert.equal(f.sent.at(-1).content.includes("different-session"), false);
});

test("Chat agent reply with missing session route metadata remains pending", async t => {
  const f = fixture(t, "private", "chat");
  await f.bridge.handleMessage({ conversation: f.conversation, user: "alice", text: "等待精确链接", messageId: "1", acknowledge: async () => true });
  f.bridge.config.multica.workspaceSlug = "";
  await f.bridge.chat.poll();
  assert.deepEqual(f.sent, []);
  assert.equal(Object.keys(f.bridge.chat.state.pending).length, 1);
  f.bridge.config.multica.workspaceSlug = "team";
  await f.bridge.chat.poll();
  assert.equal(f.sent.length, 1);
  assert.deepEqual(f.bridge.chat.state.pending, {});
});
