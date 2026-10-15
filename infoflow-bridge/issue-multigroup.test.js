import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, rmSync, readdirSync, existsSync, readFileSync, writeFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { IssueRelay } from "./src/issue-relay.js";
import { IssueGroups } from "./src/issue-groups.js";
import { withReplyMode } from "./src/reply-format.js";

const A = "11111111-1111-4111-8111-111111111111";
const B = "22222222-2222-4222-8222-222222222222";
const D = "44444444-4444-4444-8444-444444444444";
const GROUP = "1859897297556589312";
const QUOTE = "1859897297556589399";
const conversation = { kind: "group", id: GROUP };
const binding = n => ({ binding_id: `10000000-0000-4000-8000-00000000000${n}`,
  generation: `20000000-0000-4000-8000-00000000000${n}`, issue_id: `issue-${n}`, identifier: `INFE-${n}`, workspace_slug: "team",
  group: { state: "active", leader_agent_id: A, leader_robot_id: "a", agent_ids: [A, n === 1 ? B : D],
    agent_names: { [A]: "A", [B]: "B", [D]: "D" } } });
const quiet = { warn() {}, log() {}, error() {} };

// The fake service is the only router. These tests verify the JS wire contract,
// not backend permission/SQL semantics; no CLI, SDK or network is used.
function fixture(t) {
  const dir = mkdtempSync(join(tmpdir(), "issue-multigroup-"));
  const state = { plans: [1, 2].map(n => ({ conversation, binding: binding(n) })), requests: [], sent: [], comments: [],
    deliveries: [], acks: [], members: ["101", "202"], quoteMap: new Map([[QUOTE, "INFE-1"]]),
    failedStatus: new Set(), failSubmit: false, failAck: false };
  const entries = [["a", A, "101"], ["b", B, "202"], ["d", D, "404"]].map(([robotId, agentId, appId]) => ({ robotId, agentId, appId,
    client: { robotId }, send: async (target, content, options) => {
      const messageid = String(1859897297556589500n + BigInt(state.sent.length));
      state.sent.push({ robotId, target, content, options, messageid }); return { messageid };
    } }));
  const groupOptions = { statePath: join(dir, "state"), entries: async (_ws, ids) => entries.filter(entry => ids.includes(entry.agentId)),
    members: async () => state.members.map(appId => ({ appId })), invite: async () => assert.fail("unexpected group invite") };
  const groups = new IssueGroups(groupOptions), relays = [];
  const result = (bound, extra = {}) => ({ handled: true, silent: false, can_comment: true, can_reconcile_group: true,
    conversation_mode: "issue", binding: structuredClone(bound), ...extra });
  const select = body => {
    if (body.expected_binding_id) return state.plans.find(p => p.binding.binding_id === body.expected_binding_id)?.binding;
    const candidates = state.plans.filter(p => p.conversation.id === body.conversation.id);
    const quoted = state.quoteMap.get(body.reply_to_message_id);
    const explicit = body.issue_ref || String(body.content).match(/\b[A-Z0-9]{1,10}-\d+\b/)?.[0];
    const key = ["bind", "unbind"].includes(body.operation) || body.command ? explicit || quoted : quoted || explicit;
    if (key) return candidates.find(p => p.binding.identifier === key)?.binding;
    const associated = candidates.filter(p => p.binding.group.agent_ids.includes(body.agent_id));
    return associated.length === 1 ? associated[0].binding : null;
  };
  const request = async (path, body) => {
    state.requests.push({ path, body: structuredClone(body) });
    if (path === "issue-groups") return { groups: structuredClone(state.plans) };
    if (path === "issue-group-status") {
      assert.ok(body.binding_id); assert.ok(body.generation);
      if (state.failedStatus.has(body.binding_id)) throw new Error("one task is unavailable");
      const plan = state.plans.find(p => p.binding.binding_id === body.binding_id);
      assert.equal(body.generation, plan.binding.generation); assert.equal(body.conversation.id, plan.conversation.id);
      plan.binding.group.state = body.leader_present ? "active" : "pending";
      return result(plan.binding);
    }
    if (path === "issue-deliveries") return { deliveries: state.deliveries.filter(d => !state.acks.some(a => a.id === d.id)).map(d => structuredClone(d)) };
    if (path.endsWith("/ack")) {
      if (state.failAck) throw new Error("ack offline");
      state.acks.push({ id: path.split("/")[1], ...body }); return { handled: true, silent: true };
    }
    assert.equal(path, "issue-relay");
    if (body.command === "status") return { handled: true, silent: false, notice_kind: "status", conversation_mode: "issue", notice: "当前群绑定的任务：",
      task_choices: state.plans.map(p => ({ ...p.binding, status: "todo", binding_kinds: "group" })) };
    if (body.conversation.kind === "private") return { handled: false, silent: false, conversation_mode: "chat" };
    const selected = select(body);
    if (body.operation === "message") {
      if (state.failSubmit) throw new Error("submission outcome unknown");
      if (!selected || selected.generation !== body.expected_generation) throw Object.assign(new Error("binding changed"), { status: 409 });
      state.comments.push(structuredClone(body)); return result(selected, { silent: true });
    }
    if (!selected) return { handled: true, silent: false, can_comment: false, conversation_mode: "issue",
      notice: "请指定任务：INFE-1、INFE-2。引用任务消息或写明任务编号。" };
    if (body.operation === "unbind") {
      if (state.failUnbind) throw new Error("unbind outcome unknown");
      if (selected.binding_id !== body.expected_binding_id || selected.generation !== body.expected_generation) {
        throw Object.assign(new Error("unbind binding changed"), { status: 409 });
      }
      state.plans = state.plans.filter(p => p.binding.binding_id !== selected.binding_id);
      return result(selected, { notice: "已解除此任务绑定", conversation_mode: state.plans.length ? "issue" : "chat" });
    }
    return result(selected);
  };
  const make = (robotId = "a") => {
    const entry = entries.find(e => e.robotId === robotId);
    const config = { robotId, statePath: join(dir, "state"), issueRelay: { enabled: true }, pollIntervalMs: 3600000,
      multica: { defaultAgent: entry.agentId, workspaceId: "ws", workspaceSlug: "team", webUrl: "https://team.example.invalid" } };
    const relay = new IssueRelay(config, {}, entry.send, quiet, { groups });
    relay.request = request; relay.stageImages = async () => assert.fail("ambiguous image input must not stage media");
    relay.fallback = async () => assert.fail("bound or ambiguous input must not fall back to Chat");
    relays.push(relay); return relay;
  };
  const input = (text, extra = {}) => ({ conversation, user: "alice", text, messageId: "1859897297556589388", ...extra });
  const delivery = (n, author, content = "原始平台正文") => {
    const plan = state.plans.find(p => p.binding.identifier === `INFE-${n}`);
    const item = { id: `delivery-${n}`, binding: structuredClone(plan.binding), conversation: plan.conversation, author_agent_id: author,
      author_name: author === B ? "B" : "D", content };
    state.deliveries.push(item); return item;
  };
  t.after(() => { relays.forEach(r => r.stop()); rmSync(dir, { recursive: true, force: true }); });
  return { state, groups, groupOptions, entries, make, input, delivery, dir };
}

test("A in overlapping squads asks using server notice only, with no invented task or media work", async t => {
  const f = fixture(t), relay = f.make();
  await relay.handle(f.input("继续分析", { images: ["unused"] }));
  assert.equal(f.state.requests.filter(r => r.path === "issue-relay").length, 1);
  assert.equal(f.state.comments.length, 0);
  assert.equal(f.state.sent.length, 1);
  assert.match(f.state.sent[0].content, /^\*\*\[任务模式\]\*\*\n\n请指定任务：INFE-1、INFE-2/);
  assert.doesNotMatch(f.state.sent[0].content, /完整任务对话/);
  assert.deepEqual(f.state.sent[0].options, {});
  assert.equal(Object.keys(relay.inbox).length, 0);
});

test("failed ambiguity notice persists across restart and cannot become executable after unbinding another task", async t => {
  const f = fixture(t), first = f.make(); first.send = async () => { throw new Error("notice transport unavailable"); };
  await first.handle(f.input("继续分析")); first.stop();
  assert.equal(Object.values(first.inbox)[0].routingNotice.conversation_mode, "issue");
  assert.equal(existsSync(first.decisionsPath), false, "failed notice is not completed");
  f.state.plans.splice(1, 1); // A now belongs to a single task, but this input was ambiguous.
  const restored = f.make(); await restored.poll();
  assert.equal(f.state.requests.filter(r => r.path === "issue-relay").length, 1);
  assert.equal(f.state.comments.length, 0);
  assert.match(f.state.sent[0].content, /^\*\*\[任务模式\]\*\*\n\n请指定任务/);
  assert.doesNotMatch(f.state.sent[0].content, /完整任务对话/);
  assert.equal(Object.keys(restored.inbox).length, 0);
  assert.equal(readdirSync(restored.decisionsPath).length, 1);
  restored.stop(); const replay = f.make();
  await replay.handle(f.input("继续分析"));
  assert.equal(f.state.requests.filter(r => r.path === "issue-relay").length, 1);
  assert.equal(f.state.comments.length, 0); assert.equal(f.state.sent.length, 1);
});

test("completed ambiguity stays terminal after topology changes, SDK redelivery and restart; fresh clarification still works", async t => {
  const f = fixture(t), first = f.make(); const message = f.input("继续分析");
  await first.handle(message);
  assert.equal(Object.keys(first.inbox).length, 0);
  const files = readdirSync(first.decisionsPath); assert.equal(files.length, 1);
  const markerPath = join(first.decisionsPath, files[0]);
  const marker = JSON.parse(readFileSync(markerPath, "utf8"));
  assert.deepEqual(Object.keys(marker).sort(), ["completedAt", "disposition"]);
  assert.equal(marker.disposition, "no-route");
  assert.equal(statSync(first.decisionsPath).mode & 0o777, 0o700);
  assert.equal(statSync(markerPath).mode & 0o777, 0o600);
  f.state.plans.splice(1, 1);
  await first.handle(message); first.stop();
  const restored = f.make(); await restored.handle(message); await restored.poll();
  assert.equal(f.state.requests.filter(r => r.path === "issue-relay").length, 1);
  assert.equal(f.state.comments.length, 0); assert.equal(f.state.sent.length, 1);
  await restored.handle(f.input("INFE-1 继续分析", { messageId: "new-clarification" }));
  assert.equal(f.state.comments.length, 1);
  assert.equal(f.state.comments[0].expected_binding_id, binding(1).binding_id);
  assert.equal(readdirSync(restored.decisionsPath).length, 1, "accepted mutations still use server receipt path");
});

test("notice completion persistence failure keeps its no-route decision retryable, never re-routes", async t => {
  const f = fixture(t), first = f.make();
  first.completeRoutingDecision = () => { throw new Error("fake disk write failure"); };
  await first.handle(f.input("继续分析"));
  assert.equal(existsSync(first.decisionsPath), false);
  assert.ok(Object.values(first.inbox)[0].routingNotice);
  first.stop(); f.state.plans.splice(1, 1);
  const restored = f.make(); await restored.poll(); await restored.handle(f.input("继续分析"));
  assert.equal(f.state.comments.length, 0);
  assert.equal(f.state.requests.filter(r => r.path === "issue-relay").length, 1);
  assert.equal(readdirSync(restored.decisionsPath).length, 1);
  assert.equal(Object.keys(restored.inbox).length, 0);
});

test("crash between no-route marker and inbox cleanup cannot resend or execute the old input", async t => {
  const f = fixture(t), first = f.make(); const complete = first.completeRoutingDecision.bind(first); let staleInbox;
  first.completeRoutingDecision = id => { staleInbox = readFileSync(first.inboxPath, "utf8"); complete(id); };
  await first.handle(f.input("继续分析")); first.stop();
  // Model a crash before the post-marker inbox rename, using only fixture data.
  writeFileSync(first.inboxPath, staleInbox, { mode: 0o600 }); f.state.plans.splice(1, 1);
  const restored = f.make(); await restored.poll(); await restored.handle(f.input("继续分析"));
  assert.equal(Object.keys(restored.inbox).length, 0);
  assert.equal(f.state.comments.length, 0); assert.equal(f.state.sent.length, 1);
  assert.equal(f.state.requests.filter(r => r.path === "issue-relay").length, 1);
});

test("temporary preflight failures do not create terminal markers and can still retry", async t => {
  const f = fixture(t), first = f.make(); first.request = async () => { throw new Error("temporary API outage"); };
  await first.handle(f.input("继续分析"));
  assert.equal(existsSync(first.decisionsPath), false);
  assert.equal(Object.values(first.inbox)[0].routingNotice, undefined);
  first.stop(); f.state.plans.splice(1, 1);
  const restored = f.make(); await restored.poll();
  assert.equal(f.state.comments.length, 1);
  assert.equal(existsSync(restored.decisionsPath), false);
  assert.equal(Object.keys(restored.inbox).length, 0);
});

for (const [robot, task] of [["b", 1], ["d", 2]]) test(`${robot} uniquely associated task is chosen by backend, with exact preflight input`, async t => {
  const f = fixture(t), relay = f.make(robot);
  await relay.handle(f.input("继续分析"));
  const preflight = f.state.requests.find(r => r.path === "issue-relay").body;
  assert.equal(preflight.content, "继续分析"); assert.equal(preflight.operation, "binding");
  assert.equal(preflight.command, undefined);
  assert.equal(f.state.comments[0].expected_binding_id, binding(task).binding_id);
  assert.equal(f.state.comments[0].expected_generation, binding(task).generation);
  assert.equal(f.state.comments[0].content, "继续分析");
});

test("quoted exact ID and original text reach preflight unchanged; server quote selection wins over text key", async t => {
  const f = fixture(t), relay = f.make();
  await relay.handle(f.input("对照 INFE-2 继续分析\n引用原文", { replyToMessageId: QUOTE }));
  const requests = f.state.requests.filter(r => r.path === "issue-relay").map(r => r.body);
  assert.equal(requests[0].reply_to_message_id, QUOTE); assert.equal(requests[1].reply_to_message_id, QUOTE);
  assert.equal(requests[0].content, "对照 INFE-2 继续分析\n引用原文");
  assert.equal(requests[1].expected_binding_id, binding(1).binding_id);
});

test("explicit text task number is resolved by backend, never stripped from canonical comment", async t => {
  const f = fixture(t); await f.make().handle(f.input("INFE-2，继续测试"));
  assert.equal(f.state.comments[0].expected_binding_id, binding(2).binding_id);
  assert.equal(f.state.comments[0].content, "INFE-2，继续测试");
  assert.doesNotMatch(f.state.comments[0].content, /任务模式|完整任务对话/);
});

for (const command of ["binding", "unbind"]) test(`/${command} without a target is read-only; /${command} KEY preserves targeted command over quote`, async t => {
  const f = fixture(t), relay = f.make();
  await relay.handle(f.input(`/${command}`));
  assert.equal(f.state.plans.length, 2);
  assert.match(f.state.sent[0].content, command === "binding" ? /当前群绑定的任务/ : /^\*\*\[任务模式\]\*\*\n\n请指定任务/);
  await relay.handle(f.input(`/${command} INFE-2`, { replyToMessageId: QUOTE, messageId: "102" }));
  const calls = f.state.requests.filter(r => r.path === "issue-relay" && r.body.issue_ref).map(r => r.body);
  assert.ok(calls.length > 0);
  assert.ok(calls.every(body => body.issue_ref === "INFE-2" && body.reply_to_message_id === QUOTE));
  if (command === "binding") assert.ok(calls.every(body => body.command === "binding"));
  assert.match(f.state.sent.at(-1).content, /^\*\*\[任务模式\] INFE-2\*\*\n\n/);
  assert.equal(f.state.plans.length, command === "unbind" ? 1 : 2);
  assert.equal(f.state.comments.length, 0);
});

for (const change of ["another task", "same task new generation"]) test(`failed unbind cannot retarget ${change} across restart`, async t => {
  const f = fixture(t); f.state.plans.splice(1, 1); f.state.failUnbind = true;
  const first = f.make(); await first.handle(f.input(change === "another task" ? "/unbind" : "/unbind INFE-1"));
  const original = structuredClone(Object.values(first.inbox)[0].preparedBody);
  assert.equal(original.expected_binding_id, binding(1).binding_id);
  assert.equal(original.expected_generation, binding(1).generation);
  const preflight = f.state.requests.find(r => r.path === "issue-relay").body;
  assert.equal(preflight.command, "unbind"); assert.equal(preflight.operation, "binding");
  assert.equal(f.state.requests.filter(r => r.path === "issue-group-status").length, 0, "unbind must work without leader presence");
  first.stop(); f.state.failUnbind = false;
  if (change === "another task") f.state.plans[0].binding = binding(2);
  else f.state.plans[0].binding.generation = "new-generation";
  const restored = f.make(); const request = restored.request;
  restored.request = async (path, body) => {
    // Service version checks apply even when no matching live binding exists.
    if (path === "issue-relay" && body.operation === "unbind" && !f.state.plans.some(p => p.binding.binding_id === body.expected_binding_id)) {
      f.state.requests.push({ path, body: structuredClone(body) }); throw Object.assign(new Error("old binding gone"), { status: 409 });
    }
    return request(path, body);
  };
  await restored.poll();
  assert.equal(f.state.plans.length, 1);
  assert.deepEqual(f.state.requests.filter(r => r.path === "issue-relay").at(-1).body, original);
  assert.equal(f.state.requests.filter(r => r.path === "issue-relay" && r.body.operation === "binding").length, 1);
  assert.equal(readdirSync(restored.quarantinePath).length, 1);
});

test("unbound unbind preflight reports no-op without mutation or Chat", async t => {
  const f = fixture(t), relay = f.make(); const calls = [];
  relay.request = async (path, body) => { calls.push({ path, body }); return { handled: false, silent: false, conversation_mode: "chat" }; };
  await relay.handle(f.input("/unbind"));
  assert.equal(calls.length, 1); assert.equal(calls[0].body.operation, "binding");
  assert.equal(calls[0].body.command, "unbind");
  assert.match(f.state.sent[0].content, /^\*\*\[聊天模式\]\*\*\n\n当前对话未绑定/);
});

test("committed unbind with a lost response retries identical mutation, not another preflight", async t => {
  const f = fixture(t), first = f.make(); const request = first.request; let committed = null;
  first.request = async (path, body) => {
    const result = await request(path, body);
    if (path === "issue-relay" && body.operation === "unbind") {
      committed = { body: structuredClone(body), result }; throw new Error("lost committed response");
    }
    return result;
  };
  await first.handle(f.input("/unbind INFE-1")); first.stop();
  assert.deepEqual(f.state.plans.map(p => p.binding.identifier), ["INFE-2"]);
  const restored = f.make(); const freshRequest = restored.request;
  restored.request = async (path, body) => {
    if (path === "issue-relay") { assert.deepEqual(body, committed.body); return committed.result; }
    return freshRequest(path, body);
  };
  await restored.poll();
  assert.match(f.state.sent.at(-1).content, /^\*\*\[任务模式\] INFE-1\*\*\n\n已解除此任务绑定/);
  assert.deepEqual(f.state.plans.map(p => p.binding.identifier), ["INFE-2"]);
  assert.equal(Object.keys(restored.inbox).length, 0);
});

test("private unbind pins the selected binding just like group unbind and preserves server-selected mode", async t => {
  const f = fixture(t), relay = f.make(); const calls = [];
  relay.request = async (path, body) => { calls.push({ path, body }); return { handled: true, silent: false,
    binding: { ...binding(1), group: undefined, private: { state: "active", reason: "" } },
    notice: body.operation === "unbind" ? "已解除任务绑定" : "", conversation_mode: body.operation === "unbind" ? "chat" : "issue" }; };
  await relay.handle(f.input("/unbind", { conversation: { kind: "private", id: "alice" } }));
  assert.equal(calls.length, 2); assert.equal(calls[0].body.operation, "binding");
  assert.equal(calls[0].body.command, "unbind");
  assert.equal(calls[1].body.operation, "unbind");
  assert.equal(calls[1].body.expected_binding_id, binding(1).binding_id);
  assert.equal(calls[1].body.expected_generation, binding(1).generation);
  assert.match(f.state.sent[0].content, /^\*\*\[聊天模式\]\*\*\n\n已解除任务绑定/);
});

test("prepared target survives restart and unknown submit outcome, with no fresh routing on retry", async t => {
  const f = fixture(t), relay = f.make(); f.state.failSubmit = true;
  await relay.handle(f.input("INFE-1 继续", { replyToMessageId: QUOTE }));
  const original = structuredClone(Object.values(relay.inbox)[0].preparedBody);
  assert.equal(original.expected_binding_id, binding(1).binding_id); relay.stop();
  f.state.plans[0].binding.generation = "new-generation";
  f.state.quoteMap.set(QUOTE, "INFE-2"); f.state.failSubmit = false;
  const restored = f.make(); await restored.poll();
  const requests = f.state.requests.filter(r => r.path === "issue-relay");
  assert.deepEqual(requests.at(-1).body, original);
  assert.equal(requests.filter(r => r.body.operation === "binding").length, 1);
  assert.equal(f.state.comments.length, 0); assert.equal(readdirSync(restored.quarantinePath).length, 1);
});

test("same-group plans and status confirmations are keyed by binding ID, never overwritten by group ID", async t => {
  const f = fixture(t), relay = f.make();
  const plans = await relay.reconcileGroups(await relay.scope());
  assert.deepEqual([...plans.keys()], f.state.plans.map(p => p.binding.binding_id));
  const checks = f.state.requests.filter(r => r.path === "issue-group-status").map(r => r.body);
  assert.equal(checks.length, 2);
  assert.ok(checks.every(c => c.conversation.id === GROUP));
  assert.deepEqual(checks.map(c => [c.binding_id, c.generation]), [1, 2].map(n => [binding(n).binding_id, binding(n).generation]));
});

test("group verification sends current exact assignment proof without repinning prepared input", async t => {
  const f = fixture(t), relay = f.make();
  const current = f.state.plans[0].binding;
  current.group.assignment_version = "9007199254740993";
  const originalGeneration = current.generation, request = relay.request;
  relay.request = async (path, body) => {
    const result = await request(path, body);
    if (path === "issue-group-status") {
      assert.equal(body.assignment_version, "9007199254740993");
      result.binding.generation = "30000000-0000-4000-8000-000000000001";
      result.binding_revalidated = true;
    }
    return result;
  };
  const checked = await relay.reconcileGroup(await relay.scope(), conversation, current);
  assert.notEqual(checked.binding.generation, originalGeneration);
  assert.equal(current.generation, originalGeneration);
  await relay.handle(f.input("INFE-1 继续", { messageId: "101" }));
  assert.equal(f.state.requests.filter(item => item.path === "issue-group-status").length, 2);
  assert.equal(f.state.comments.length, 0);
  assert.equal(Object.keys(relay.inbox).length, 0);
});

test("wrong binding in status reply is rejected even if group, leader and generation match", async t => {
  const f = fixture(t), relay = f.make(); const request = relay.request;
  relay.request = async (path, body) => {
    const result = await request(path, body);
    if (path === "issue-group-status") result.binding.binding_id = binding(2).binding_id;
    return result;
  };
  await assert.rejects(relay.reconcileGroup(await relay.scope(), conversation, binding(1)), /binding changed/);
});

test("member B sends issue1, absent D uses A for issue2, and each ack retains actual sender message IDs", async t => {
  const f = fixture(t); const originals = [f.delivery(1, B), f.delivery(2, D)];
  await f.make().poll();
  assert.deepEqual(f.state.sent.map(s => s.robotId), ["b", "a"]);
  assert.match(f.state.sent[0].content, /^\*\*\[任务模式\] INFE-1\*\*\n\n原始平台正文/);
  assert.match(f.state.sent[1].content, /^\*\*\[任务模式\] INFE-2\*\*\n\n【D · 队长代发】/);
  assert.deepEqual(f.state.acks.map(a => a.message_ids), f.state.sent.map(s => [s.messageid]));
  assert.ok(originals.every(d => d.content === "原始平台正文"));
  assert.ok(f.state.sent.every(s => s.options.atUser === undefined));
});

test("both present members send as themselves even with the same physical group and leader", async t => {
  const f = fixture(t); f.state.members.push("404"); f.delivery(1, B); f.delivery(2, D);
  await f.make().poll(); assert.deepEqual(f.state.sent.map(s => s.robotId), ["b", "d"]);
  assert.ok(f.state.sent.every(s => !s.content.includes("队长代发")));
});

test("one long summary has its task number and precise footer, with a stable shared retry checkpoint", async t => {
  const f = fixture(t); const content = "中文🙂原文".repeat(500);
  const item = f.delivery(2, D, content); item.comment_id = "comment-2"; f.state.failAck = true;
  const relay = f.make(); await relay.poll(); const firstCount = f.state.sent.length;
  assert.equal(firstCount, 1); assert.equal(item.content, content);
  assert.ok(f.state.sent.every(s => s.content.startsWith("**[任务模式] INFE-2**\n\n【D · 队长代发】") &&
    s.content.endsWith("**[完整对话链接](https://team.example.invalid/team/issues/INFE-2#comment-comment-2)**") && Buffer.byteLength(s.content) <= 1800));
  f.state.failAck = false; await relay.poll();
  assert.equal(f.state.sent.length, firstCount); assert.equal(f.state.acks[0].message_ids.length, firstCount);
  assert.equal(f.groups.deliveries[item.id].acked, true);
});

for (const fault of ["status", "ownership", "offline member"]) test(`one issue ${fault} fault cannot stop the other issue in the same group`, async t => {
  const f = fixture(t); f.delivery(1, B); f.delivery(2, D);
  if (fault === "status") f.state.failedStatus.add(binding(1).binding_id);
  if (fault === "ownership") { const owns = f.groups.owns.bind(f.groups); f.groups.owns = async (scope, bound) => {
    if (bound.binding_id === binding(1).binding_id) throw new Error("broken task roster"); return owns(scope, bound);
  }; }
  if (fault === "offline member") f.entries[1].client = null;
  await f.make().poll();
  assert.equal(f.state.sent.length, 1); assert.match(f.state.sent[0].content, /^\*\*\[任务模式\] INFE-2/);
  assert.deepEqual(f.state.acks.map(a => a.id), ["delivery-2"]);
});

test("a failed group plan also leaves deliveries in another group running", async t => {
  const f = fixture(t); f.state.plans[1].conversation = { kind: "group", id: "1859897297556589390" };
  f.state.failedStatus.add(binding(1).binding_id); f.delivery(1, B); f.delivery(2, D);
  await f.make().poll();
  assert.equal(f.state.sent.length, 1); assert.equal(f.state.sent[0].target.id, "1859897297556589390");
});

test("one pinned task's failed leader check cannot block another inbound task; same-task order is retained", async t => {
  const f = fixture(t), relay = f.make(); f.state.failedStatus.add(binding(1).binding_id);
  await relay.handle(f.input("INFE-1 first", { messageId: "100" }));
  await relay.handle(f.input("INFE-1 second", { messageId: "101" }));
  await relay.handle(f.input("INFE-2 independent", { messageId: "102" }));
  assert.deepEqual(f.state.comments.map(c => c.content), ["INFE-2 independent"]);
  assert.equal(Object.keys(relay.inbox).length, 2);
  f.state.failedStatus.clear(); await relay.poll();
  assert.deepEqual(f.state.comments.map(c => c.content), ["INFE-2 independent", "INFE-1 first", "INFE-1 second"]);
  assert.equal(Object.keys(relay.inbox).length, 0);
});

test("/bind KEY keeps other task bindings and forwards only the explicitly requested target", async t => {
  const f = fixture(t), relay = f.make();
  await relay.handle(f.input("/bind INFE-2", { replyToMessageId: QUOTE }));
  assert.deepEqual(f.state.plans.map(p => p.binding.identifier), ["INFE-1", "INFE-2"]);
  const mutation = f.state.requests.filter(r => r.path === "issue-relay");
  assert.equal(mutation.length, 1); assert.equal(mutation[0].body.operation, "bind");
  assert.equal(mutation[0].body.issue_ref, "INFE-2");
  assert.ok(f.state.requests.filter(r => r.path === "issue-group-status").every(r => r.body.binding_id === binding(2).binding_id));
});

test("binding membership locks are independent even within one group", async t => {
  const f = fixture(t); let release;
  const wait = new Promise(resolve => { release = resolve; });
  const inspect = f.groups.inspectSerial.bind(f.groups);
  f.groups.inspectSerial = async (...args) => { if (args[2].binding_id === binding(1).binding_id) await wait; return inspect(...args); };
  const scope = { workspace_id: "ws", robot_id: "a", agent_id: A };
  const pending = f.groups.inspect(scope, conversation, binding(1));
  try {
    const second = await Promise.race([f.groups.inspect(scope, conversation, binding(2)),
      new Promise((_, reject) => { const timer = setTimeout(() => reject(new Error("second binding blocked")), 1000); timer.unref(); })]);
    assert.equal(second.leader_present, true);
  } finally { release(); await pending; }
});

test("unbound private input remains Chat, with quote/content unchanged and no task identifier", async t => {
  const f = fixture(t), relay = f.make(); let handed = null;
  relay.fallback = async input => { handed = input; };
  await relay.handle(f.input("私人内容 INFE-1", { conversation: { kind: "private", id: "alice" }, replyToMessageId: QUOTE }));
  assert.equal(handed.text, "私人内容 INFE-1"); assert.equal(handed.replyToMessageId, QUOTE);
  assert.equal(f.state.comments.length, 0); assert.equal(f.state.sent.length, 0);
});

test("ordinary task-like text in an unbound group remains the server-selected Chat route", async t => {
  const f = fixture(t), relay = f.make(); let handed = null; const requests = [];
  relay.request = async (path, body) => { requests.push({ path, body }); return { handled: false, silent: false, conversation_mode: "chat" }; };
  relay.fallback = async input => { handed = input; };
  await relay.handle(f.input("介绍 GPT-6-sol，以及 INFE-1 的含义"));
  assert.equal(requests.length, 1); assert.equal(requests[0].body.content, handed.text);
  assert.equal(requests[0].body.operation, "binding"); assert.equal(requests[0].body.issue_ref, undefined);
  assert.equal(f.state.sent.length, 0); assert.equal(f.state.comments.length, 0);
});

test("known foreign quote refusal is terminal even if the addressed agent has one task", async t => {
  const f = fixture(t), relay = f.make("b"); const calls = [];
  relay.request = async (path, body) => { calls.push({ path, body }); return { handled: true, silent: false, conversation_mode: "issue",
    notice: "引用消息不属于当前群任务，请重新指定任务" }; };
  await relay.handle(f.input("继续", { replyToMessageId: QUOTE }));
  assert.equal(calls.length, 1); assert.equal(calls[0].body.reply_to_message_id, QUOTE);
  assert.match(f.state.sent[0].content, /^\*\*\[任务模式\]\*\*\n\n引用消息不属于当前群任务/);
  assert.doesNotMatch(f.state.sent[0].content, /完整任务对话/); assert.equal(f.state.comments.length, 0);
});

test("task labels accept official digit-leading workspace prefixes and cannot inject lines", () => {
  for (const ref of ["INFE-1", "2026-1", "4A-1", "0123456789-999"]) {
    assert.equal(withReplyMode("正文", "issue", ref), `**[任务模式] ${ref}**\n\n正文`);
  }
  for (const ref of ["INFE-1\n@all", "ABCDEFGHIJK-1", "[INFE-1]", "INFE_1"]) {
    assert.throws(() => withReplyMode("正文", "issue", ref), /invalid reply/);
  }
  assert.equal(withReplyMode("正文", "chat", "INFE-1"), "**[聊天模式]**\n\n正文");
  assert.equal(withReplyMode("请选择 INFE-1 或 INFE-2", "issue"), "**[任务模式]**\n\n请选择 INFE-1 或 INFE-2");
});
