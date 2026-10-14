import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, rmSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { Bridge } from "./src/bridge.js";
import { acknowledgeReceipt } from "./src/reactions.js";

function fixture(t, kind) {
  const dir = mkdtempSync(join(tmpdir(), "receipt-handoff-"));
  const replies = [], calls = [], submissions = [];
  const config = { robotId: "robot", issueRelay: { enabled: true }, statePath: join(dir, "state"),
    allowUsers: ["alice"], allowAllInGroups: true, pollIntervalMs: 100000,
    multica: { cli: "/missing-fixture-cli", defaultAgent: "agent", workspaceId: "ws", webUrl: "https://web.example.test" } };
  const client = { im: { message: { addEmojiReply: async options => { calls.push(options); return { success: true }; } } } };
  const make = () => {
    const b = new Bridge(config, async (_c, content) => replies.push(content), { log() {}, warn() {}, error() {} },
      { acknowledgeReceipt: o => acknowledgeReceipt(client, o) });
    b.issueRelay.request = async path => path === "issue-deliveries" ? { deliveries: [] } : { handled: false, silent: false, conversation_mode: "chat" };
    Object.assign(b.multica, { listAgents: async () => [{ id: "a", name: "agent" }],
      createChatSession: async () => ({ id: "chat", status: "active" }),
      sendChatMessage: async (_id, content) => { submissions.push(content); return { task_id: "task" }; },
      chatSession: async () => ({ status: "active" }), chatMessages: async () => [],
    });
    t.after(() => b.stop());
    return b;
  };
  const b = make();
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const receipt = { fromUid: "alice", chatType: kind === "private" ? 7 : 2, chatId: "12",
    baseMsgId: "1836339350307196087", msgId2: "300010063", replyContent: "d101", replyDesc: "/收到" };
  const message = { conversation: { kind, id: kind === "private" ? "alice" : "12" }, user: "alice", text: "hello",
    messageId: receipt.baseMsgId, receipt };
  return { b, make, replies, calls, submissions, message };
}

for (const kind of ["private", "group"]) {
  test(`${kind} Chat handoff preserves live receipt without posting a text acknowledgement`, async t => {
    const f = fixture(t, kind); let acked = 0;
    await f.b.handleMessage({ ...f.message, acknowledge: async () => { acked++; return true; } });
    await f.b.handleMessage(f.message);
    assert.equal(acked, 1);
    assert.equal(f.submissions.length, 1);
    assert.deepEqual(f.replies, []);
  });

  test(`${kind} durable recovery restores the receipt descriptor, not a function or credential`, async t => {
    const f = fixture(t, kind);
    f.b.issueRelay.request = async () => { throw new Error("temporary network outage"); };
    await f.b.handleMessage({ ...f.message, acknowledge: async () => { throw new Error("old process callback must not be used"); } });
    const saved = readFileSync(f.b.issueRelay.inboxPath, "utf8");
    assert.doesNotMatch(saved, /acknowledge|appSecret|appKey|token/);
    assert.match(saved, /1836339350307196087/);
    f.b.stop(); const recovered = f.make();
    await recovered.issueRelay.poll();
    assert.deepEqual(f.calls, [f.message.receipt]);
    assert.deepEqual(f.submissions, ["hello"]);
    assert.deepEqual(f.replies, []);
    assert.deepEqual(recovered.issueRelay.inbox, {});
  });

  test(`${kind} denied admission never reacts or starts Chat`, async t => {
    const f = fixture(t, kind);
    f.b.issueRelay.request = async () => ({ handled: true, silent: false, notice: "没有工作区权限" });
    await f.b.handleMessage({ ...f.message, acknowledge: async () => { throw new Error("must not react"); } });
    assert.equal(f.submissions.length, 0); assert.equal(f.calls.length, 0);
  });
}

test("recovered receipt rebuilds only the fixed emoji operation and rejects invalid IDs", async () => {
  const sent = [];
  const c = { im: { message: { addEmojiReply: async o => { sent.push(o); return { success: true }; } } } };
  const o = { fromUid: "alice", chatType: 7, chatId: "123", baseMsgId: "456", msgId2: "789" };
  assert.equal(await acknowledgeReceipt(c, { ...o, replyContent: "evil", token: "must-not-send" }), true);
  assert.equal(sent[0].replyContent, "d101"); assert.equal(sent[0].token, undefined);
  for (const extra of [{ chatId: "https://evil.test" }, { baseMsgId: "1e20" }, { fromUid: "alice|bob" }, { chatType: 999 }]) {
    assert.equal(await acknowledgeReceipt(c, { ...o, ...extra }), false);
  }
  assert.equal(sent.length, 1);
});
