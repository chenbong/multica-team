import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { ChatRelay } from "./src/chat.js";
import { IssueRelay } from "./src/issue-relay.js";

const workspace = "11111111-1111-4111-8111-111111111111";
const agent = "22222222-2222-4222-8222-222222222222";
const delivery = "33333333-3333-4333-8333-333333333333";
const attachment = { id: "44444444-4444-4444-8444-444444444444", uploader_type: "agent", content_type: "image/png", filename: "generated.png", size_bytes: 10 };
const quiet = { warn() {}, log() {}, error() {} };
const rendered = "![generated](https://fixture.example.test/image.png)";

function config(t) {
  const directory = mkdtempSync(join(tmpdir(), "reply-image-wiring-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  return { robotId: "fixture", statePath: join(directory, "state.json"), pollIntervalMs: 100000,
    issueRelay: { enabled: true }, multica: { profile: "fixture", defaultAgent: agent, workspaceId: workspace,
      serverUrl: "http://127.0.0.1:9", webUrl: "http://localhost", workspaceSlug: "fixture" } };
}

test("Chat prepares generated image metadata once and freezes it across send failure and restart", async t => {
  const cfg = config(t); let prepares = 0, fail = true;
  const sent = [];
  const answer = { id: "answer", role: "assistant", task_id: "task", created_at: "2026-01-02T00:00:00Z", content: "Original reply", attachments: [attachment] };
  const api = { chatMessages: async () => [answer], downloadChatReplyImage: async () => Buffer.from("image") };
  const images = { prepare: async (body, metadata) => { prepares++; assert.equal(body, "Original reply"); assert.equal(metadata.createdAt, answer.created_at);
    assert.equal(metadata.workspaceId, workspace); assert.deepEqual(metadata.attachments, [attachment]);
    return { content: body + "\n\n" + rendered }; } };
  const reply = async (_c, _u, content) => { sent.push(content); if (fail) throw Error("network unavailable"); };
  const relay = new ChatRelay(cfg, api, reply, async () => {}, quiet, { replyImages: images });
  t.after(() => relay.stop());
  relay.state.pending.task = { sessionId: "session", conversation: { kind: "private", id: "alice" }, user: "alice", at: Date.now() };
  await relay.poll();
  assert.equal(prepares, 1);
  assert(relay.state.pending.task.replyProgress.imageReply.content.includes(rendered));
  relay.stop(); fail = false;
  const restored = new ChatRelay(cfg, api, reply, async () => {}, quiet, { replyImages: images });
  t.after(() => restored.stop());
  await restored.poll();
  assert.equal(prepares, 1);
  assert.deepEqual(sent, ["Original reply\n\n" + rendered, "Original reply\n\n" + rendered]);
  assert.equal(Object.keys(restored.state.pending).length, 0);
});

for (const kind of ["group", "private"]) test(`${kind} task delivery pins images across send retries and ACK failures`, async t => {
  const cfg = config(t); let prepares = 0, failSend = true, failAck = true, acked = false;
  const sent = [], checks = [];
  const notice = "\n\n图片：1 张，请打开任务链接查看。";
  const item = { id: delivery, author_type: "agent", content: "Task result" + notice, comment_id: "comment", comment_created_at: "2026-01-02T00:00:00Z",
    image_notice: notice, image_count: 1, image_attachments: [attachment], conversation: { kind, id: kind === "group" ? "42" : "alice" },
    requires_check: true, notify_users: kind === "group" ? ["alice"] : [], binding: { issue_id: "issue", identifier: "TEST-1", workspace_slug: "fixture" } };
  const images = { prepare: async (body, metadata) => { prepares++; assert.equal(body, "Task result"); await metadata.beforeTransfer(); return { content: body + "\n\n" + rendered }; } };
  const sender = async (_c, content, options) => { await options.beforeSend(); sent.push({ content, options }); if (failSend) throw Error("send failed"); return { messageid: "123", msgkey: "456" }; };
  const relay = new IssueRelay(cfg, {}, sender, quiet, { replyImages: images }); t.after(() => relay.stop());
  relay.request = async path => {
    if (path.endsWith("/check")) { checks.push(path); return { allowed: true }; }
    if (path.endsWith("/ack")) { if (failAck) throw Error("ack failed"); acked = true; return {}; }
    return { deliveries: acked ? [] : [item] };
  };
  await relay.poll(); assert.equal(prepares, 1);
  failSend = false; await relay.poll();
  assert.equal(prepares, 1); assert.equal(sent.length, 2); assert.equal(sent[0].content, sent[1].content);
  assert(sent[0].content.includes(rendered)); assert(!sent[0].content.includes("图片：1 张"));
  if (kind === "group") assert.deepEqual(sent[0].options.atUsers, ["alice"]);
  failAck = false; await relay.poll();
  assert.equal(sent.length, 2); assert.equal(acked, true); assert(checks.length >= 4);
});

test("a sync pause during image preparation prevents even custom transports from sending", async t => {
  const cfg = config(t); let allowed = true, sent = 0;
  const images = { prepare: async () => { allowed = false; return { content: rendered }; } };
  const relay = new IssueRelay(cfg, {}, async () => { sent++; }, quiet, { replyImages: images }); t.after(() => relay.stop());
  const item = { id: delivery, content: "reply", author_type: "agent", requires_check: true, comment_created_at: "2026-01-02T00:00:00Z",
    image_attachments: [attachment], conversation: { kind: "private", id: "alice" }, binding: { issue_id: "issue", identifier: "TEST-1", workspace_slug: "fixture" } };
  relay.request = async path => path.endsWith("/check") ? { allowed } : { deliveries: [item] };
  await relay.poll(); assert.equal(sent, 0);
});

test("human task mirrors never enter the generated-image publisher", async t => {
  const cfg = config(t); let sent = 0;
  const relay = new IssueRelay(cfg, {}, async () => { sent++; return { msgkey: "123" }; }, quiet,
    { replyImages: { prepare: async () => { throw Error("human mirror must not publish"); } } }); t.after(() => relay.stop());
  const item = { id: delivery, content: "User message", author_type: "user", author_name: "alice", image_attachments: [attachment],
    conversation: { kind: "private", id: "bob" }, binding: { issue_id: "issue", identifier: "TEST-1", workspace_slug: "fixture" } };
  relay.request = async path => path.endsWith("/ack") ? {} : { deliveries: [item] };
  await relay.poll(); assert.equal(sent, 1);
});
