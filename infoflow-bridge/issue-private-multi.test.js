import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, rmSync, readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { IssueRelay } from "./src/issue-relay.js";

const A = "11111111-1111-4111-8111-111111111111";
const B = "22222222-2222-4222-8222-222222222222";
const quote = "1859897297556589399";
const quiet = { warn() {}, log() {}, error() {} };
const bound = n => ({ binding_id: `binding-${n}`, generation: `generation-${n}`, issue_id: `issue-${n}`,
  identifier: `INFE-${n}`, workspace_slug: "team", assignment_version: "0", private: { state: "active", reason: "" } });

// This fake service mirrors the unified routing protocol, not SQL or permission
// implementation. Backend tests own those rules; JS tests own scope and retries.
function fixture(t) {
  const dir = mkdtempSync(join(tmpdir(), "infoflow-private-multi-"));
  const state = { bindings: [1, 2].map(n => ({ scope: ["a", "ws", A, "alice"], binding: bound(n) })),
    requests: [], comments: [], sent: [], chat: [], quotes: new Map([[quote, "INFE-1"]]),
    failSubmit: false, failUnbind: false, failedBindings: new Set() };
  const relays = [];
  const response = (binding, extra = {}) => ({ handled: true, silent: false, can_comment: true,
    conversation_mode: "issue", binding: structuredClone(binding), ...extra });
  const request = async (path, body) => {
    state.requests.push({ path, body: structuredClone(body) });
    if (path === "issue-deliveries") return { deliveries: [] };
    assert.equal(path, "issue-relay");
    assert.equal(body.conversation.kind, "private");
    const matches = state.bindings.filter(entry => JSON.stringify(entry.scope) ===
      JSON.stringify([body.robot_id, body.workspace_id, body.agent_id, body.sender]));
    // The real backend verifies current assignment. A failed private bind keeps
    // existing associations, and never creates a comment or invokes a squad.
    if (body.operation === "bind") {
      if (state.bindDenied) return { handled: true, silent: false, conversation_mode: "issue", can_comment: false,
        notice: "私聊仅支持由单个智能体负责的任务，请绑定群聊。" };
      let selected = matches.find(entry => entry.binding.identifier === body.issue_ref);
      if (!selected) {
        const n = Number(body.issue_ref.split("-").at(-1));
        selected = { scope: [body.robot_id, body.workspace_id, body.agent_id, body.sender], binding: bound(n) };
        state.bindings.push(selected);
      } else if (selected.binding.private.state === "needs_rebind") {
        selected.binding.private = { state: "active", reason: "" };
        selected.binding.generation += "-explicit-rebind";
        selected.binding.assignment_version = "2";
      }
      return response(selected.binding, { notice: "已添加任务绑定" });
    }
    if (!matches.length) return { handled: false, silent: false, conversation_mode: "chat" };
    const explicit = body.issue_ref || String(body.content).match(/\bINFE-\d+\b/)?.[0];
    const quoted = state.quotes.get(body.reply_to_message_id);
    const ref = body.command ? explicit || quoted : quoted || explicit;
    let selected = body.expected_binding_id ? matches.find(entry => entry.binding.binding_id === body.expected_binding_id) :
      ref ? matches.find(entry => entry.binding.identifier === ref) : matches.length === 1 ? matches[0] : null;
    if (body.expected_binding_id && (!selected || selected.binding.generation !== body.expected_generation)) {
      throw Object.assign(new Error("binding changed"), { status: 409 });
    }
    if (!selected) return { handled: true, silent: false, conversation_mode: "issue", can_comment: false,
      notice: quoted === "foreign" ? "引用消息不属于当前如流对话。" : `请指定任务：${matches.map(entry => entry.binding.identifier).join("、")}。` };
    if (selected.binding.private.state === "needs_rebind" && !["unbind"].includes(body.command || body.operation)) {
      return response(selected.binding, { can_comment: false, notice: "任务负责人已变更，请重新绑定。" });
    }
    if (body.operation === "message") {
      if (state.failSubmit || state.failedBindings.has(selected.binding.binding_id)) throw new Error("submit outcome unknown");
      assert.equal(body.expected_binding_id, selected.binding.binding_id);
      assert.equal(body.expected_generation, selected.binding.generation);
      state.comments.push(structuredClone(body)); return response(selected.binding, { silent: true });
    }
    if (body.operation === "unbind") {
      if (state.failUnbind) throw new Error("unbind outcome unknown");
      assert.equal(body.expected_binding_id, selected.binding.binding_id);
      assert.equal(body.expected_generation, selected.binding.generation);
      state.bindings = state.bindings.filter(entry => entry !== selected);
      return response(selected.binding, { notice: "已解除此任务绑定", conversation_mode: matches.length === 1 ? "chat" : "issue" });
    }
    return response(selected.binding);
  };
  const make = ({ robot = "a", workspace = "ws", agent = A } = {}) => {
    const relay = new IssueRelay({ robotId: robot, statePath: join(dir, "state"), issueRelay: { enabled: true }, pollIntervalMs: 3600000,
      multica: { defaultAgent: agent, workspaceId: workspace, workspaceSlug: "team", webUrl: "https://team.example.invalid" } }, {},
    async (conversation, content, options) => state.sent.push({ robot, conversation, content, options }), quiet);
    relay.request = request;
    relay.fallback = async input => state.chat.push(input);
    relay.stageImages = () => assert.fail("no unselected or stale media staging");
    relays.push(relay); return relay;
  };
  const input = (text, extra = {}) => ({ conversation: { kind: "private", id: "alice" }, user: "alice", text, messageId: "100", ...extra });
  t.after(() => { relays.forEach(relay => relay.stop()); rmSync(dir, { recursive: true, force: true }); });
  return { state, make, input, dir };
}

test("private multiissue ambiguity asks once without Chat, comments or image staging", async t => {
  const f = fixture(t), relay = f.make(), message = f.input("继续", { images: [{}] });
  await relay.handle(message); await relay.handle(message);
  assert.equal(f.state.requests.length, 1); assert.equal(f.state.comments.length, 0); assert.equal(f.state.chat.length, 0);
  assert.match(f.state.sent[0].content, /^\*\*\[任务模式\]\*\*\n\n请指定任务：INFE-1、INFE-2/);
  assert.doesNotMatch(f.state.sent[0].content, /完整任务对话/);
});

for (const { text, replyTo, ref } of [
  { text: "INFE-2 继续", replyTo: quote, ref: "INFE-1" },
  { text: "INFE-2 继续", ref: "INFE-2" },
]) test(`private selected ${ref} preserves original content and exact quote`, async t => {
  const f = fixture(t), relay = f.make();
  await relay.handle(f.input(text, { replyToMessageId: replyTo }));
  assert.equal(f.state.comments.length, 1); const mutation = f.state.comments[0];
  assert.equal(mutation.expected_binding_id, ref === "INFE-1" ? "binding-1" : "binding-2");
  assert.equal(mutation.content, text); assert.equal(mutation.reply_to_message_id, replyTo);
  assert.equal(f.state.requests[0].body.operation, "binding"); assert.equal(f.state.sent.length, 0);
});

test("private unique association routes only that issue, known foreign quote refuses instead", async t => {
  const f = fixture(t), relay = f.make(); f.state.bindings.pop();
  await relay.handle(f.input("继续")); assert.equal(f.state.comments.length, 1);
  f.state.quotes.set("foreign-message", "foreign");
  await relay.handle(f.input("继续", { messageId: "101", replyToMessageId: "foreign-message" }));
  assert.equal(f.state.comments.length, 1); assert.match(f.state.sent[0].content, /引用消息不属于当前如流对话/);
  assert.equal(f.state.chat.length, 0);
});

test("private bind adds one association instead of replacing the current task", async t => {
  const f = fixture(t), relay = f.make(); await relay.handle(f.input("/bind INFE-3"));
  assert.deepEqual(f.state.bindings.map(entry => entry.binding.identifier), ["INFE-1", "INFE-2", "INFE-3"]);
  assert.equal(f.state.comments.length, 0); assert.equal(f.state.chat.length, 0);
  assert.match(f.state.sent[0].content, /^\*\*\[任务模式\] INFE-3\*\*\n\n已添加任务绑定/);
  assert.match(f.state.sent[0].content, /完整任务对话\]\(https:\/\/team.example.invalid\/team\/issues\/INFE-3\)\*\*/);
});

for (const command of ["binding", "unbind"]) test(`private /${command} selects explicitly and asks on multiple candidates`, async t => {
  const f = fixture(t), relay = f.make(); await relay.handle(f.input(`/${command}`));
  assert.equal(f.state.bindings.length, 2); assert.match(f.state.sent[0].content, /请指定任务/);
  await relay.handle(f.input(`/${command} INFE-2`, { replyToMessageId: quote, messageId: "101" }));
  assert.equal(f.state.comments.length, 0); assert.equal(f.state.chat.length, 0);
  assert.match(f.state.sent.at(-1).content, /^\*\*\[任务模式\] INFE-2\*\*\n\n/);
  const selected = f.state.requests.filter(entry => entry.body.issue_ref);
  assert.ok(selected.every(entry => entry.body.issue_ref === "INFE-2" && entry.body.reply_to_message_id === quote));
  assert.equal(f.state.bindings.length, command === "unbind" ? 1 : 2);
  if (command === "unbind") assert.equal(selected.at(-1).body.expected_binding_id, "binding-2");
});

test("private unbinding one task retains issue mode, removing last returns to Chat mode", async t => {
  const f = fixture(t), relay = f.make(); await relay.handle(f.input("/unbind INFE-1"));
  assert.match(f.state.sent.at(-1).content, /^\*\*\[任务模式\] INFE-1\*\*\n\n已解除/);
  await relay.handle(f.input("/unbind INFE-2", { messageId: "101" }));
  assert.match(f.state.sent.at(-1).content, /^\*\*\[聊天模式\]\*\*\n\n已解除/);
  await relay.handle(f.input("新的普通聊天", { messageId: "102" }));
  assert.equal(f.state.chat.length, 1);
});

test("private binding cannot leak across sender, workspace, robot or configured agent scopes", async t => {
  for (const kind of ["sender", "workspace", "robot", "agent"]) await t.test(kind, async t => {
    const f = fixture(t), relay = f.make(kind === "workspace" ? { workspace: "other-ws" } :
      kind === "robot" ? { robot: "other" } : kind === "agent" ? { agent: B } : {});
    const message = f.input("INFE-1 继续", kind === "sender" ? { user: "bob", conversation: { kind: "private", id: "bob" } } : {});
    await relay.handle(message); assert.equal(f.state.comments.length, 0); assert.equal(f.state.chat.length, 1);
    assert.deepEqual(f.state.chat[0], { ...message, replyToMessageId: null, images: [], context: null });
  });
});

test("private squad, human or unassigned bind rejection does not affect prior task associations", async t => {
  const f = fixture(t), relay = f.make(); f.state.bindDenied = true;
  await relay.handle(f.input("/bind INFE-3"));
  assert.equal(f.state.bindings.length, 2); assert.equal(f.state.comments.length, 0); assert.equal(f.state.chat.length, 0);
  assert.match(f.state.sent[0].content, /私聊仅支持由单个智能体负责的任务/);
});

test("stale private assignment blocks text, media and owner-authority commands until explicit rebind", async t => {
  const f = fixture(t), relay = f.make(); f.state.bindings.pop();
  f.state.bindings[0].binding.private = { state: "needs_rebind", reason: "assignment_changed" };
  await relay.handle(f.input("继续", { images: [{}] }));
  await relay.handle(f.input("/new", { messageId: "101" }));
  assert.equal(f.state.comments.length, 0); assert.equal(f.state.chat.length, 0);
  assert.ok(f.state.sent.every(sent => /任务负责人已变更/.test(sent.content)));
  await relay.handle(f.input("/bind INFE-1", { messageId: "102" }));
  await relay.handle(f.input("继续", { messageId: "103" }));
  assert.equal(f.state.comments.length, 1);
  assert.equal(f.state.comments[0].expected_generation, "generation-1-explicit-rebind");
});

test("private stale assignment is guarded even when a malformed server preflight grants commenting", async t => {
  const f = fixture(t), relay = f.make(); const original = relay.request;
  f.state.bindings.pop(); f.state.bindings[0].binding.private = { state: "needs_rebind", reason: "assignment_changed" };
  relay.request = async (...args) => { const result = await original(...args); result.can_comment = true; delete result.notice; return result; };
  await relay.handle(f.input("继续", { images: [{}] }));
  assert.equal(f.state.comments.length, 0); assert.match(f.state.sent[0].content, /任务负责人已变更/);
});

for (const operation of ["message", "unbind"]) test(`private prepared ${operation} cannot follow reassignment or another issue after restart`, async t => {
  const f = fixture(t), first = f.make();
  f.state.failSubmit = true; f.state.failUnbind = true;
  await first.handle(f.input(operation === "message" ? "INFE-1 继续" : "/unbind INFE-1"));
  const prepared = structuredClone(Object.values(first.inbox)[0].preparedBody);
  assert.equal(prepared.expected_binding_id, "binding-1"); assert.equal(prepared.expected_generation, "generation-1");
  first.stop(); f.state.bindings[0].binding.generation = "reassigned-generation";
  f.state.failSubmit = false; f.state.failUnbind = false;
  const restored = f.make(); await restored.poll();
  assert.deepEqual(f.state.requests.filter(call => call.path === "issue-relay").at(-1).body, prepared);
  assert.equal(f.state.comments.length, 0); assert.equal(f.state.bindings.length, 2);
  assert.equal(readdirSync(restored.quarantinePath).length, 1);
});

test("failed private ambiguity clarification never executes after remaining association becomes unique", async t => {
  const f = fixture(t), first = f.make(); first.send = () => { throw new Error("notice unavailable"); };
  await first.handle(f.input("继续")); first.stop();
  f.state.bindings.pop(); const restored = f.make(); await restored.poll(); await restored.handle(f.input("继续"));
  assert.equal(f.state.comments.length, 0); assert.equal(f.state.chat.length, 0);
  assert.match(f.state.sent[0].content, /INFE-1、INFE-2/);
  assert.equal(f.state.requests.filter(entry => entry.path === "issue-relay").length, 1);
  const marker = JSON.parse(readFileSync(join(restored.decisionsPath, readdirSync(restored.decisionsPath)[0]), "utf8"));
  assert.equal(marker.disposition, "no-route");
});

test("private per-issue failure does not block another selected issue while same-task order is retained", async t => {
  const f = fixture(t), relay = f.make(); f.state.failedBindings.add("binding-1");
  await relay.handle(f.input("INFE-1 first"));
  await relay.handle(f.input("INFE-1 second", { messageId: "101" }));
  await relay.handle(f.input("INFE-2 independent", { messageId: "102" }));
  assert.equal(f.state.comments.length, 1); assert.equal(f.state.comments[0].expected_binding_id, "binding-2");
  assert.equal(Object.keys(relay.inbox).length, 2);
  f.state.failedBindings.clear(); await relay.poll();
  assert.deepEqual(f.state.comments.map(body => body.content), ["INFE-2 independent", "INFE-1 first", "INFE-1 second"]);
});
