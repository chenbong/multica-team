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
const footer = "\n\n**[完整任务对话](https://team.example.test/team/issues/TASK-1)**";

function fixture(t, kind, mode) {
  const dir = mkdtempSync(join(tmpdir(), "reply-mode-test-"));
  const sent = [], apiInputs = [];
  const config = { robotId: "robot", issueRelay: { enabled: true }, statePath: join(dir, "state.json"),
    allowUsers: ["alice"], allowAllInGroups: true, pollIntervalMs: 100000,
    multica: { cli: "/missing-fixture-cli", defaultAgent: agent, workspaceId: "workspace", workspaceSlug: "team", webUrl: "https://team.example.test", serverUrl: "https://api.example.test" } };
  const bridge = new Bridge(config, async (conversation, content, options) => { sent.push({ conversation, content, options }); return { messageid: "456" }; }, { log() {}, warn() {}, error() {} });
  const conversation = { kind, id: kind === "group" ? "42" : "alice" };
  const delivery = { id: "d1", content: answer, binding, conversation };
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
        if (body.operation === "message") apiInputs.push(body.content);
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
      const expected = withReplyMode(answer + (mode === "issue" ? footer : ""), mode, binding.identifier);
      assert.ok(f.sent.some(message => message.content === expected));
      assert.ok(f.sent.every(message => message.content.startsWith(mode === "chat" ? "**[聊天模式]**\n\n" : "**[任务模式] TASK-1**\n\n")));
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

test("bind, permission notices and successful unbind show the correct current mode", async t => {
  const f = fixture(t, "group", "issue");
  for (const notice of ["已绑定任务", "没有工作区权限，无法评论", "没有智能体权限，当前仅新增评论", "只有管理员可以解除绑定"]) {
    await f.bridge.issueRelay.sendNotice(f.conversation, { notice, binding, conversation_mode: "issue" });
    assert.equal(f.sent.at(-1).content, withReplyMode(notice + footer, "issue", binding.identifier));
  }
  await f.bridge.issueRelay.sendNotice(f.conversation, { notice: "已解除任务绑定", binding, conversation_mode: "chat" });
  assert.equal(f.sent.at(-1).content, withReplyMode("已解除任务绑定" + footer, "chat"));
  await f.bridge.issueRelay.sendNotice(f.conversation, { notice: "未绑定任务", conversation_mode: "chat" });
  assert.equal(f.sent.at(-1).content, withReplyMode("未绑定任务", "chat"));
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

test("delayed Chat replies keep Chat mode after the conversation is bound", async t => {
  const f = fixture(t, "private", "chat");
  await f.bridge.handleMessage({ conversation: f.conversation, user: "alice", text: "先前的聊天", messageId: "1" });
  await f.bridge.issueRelay.sendNotice(f.conversation, { notice: "已绑定任务", binding, conversation_mode: "issue" });
  await f.bridge.chat.poll();
  assert.equal(f.sent.at(-1).content, withReplyMode(answer, "chat"));
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
    s.content.endsWith("**[完整任务对话](http://192.0.2.17:8004/team/issues/TASK-1)**")));
  assert.deepEqual(f.bridge.issueRelay.sentParts, {});
  assert.ok(raw.every(p => p.endsWith("完整任务对话：https://team.example.test/team/issues/TASK-1")));
  assert.equal(f.delivery.content, "中文🙂".repeat(500));
});
