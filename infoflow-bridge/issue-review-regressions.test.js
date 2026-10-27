import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, rmSync, readdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Bridge } from "./src/bridge.js";
import { groupRobotEntries } from "./src/group-robot-entries.js";
import { IssueGroups } from "./src/issue-groups.js";

const A = "11111111-1111-4111-8111-111111111111";
const B = "22222222-2222-4222-8222-222222222222";
const quiet = { log() {}, warn() {}, error() {} };
const unbound = { handled: false, silent: false };
const binding = id => ({ binding_id: `binding-${id}`, generation: `generation-${id}`, issue_id: `issue-${id}`, identifier: `TEST-${id}`, workspace_slug: "team" });

function fixture(t) {
  const dir = mkdtempSync(join(tmpdir(), "infoflow-review-"));
  const config = { robotId: "a", statePath: join(dir, "state.json"), issueRelay: { enabled: true },
    allowUsers: ["alice"], allowAllInGroups: true, pollIntervalMs: 3600000,
    multica: { cli: join(dir, "missing-test-cli"), defaultAgent: A, workspaceId: "ws", workspaceSlug: "team", webUrl: "https://web.example.invalid" } };
  const state = { binding: null, requests: [], sent: [], comments: [], fail: false };
  const instances = [];
  const make = () => {
    const bridge = new Bridge(config, async (_conversation, content) => state.sent.push(content), quiet);
    bridge.issueRelay.request = async (path, body) => {
      if (path === "issue-deliveries") return { deliveries: [] };
      state.requests.push(structuredClone(body));
      if (body.operation === "binding") return state.binding ? { handled: true, silent: false, can_comment: true, binding: state.binding } : unbound;
      if (state.fail) throw new Error("temporary failure");
      if (!state.binding) return unbound;
      if (body.expected_binding_id !== state.binding.binding_id || body.expected_generation !== state.binding.generation) {
        throw Object.assign(new Error("binding changed"), { status: 409 });
      }
      state.comments.push(structuredClone(body));
      return { handled: true, silent: true, binding: state.binding };
    };
    instances.push(bridge);
    return bridge;
  };
  t.after(() => { instances.forEach(bridge => bridge.stop()); rmSync(dir, { recursive: true, force: true }); });
  const message = (text, messageId = "100", kind = "group") => ({ conversation: { kind, id: kind === "group" ? "42" : "alice" }, user: "alice", text, messageId });
  return { dir, config, state, make, message };
}

test("completed Chat redelivery is rejected before Relay after binding and restart", async t => {
  const f = fixture(t), first = f.make();
  let chats = 0;
  first.chat.send = async (...args) => { args[7].beforeSubmit(); chats++; args[7].afterSubmit(); };
  await first.handleMessage(f.message("hello"));
  f.state.binding = binding(1);
  const before = f.state.requests.length;
  first.stop();
  const restored = f.make();
  await restored.handleMessage(f.message("hello"));
  assert.equal(chats, 1);
  assert.equal(f.state.requests.length, before, "completed Chat must never reach issue relay");
  assert.deepEqual(f.state.comments, []);
});

test("successful /new is durable: duplicate 100 cannot clear a subsequently created chat", async t => {
  const f = fixture(t), first = f.make();
  await first.handleMessage(f.message("/new"));
  const key = first.chat.key(f.message("").conversation, "alice");
  first.chat.state.threads[key] = { id: "new-chat", agentId: A }; first.chat.save();
  first.stop();
  const restored = f.make();
  await restored.handleMessage(f.message("/new"));
  assert.equal(restored.chat.state.threads[key]?.id, "new-chat");
  assert.equal(f.state.sent.length, 1);
  await restored.handleMessage(f.message("/new", "101"));
  assert.equal(restored.chat.state.threads[key], undefined);
  assert.equal(f.state.sent.length, 2);
});

test("failed /agent lookup retries; successful commands persist dedup across restart", async t => {
  const f = fixture(t), first = f.make();
  first.multica.listAgents = async () => { throw new Error("catalog temporarily unavailable"); };
  await first.handleMessage(f.message("/agent member"));
  assert.equal(Object.keys(first.issueRelay.inbox).length, 1);
  first.multica.listAgents = async () => [{ id: B, name: "member" }];
  await first.issueRelay.poll();
  assert.equal(first.agentFor("alice"), B);
  first.stop();
  const restored = f.make();
  restored.multica.listAgents = async () => { throw new Error("duplicate command reran lookup"); };
  const count = f.state.sent.length;
  await restored.handleMessage(f.message("/agent member"));
  assert.equal(f.state.sent.length, count);
  assert.equal(Object.keys(restored.issueRelay.inbox).length, 0);
  await restored.handleMessage(f.message("/help", "101"));
  await restored.handleMessage(f.message("/help", "101"));
  assert.equal(f.state.sent.length, count + 1);
});

for (const enabled of [false, true]) {
  test(`concurrent /new duplicates serialize without pre-marking (relay ${enabled})`, async t => {
    const f = fixture(t); f.config.issueRelay.enabled = enabled;
    const bridge = f.make();
    await Promise.all([bridge.handleMessage(f.message("/new")), bridge.handleMessage(f.message("/new"))]);
    assert.equal(f.state.sent.length, 1);
    await bridge.handleMessage(f.message("/new", "101"));
    assert.equal(f.state.sent.length, 2);
  });

  test(`persisted reset is completed before failed notification (relay ${enabled})`, async t => {
    const f = fixture(t); f.config.issueRelay.enabled = enabled;
    const first = f.make();
    first.sender = async () => { throw new Error("receipt unavailable"); };
    await first.handleMessage(f.message("/new"));
    const key = first.chat.key(f.message("").conversation, "alice");
    first.chat.state.threads[key] = { id: "new-chat", agentId: A }; first.chat.save(); first.stop();
    const restored = f.make(); await restored.handleMessage(f.message("/new"));
    assert.equal(restored.chat.state.threads[key]?.id, "new-chat");
    assert.equal(Object.keys(restored.issueRelay.inbox).length, 0);
  });
}

for (const kind of ["group", "private"]) {
  test(`${kind} bound request pins preflight version durably and quarantines retry after rebind`, async t => {
    const f = fixture(t), first = f.make();
    f.state.binding = binding(1); f.state.fail = true;
    await first.handleMessage(f.message("continue", "100", kind));
    const pending = Object.values(first.issueRelay.inbox)[0];
    assert.equal(pending.preparedBody.expected_binding_id, "binding-1");
    assert.equal(pending.preparedBody.expected_generation, "generation-1");
    const original = structuredClone(pending.preparedBody);
    first.stop(); f.state.binding = binding(2); f.state.fail = false;
    const restored = f.make(); await restored.issueRelay.poll();
    assert.deepEqual(f.state.requests.at(-1), original);
    assert.equal(f.state.requests.filter(body => body.operation === "binding").length, 1);
    assert.deepEqual(f.state.comments, []);
    assert.equal(readdirSync(restored.issueRelay.quarantinePath).length, 1);
  });
}

test("unbound Chat handoff retains its original mode when a pre-submit failure spans binding", async t => {
  const f = fixture(t), first = f.make();
  first.chat.send = async () => { throw new Error("pre-submit read failed"); };
  await first.handleMessage(f.message("hello"));
  first.stop(); f.state.binding = binding(1);
  const restored = f.make(); let chats = 0;
  restored.chat.send = async (...args) => { args[7].beforeSubmit(); chats++; args[7].afterSubmit(); };
  const count = f.state.requests.length;
  await restored.issueRelay.poll();
  assert.equal(chats, 1);
  assert.equal(f.state.requests.length, count);
  assert.deepEqual(f.state.comments, []);
});

test("legacy unversioned prepared ordinary input fails closed without acquiring a new binding", async t => {
  const f = fixture(t), first = f.make();
  const input = f.message("old queued request");
  const id = "a".repeat(64);
  first.issueRelay.inbox[id] = { key: JSON.stringify(["group", "42"]), input,
    preparedBody: { robot_id: "a", agent_id: A, workspace_id: "ws", conversation: input.conversation, sender: "alice", message_id: "100", operation: "message", content: input.text } };
  first.issueRelay.saveInbox(); first.stop(); f.state.binding = binding(2);
  const restored = f.make(); await restored.issueRelay.poll();
  assert.deepEqual(f.state.requests, []);
  assert.deepEqual(f.state.comments, []);
  assert.equal(readdirSync(restored.issueRelay.quarantinePath).length, 1);
});

test("first observed binding is pinned even when group reconciliation fails before mutation", async t => {
  const f = fixture(t), first = f.make();
  f.state.binding = { ...binding(1), group: { state: "active" } };
  first.issueRelay.groups = {};
  first.issueRelay.reconcileGroup = async () => { throw new Error("membership transport failed"); };
  await first.handleMessage(f.message("old target"));
  assert.equal(Object.values(first.issueRelay.inbox)[0].preparedBody.expected_binding_id, "binding-1");
  first.stop(); f.state.binding = binding(2);
  const restored = f.make(); await restored.issueRelay.poll();
  assert.equal(f.state.requests.filter(body => body.operation === "message").length, 0);
  assert.equal(readdirSync(restored.issueRelay.quarantinePath).length, 1);
});

test("failed media preparation retains pinned target and retries original attachments after restart", async t => {
  const f = fixture(t), first = f.make(); f.state.binding = binding(1);
  const input = { ...f.message("image input"), images: [{}] };
  first.issueRelay.stageImages = async () => { throw new Error("image staging temporarily failed"); };
  await first.handleMessage(input);
  assert.equal(Object.values(first.issueRelay.inbox)[0].preparedBody.expected_binding_id, "binding-1");
  first.stop();
  const path = join(f.dir, "image.png"); writeFileSync(path, Buffer.from([1, 2, 3]));
  const restored = f.make(); let cleanups = 0;
  restored.issueRelay.stageImages = async () => ({ paths: [path], failed: [], cleanup: async () => { cleanups++; } });
  await restored.issueRelay.poll();
  assert.equal(cleanups, 1); assert.equal(f.state.comments.length, 1);
  assert.equal(f.state.comments[0].expected_generation, "generation-1");
  assert.deepEqual(f.state.comments[0].attachments, [{ filename: "image.png", content_type: "image/png", data_base64: "AQID" }]);
});

test("failed /help notification retries, while accepted /agent change does not rerun on receipt failure", async t => {
  const f = fixture(t), first = f.make();
  first.sender = async () => { throw new Error("receipt unavailable"); };
  await first.handleMessage(f.message("/help"));
  assert.equal(Object.keys(first.issueRelay.inbox).length, 1);
  first.sender = async (_conversation, content) => f.state.sent.push(content);
  await first.issueRelay.poll(); assert.equal(f.state.sent.length, 1);
  first.multica.listAgents = async () => [{ id: B, name: "member" }];
  first.sender = async () => { throw new Error("receipt unavailable"); };
  await first.handleMessage(f.message("/agent member", "101"));
  assert.equal(first.agentFor("alice"), B); first.stop();
  const restored = f.make();
  restored.multica.listAgents = async () => assert.fail("completed switch must not query again");
  await restored.handleMessage(f.message("/agent member", "101"));
  assert.equal(Object.keys(restored.issueRelay.inbox).length, 0);
});

test("Chat without sender authorization never submits and stays rejected after restart and binding", async t => {
  const f = fixture(t); f.config.issueRelay.enabled = false;
  const first = f.make(); let submissions = 0;
  first.chat.send = async (...args) => { args[7].beforeSubmit(); submissions++; throw new Error("POST outcome unknown"); };
  await first.handleMessage(f.message("uncertain input")); first.stop();
  f.config.issueRelay.enabled = true; f.state.binding = binding(1);
  const restored = f.make();
  await restored.handleMessage(f.message("uncertain input"));
  assert.equal(submissions, 0); assert.deepEqual(f.state.requests, []); assert.deepEqual(f.state.comments, []);
});

test("Chat without sender authorization never calls an owner-profile fallback", async t => {
  const f = fixture(t); f.config.issueRelay.enabled = false;
  const bridge = f.make();
  bridge.chat.send = async () => { throw new Error("lookup failure before POST"); };
  await bridge.handleMessage(f.message("safe retry"));
  let submissions = 0;
  bridge.chat.send = async (...args) => { args[7].beforeSubmit(); submissions++; args[7].afterSubmit(); };
  await bridge.handleMessage(f.message("safe retry"));
  await bridge.handleMessage(f.message("safe retry"));
  assert.equal(submissions, 0); assert.deepEqual(bridge.state.uncertainChatMessageIds, []);
});

test("committed receipt can be replayed after unbind without a fresh preflight or Chat fallback", async t => {
  const f = fixture(t), first = f.make(); f.state.binding = binding(1);
  let committed;
  first.issueRelay.request = async (_path, body) => {
    if (body.operation === "binding") return { handled: true, silent: false, can_comment: true, binding: binding(1) };
    committed = structuredClone(body); throw new Error("response lost after commit");
  };
  await first.handleMessage(f.message("already committed")); first.stop();
  const restored = f.make(); let replayed = false;
  restored.issueRelay.request = async (path, body) => {
    if (path === "issue-deliveries") return { deliveries: [] };
    assert.equal(body.operation, "message"); assert.deepEqual(body, committed);
    replayed = true; return { handled: true, silent: true };
  };
  restored.chat.send = async () => assert.fail("receipt must not enter Chat");
  await restored.issueRelay.poll(); assert.equal(replayed, true);
  assert.equal(Object.keys(restored.issueRelay.inbox).length, 0);
});

function robotFixture(t) {
  const dir = mkdtempSync(join(tmpdir(), "infoflow-robot-catalog-"));
  const config = { statePath: join(dir, "state"), issueRelay: { enabled: true }, multica: { cli: join(dir, "fake-cli"), workspaceId: "ws" }, robots: [
    { id: "a", agent: "leader", profile: "alice", workspaceId: "ws", appId: "101", enabled: true },
    { id: "b", agent: "member", profile: "bob", workspaceId: "ws", appId: "202", enabled: true },
  ] };
  // An explicit test-created executable handles offline account lookup. It never
  // resolves or launches any user-installed CLI or reads an actual profile.
  writeFileSync(config.multica.cli, `#!${process.execPath}\nconst args = process.argv.slice(2);\nif (args[args.indexOf('--profile')+1] !== 'bob' || !args.includes('agent')) process.exit(2);\nconsole.log(JSON.stringify([{id:${JSON.stringify(B)},name:'member'}]));\n`, { mode: 0o700 });
  const runtime = { instances: new Map(), groupRobotEntries: (ws, ids, options) => groupRobotEntries(config, runtime.instances, ws, ids, quiet, options) };
  runtime.issueGroups = new IssueGroups({ statePath: config.statePath, entries: runtime.groupRobotEntries });
  const lookups = [];
  for (const [index, robot] of config.robots.entries()) runtime.instances.set(robot.id, { robot,
    bridge: { multica: { listAgents: async () => { lookups.push(robot.profile); return [{ id: index ? B : A, name: robot.agent }]; } } },
    client: { id: robot.id }, send: async () => robot.id });
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  return { runtime, config, lookups };
}

test("private names resolve through each robot account and in-group members send themselves", async t => {
  const f = robotFixture(t);
  const entries = await f.runtime.groupRobotEntries("ws", [A, B]);
  assert.deepEqual(f.lookups, ["alice", "bob"]);
  assert.deepEqual(entries.map(entry => entry.agentId), [A, B]);
  f.runtime.issueGroups.members = async () => [{ appId: "101" }, { appId: "202" }];
  const bound = { binding_id: "binding-1", group: { leader_agent_id: A, agent_ids: [A, B] } };
  const observed = await f.runtime.issueGroups.inspect({ robot_id: "a", agent_id: A, workspace_id: "ws" }, { kind: "group", id: "42" }, bound);
  const route = f.runtime.issueGroups.selectSender(observed, { binding: bound, author_agent_id: B });
  assert.equal(route.pin.robot_id, "b"); assert.equal(route.pin.delegated, false);
});

test("offline robot legacy name uses its own fake CLI profile and remains configured", async t => {
  const f = robotFixture(t); f.runtime.instances.delete("b");
  const entries = await f.runtime.groupRobotEntries("ws", [A, B]);
  assert.equal(entries[1].agentId, B); assert.equal(entries[1].client, null); assert.equal(entries[1].send, null);
});

test("invisible and ambiguous legacy names fail closed; UUIDs require no catalog", async t => {
  const f = robotFixture(t), member = f.runtime.instances.get("b");
  member.bridge.multica.listAgents = async () => [];
  await assert.rejects(f.runtime.groupRobotEntries("ws", [A, B]), /mapping|catalog/);
  member.bridge.multica.listAgents = async () => [{ id: A, name: "member" }, { id: B, name: "member" }];
  await assert.rejects(f.runtime.groupRobotEntries("ws", [A, B]), /ambiguous/);
  member.bridge.multica.listAgents = async () => [{ name: "member" }];
  await assert.rejects(f.runtime.groupRobotEntries("ws", [A, B]), /invalid mapping/);
  f.config.robots[0].agent = A; f.config.robots[1].agent = B;
  f.runtime.instances.get("a").bridge.multica.listAgents = member.bridge.multica.listAgents = async () => assert.fail("UUID lookup invoked CLI");
  assert.deepEqual((await f.runtime.groupRobotEntries("ws", [A, B])).map(entry => entry.agentId), [A, B]);
});

test("unavailable own-account catalogs never imply absent robots; unrelated UUIDs need no lookup", async t => {
  const f = robotFixture(t);
  f.runtime.instances.get("b").bridge.multica.listAgents = async () => { throw new Error("expired fake profile"); };
  await assert.rejects(f.runtime.groupRobotEntries("ws", [A, B]), /expired/);
  f.config.robots[0].agent = A;
  f.config.robots[1].agent = "33333333-3333-4333-8333-333333333333";
  f.runtime.instances.get("a").bridge.multica.listAgents = async () => assert.fail("UUID lookup");
  assert.deepEqual((await f.runtime.groupRobotEntries("ws", [A])).map(entry => entry.agentId), [A]);
});

test("authoritative task roster excludes an unrelated disabled invalid name without blocking the leader", async t => {
  const f = robotFixture(t);
  f.config.robots[0].agent = A;
  f.config.robots[1].agent = "deleted-agent";
  f.config.robots[1].enabled = false;
  f.runtime.instances.get("b").bridge.multica.listAgents = async () => assert.fail("unrelated invalid name was queried");
  const bound = { binding_id: "binding-1", group: { leader_agent_id: A, agent_ids: [A], agent_names: { [A]: "leader" } } };
  const scope = { workspace_id: "ws", robot_id: "a", agent_id: A };
  assert.equal(await f.runtime.issueGroups.owns(scope, bound), true);
  f.runtime.issueGroups.members = async () => [{ appId: "101" }];
  const observed = await f.runtime.issueGroups.inspect(scope, { kind: "group", id: "42" }, bound);
  assert.equal(observed.leader_present, true);
  assert.equal(f.runtime.issueGroups.selectSender(observed, { binding: bound, author_agent_id: A }).pin.robot_id, "a");
});

test("a roster-matching private participant cannot disappear when its own lookup fails", async t => {
  const f = robotFixture(t); f.config.robots[0].agent = A;
  f.runtime.instances.get("b").bridge.multica.listAgents = async () => [];
  await assert.rejects(f.runtime.groupRobotEntries("ws", [A, B], { agentNames: { [A]: "leader", [B]: "member" } }), /mapping/);
});

test("a missing member robot can use the leader despite unrelated obsolete configuration", async t => {
  const f = robotFixture(t); f.config.robots[0].agent = A; f.config.robots[1].agent = "deleted-agent";
  f.runtime.instances.get("b").bridge.multica.listAgents = async () => assert.fail("unrelated lookup");
  const bound = { binding_id: "binding-1", group: { leader_agent_id: A, agent_ids: [A, B], agent_names: { [A]: "leader", [B]: "member" } } };
  f.runtime.issueGroups.members = async () => [{ appId: "101" }];
  const observed = await f.runtime.issueGroups.inspect({ workspace_id: "ws", robot_id: "a", agent_id: A }, { kind: "group", id: "42" }, bound);
  assert.equal(observed.leader_present, true);
  assert.equal(f.runtime.issueGroups.selectSender(observed, { binding: bound, author_agent_id: B }).pin.delegated, true);
});

test("authorized bootstrap retains the addressed inviter even outside the task roster", async t => {
  const f = robotFixture(t); f.config.robots[0].agent = A;
  const entries = await f.runtime.groupRobotEntries("ws", [A], { agentNames: { [A]: "leader" }, includeRobotId: "b" });
  assert.deepEqual(entries.map(entry => entry.robotId), ["a", "b"]);
  assert.deepEqual(f.lookups, ["bob"]);
});

test("incomplete roster metadata cannot silently exclude an unresolved participant", async t => {
  const f = robotFixture(t); f.config.robots[0].agent = A;
  f.runtime.instances.get("b").bridge.multica.listAgents = async () => [];
  await assert.rejects(f.runtime.groupRobotEntries("ws", [A, B], { agentNames: { [A]: "leader" } }), /mapping/);
});
