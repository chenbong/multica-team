import assert from "node:assert/strict";
import test from "node:test";
import { mkdtempSync, readFileSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { execFileSync } from "node:child_process";
import vm from "node:vm";
import { AgentBindingError, resolveRobotAgent, RobotAgentBinding } from "../src/agent-binding.js";
import { RobotRuntime } from "../src/robots.js";
import { Multica } from "../src/multica.js";
import { IssueRelay } from "../src/issue-relay.js";
import { ChatRelay } from "../src/chat.js";
import { groupRobotEntries } from "../src/group-robot-entries.js";
import { resolveOwnedAgent, assertBindingAvailable } from "../src/admin-agents.js";

const W = "11111111-1111-4111-8111-111111111111";
const U = "22222222-2222-4222-8222-222222222222";
const A = "33333333-3333-4333-8333-333333333333";
const B = "44444444-4444-4444-8444-444444444444";
const logger = { log() {}, warn() {}, error() {} };
function fixture() {
  const robot = { id: "robot", name: "Fixture robot", purpose: "task", enabled: true,
    workspaceId: W, profile: "owner", boundByEmail: "owner@example.test", agent: "test助手",
    appId: "123", appKey: "fixture-key", appSecret: "fixture-secret", allowUsers: ["owner"] };
  const state = { me: { id: U, email: robot.boundByEmail },
    agent: { id: A, name: "TEST助手", workspace_id: W, owner_id: U, archived_at: null }, calls: [] };
  const multica = { workspaceId: W,
    async request(path) { state.calls.push(path); if (state.failure) throw state.failure;
      return path === "/api/me" ? state.me : state.agent; },
    async listAgents() { state.calls.push("catalog"); if (state.failure) throw state.failure;
      return state.catalog || [state.agent]; } };
  return { robot, state, multica };
}

test("legacy case-only rename resolves uniquely within the original owner and workspace", async () => {
  const f = fixture();
  f.state.catalog = [f.state.agent, { ...f.state.agent, id: B, owner_id: B },
    { ...f.state.agent, id: B, workspace_id: B }];
  const before = structuredClone(f.robot);
  const result = await resolveRobotAgent(f.robot, f.multica);
  assert.equal(result.agent.id, A);
  assert.equal(result.agent.name, "TEST助手");
  assert.deepEqual(f.robot, before); // Lookup itself never writes configuration.
});

test("fixed ID survives arbitrary renames and case changes without a name lookup", async () => {
  const f = fixture(); f.robot.agentId = A;
  for (const name of ["TEST助手", "完全不同的新名称", "test助手"]) {
    f.state.agent.name = name;
    const result = await resolveRobotAgent(f.robot, f.multica);
    assert.equal(result.agent.id, A); assert.equal(result.agent.name, name);
  }
  assert.equal(f.state.calls.includes("catalog"), false);
});

test("legacy bindings never guess across owners, workspaces, case collisions or full renames", async t => {
  for (const kind of ["owner", "workspace", "ambiguous", "renamed", "account", "unanchored"]) {
    await t.test(kind, async () => {
      const f = fixture();
      if (kind === "owner") f.state.agent.owner_id = B;
      if (kind === "workspace") f.state.agent.workspace_id = B;
      if (kind === "ambiguous") f.state.catalog = [f.state.agent, { ...f.state.agent, id: B, name: "test助手" }];
      if (kind === "renamed") f.state.agent.name = "完全不同的名字";
      if (kind === "account") f.state.me.email = "someone-else@example.test";
      if (kind === "unanchored") delete f.robot.boundByEmail;
      await assert.rejects(resolveRobotAgent(f.robot, f.multica), AgentBindingError);
    });
  }
});

test("fixed ID validates owner, workspace, archive and permissions; never falls back to a replacement name", async t => {
  for (const [kind, code] of [["owner", "owner_changed"], ["workspace", "workspace_changed"],
    ["archive", "archived"], ["missing", "missing_or_inaccessible"], ["permission", "permission_denied"]]) {
    await t.test(kind, async () => {
      const f = fixture(); f.robot.agentId = A;
      if (kind === "owner") f.state.agent.owner_id = B;
      if (kind === "workspace") f.state.agent.workspace_id = B;
      if (kind === "archive") f.state.agent.archived_at = "2026-01-01T00:00:00Z";
      if (kind === "missing") f.state.failure = Object.assign(new Error("not available"), { status: 404 });
      if (kind === "permission") f.state.failure = Object.assign(new Error("not allowed"), { status: 403 });
      f.state.catalog = [{ ...f.state.agent, id: B, name: f.robot.agent }];
      await assert.rejects(resolveRobotAgent(f.robot, f.multica), error => error.code === code && error.state === "binding_error");
      assert.equal(f.state.calls.includes("catalog"), false);
    });
  }
});

test("temporary HTTP/network/malformed responses retain identity and report validation failure, then recover", async () => {
  const f = fixture(); f.robot.agentId = A;
  let writes = 0;
  const binding = new RobotAgentBinding(f.robot, f.multica, { ttlMs: 0, onResolved: () => { writes++; } });
  for (const error of [new Error("timeout"), Object.assign(new Error("gateway down"), { status: 503 })]) {
    f.state.failure = error;
    await assert.rejects(binding.resolve(), error => error.code === "query_failed");
    assert.equal(binding.health.state, "validation_failed");
    assert.equal(f.robot.agentId, A); assert.equal(writes, 0);
  }
  delete f.state.failure;
  f.state.me = { invalid: true };
  await assert.rejects(binding.resolve(), error => error.state === "validation_failed");
  f.state.me = { id: U, email: f.robot.boundByEmail };
  await binding.resolve(); assert.equal(binding.health.state, "healthy"); assert.equal(writes, 1);
});

test("identity persistence preserves credentials, unrelated records, unknown options, and concurrent edits", () => {
  const dir = mkdtempSync(join(tmpdir(), "binding-persist-"));
  try {
    const path = join(dir, "config.json");
    const f = fixture();
    const config = { multica: { keep: true }, custom: { untouched: true },
      robots: [f.robot, { id: "other", appSecret: "other-fixture-secret", agent: "unchanged" }] };
    writeFileSync(path, JSON.stringify(config), { mode: 0o600 });
    const moduleURL = new URL("../src/config.js", import.meta.url).href;
    const script = `import {loadConfig,persistAgentIdentity} from ${JSON.stringify(moduleURL)};
      import {readFileSync,writeFileSync} from 'node:fs';
      const robot=loadConfig().robots[0];
      const latest=JSON.parse(readFileSync(process.env.INFOFLOW_CONFIG_PATH));
      latest.robots[0].appSecret='new-fixture-secret'; latest.robots[0].customOption='preserved';
      writeFileSync(process.env.INFOFLOW_CONFIG_PATH,JSON.stringify(latest));
      persistAgentIdentity(robot,${JSON.stringify({ agent: f.state.agent, ownerId: U, workspaceId: W })});`;
    execFileSync(process.execPath, ["--input-type=module", "-e", script], { env: { ...process.env, INFOFLOW_CONFIG_PATH: path } });
    const saved = JSON.parse(readFileSync(path));
    assert.equal(saved.robots[0].agentId, A); assert.equal(saved.robots[0].agentName, "TEST助手");
    assert.equal(saved.robots[0].appSecret, "new-fixture-secret");
    assert.equal(saved.robots[0].customOption, "preserved");
    assert.equal(saved.robots[0].stateAgentRef, f.robot.agent);
    assert.deepEqual(saved.robots[1], config.robots[1]); assert.deepEqual(saved.custom, config.custom);
    const changed = `import {loadConfig,persistAgentIdentity} from ${JSON.stringify(moduleURL)};
      import {readFileSync,writeFileSync} from 'node:fs';const robot=loadConfig().robots[0];
      const latest=JSON.parse(readFileSync(process.env.INFOFLOW_CONFIG_PATH));latest.robots=[];
      writeFileSync(process.env.INFOFLOW_CONFIG_PATH,JSON.stringify(latest));
      try {persistAgentIdentity(robot,${JSON.stringify({ agent: f.state.agent, ownerId: U, workspaceId: W })});process.exitCode=1;}catch{}`;
    execFileSync(process.execPath, ["--input-type=module", "-e", changed], { env: { ...process.env, INFOFLOW_CONFIG_PATH: path } });
    assert.deepEqual(JSON.parse(readFileSync(path)).robots, []);
  } finally { rmSync(dir, { recursive: true, force: true }); }
});

test("admin success requires a connected transport and a healthy binding; failures recover without reconnect", async () => {
  const f = fixture(); f.robot.agentId = A;
  const runtime = new RobotRuntime({ robots: [f.robot], multica: {}, issueRelay: { enabled: false } }, logger,
    { createMultica: () => f.multica, persistBinding: (robot, result) => Object.assign(robot,
      { agentId: result.agent.id, agentName: result.agent.name, boundUserId: result.ownerId }) });
  const socket = { getState: () => "connected", disconnect() {} };
  runtime.instances.set(f.robot.id, { wsClient: socket, bridge: { stop() {} }, robot: f.robot });
  try {
    assert.equal(runtime.status()[0].state, "checking");
    await runtime.refreshBindings(); assert.equal(runtime.status()[0].state, "connected");
    assert.equal(runtime.status()[0].agentName, "TEST助手");
    const binding = runtime.bindings.get(f.robot.id); binding.ttlMs = 0;
    f.state.failure = Object.assign(new Error("temporary"), { status: 500 });
    await runtime.refreshBindings(); assert.equal(runtime.status()[0].state, "validation_failed");
    delete f.state.failure; f.state.agent.archived_at = "2026-01-01";
    await runtime.refreshBindings(); assert.equal(runtime.status()[0].state, "binding_error");
    f.state.agent.archived_at = null; f.state.agent.name = "改名后的助手";
    await runtime.refreshBindings(); assert.equal(runtime.status()[0].state, "connected");
    assert.equal(runtime.status()[0].agentName, "改名后的助手");
    assert.equal(runtime.instances.get(f.robot.id).wsClient, socket);
    socket.getState = () => "stopped";
    f.state.agent.archived_at = "2026-01-01";
    await runtime.refreshBindings();
    assert.equal(runtime.status()[0].state, "stopped");
    assert.equal(runtime.status()[0].bindingState, "binding_error");
    assert.match(runtime.status()[0].bindingError, /已归档/);
  } finally { await runtime.stopAll(); }
});

test("chat, task scope and group participant discovery share the same fixed agent ID after rename", async () => {
  const f = fixture(); f.robot.agentId = A; f.state.agent.name = "Renamed agent";
  const binding = new RobotAgentBinding(f.robot, f.multica, { ttlMs: 0 });
  const multica = new Multica({ boundAgentResolver: binding, workspaceId: W });
  assert.equal((await multica.resolveAgent(A)).id, A);
  const relay = new IssueRelay({ robotId: f.robot.id, multica: { workspaceId: W }, statePath: "/unused", issueRelay: { enabled: false } }, multica, () => {}, logger);
  assert.deepEqual(await relay.scope(), { robot_id: f.robot.id, agent_id: A, workspace_id: W });
  for (const kind of ["private", "group"]) {
    const entries = await groupRobotEntries({ robots: [f.robot], multica: {} }, new Map(), W, [A], logger,
      { resolveBinding: () => binding.resolve(), agentNames: { [A]: kind } });
    assert.equal(entries[0].agentId, A); assert.equal(entries[0].robotId, f.robot.id);
  }
});

test("admin picker resolves fixed IDs across renames and rejects archived or ambiguous legacy selections", async () => {
  const f = fixture();
  const options = { account: { userId: U }, workspaces: [{ id: W }], workspaceId: W,
    listAgents: async () => [f.state.agent] };
  assert.equal((await resolveOwnedAgent({ ...options, agentId: A, agentName: "stale" })).agent.id, A);
  f.state.agent.archived_at = "2026-01-01";
  await assert.rejects(resolveOwnedAgent({ ...options, agentId: A }));
  f.state.agent.archived_at = null;
  assert.throws(() => assertBindingAvailable([{ ...f.robot, agentId: A }],
    { ...f.robot, id: "second", agentId: A, agent: "renamed", appId: "different" }), /已绑定/);
});

test("private and group chats retain their sessions when the configured agent is renamed", async () => {
  const f = fixture(); f.robot.agentId = A;
  const binding = new RobotAgentBinding(f.robot, f.multica, { ttlMs: 0 });
  const multica = new Multica({ boundAgentResolver: binding });
  let created = 0, sent = 0;
  multica.createChatSession = async id => { assert.equal(id, A); created++; return { id: "session-" + created }; };
  multica.chatSession = async () => ({ status: "active" });
  multica.sendChatMessage = async () => ({ task_id: "task-" + ++sent });
  const dir = mkdtempSync(join(tmpdir(), "binding-chat-"));
  const relay = new ChatRelay({ robotId: "fixed", statePath: join(dir, "state"), pollIntervalMs: 3600000,
    multica: { profile: "owner", workspaceId: W } }, multica, async () => {},
    async () => ({ paths: [], failed: [], cleanup: async () => {} }), logger);
  try {
    for (const kind of ["private", "group"]) {
      const conversation = { kind, id: "fixture-conversation" };
      await relay.send(conversation, "owner", "first", [], f.robot.agent);
      f.state.agent.name = "任意改名后仍是原智能体";
      await relay.send(conversation, "owner", "second", [], f.robot.agent);
    }
    assert.equal(created, 2); assert.equal(sent, 4);
  } finally { relay.stop(); rmSync(dir, { recursive: true, force: true }); }
});

test("admin retains the owner's binding error when its CLI profile cannot be identified", async () => {
  const source = readFileSync(new URL("../src/admin.js", import.meta.url), "utf8");
  const start = source.indexOf("async function snapshot(");
  const end = source.indexOf("// Splits the machine's signed-in", start);
  const mine = fixture().robot;
  const robots = [mine, { ...mine, id: "foreign", boundByEmail: "someone-else@example.test" }];
  const context = vm.createContext({ runtime: { config: { robots, multica: {} },
    refreshBindings: async rows => assert.equal(rows.length, 1),
    status: () => robots.map(robot => ({ id: robot.id, state: "binding_error", error: "请恢复原账号登录" })) },
    visibleAccounts: async () => ({ mine: [], unresolved: 1 }),
    redactRobot: robot => { const { appSecret, ...rest } = robot; return rest; }, accounts: {}, session: { email: mine.boundByEmail } });
  vm.runInContext(source.slice(start, end), context);
  const result = await vm.runInContext('snapshot(runtime, accounts, "owner", session)', context);
  assert.equal(result.robots.length, 1); assert.equal(result.robots[0].id, mine.id);
  assert.equal(result.status[0].state, "binding_error");
  assert.equal(result.robots[0].appSecret, undefined);
});

test("name-to-ID migration retains the same delivery inbox, receipts and checkpoints after restart", () => {
  const legacy = { robotId: "fixed-robot", statePath: "/unused", issueRelay: { enabled: false },
    multica: { workspaceId: W, defaultAgent: "old-lowercase-name" } };
  const first = new IssueRelay(legacy, {}, () => {}, logger);
  const afterMigration = new IssueRelay({ ...legacy,
    multica: { ...legacy.multica, defaultAgent: A, stateAgentRef: "old-lowercase-name" } }, {}, () => {}, logger);
  for (const key of ["path", "inboxPath", "quarantinePath", "decisionsPath", "noticesPath", "receiptsPath"]) {
    assert.equal(afterMigration[key], first[key], key);
  }
});

test("admin renders distinct health states and the picker submits IDs rather than names", () => {
  const html = readFileSync(new URL("../public/index.html", import.meta.url), "utf8");
  const code = html.match(/function stateLabel\(state\) \{[\s\S]*?\n\}/)[0];
  const context = vm.createContext({}); vm.runInContext(code, context);
  assert.equal(vm.runInContext('stateLabel("binding_error")', context), "绑定异常");
  assert.equal(vm.runInContext('stateLabel("validation_failed")', context), "校验失败");
  assert.match(html, /option\.value = agent\.id/); assert.match(html, /agentId: form\.agent\.value/);
});

test("explicit per-user agent choices remain ID-based without changing the robot's binding", async () => {
  const f = fixture(); f.robot.agentId = A;
  const binding = new RobotAgentBinding(f.robot, f.multica, { ttlMs: 0 });
  const multica = new Multica({ workspaceId: W, boundAgentResolver: binding });
  multica.request = async () => ({ id: B, name: "Explicit choice", workspace_id: W });
  assert.equal((await multica.resolveAgent(B)).id, B);
  f.state.agent.name = "Renamed bound agent";
  assert.equal((await multica.resolveAgent(A)).id, A);
  assert.equal(f.robot.agentId, A);
});

test("legacy per-user names cannot auto-match another owner or workspace", async () => {
  const f = fixture(); f.robot.agentId = A;
  const binding = new RobotAgentBinding(f.robot, f.multica, { ttlMs: 0 });
  const multica = new Multica({ workspaceId: W, boundAgentResolver: binding });
  multica.listAgents = async () => [{ id: B, name: "Legacy choice", owner_id: B, workspace_id: W },
    { id: B, name: "Legacy choice", owner_id: U, workspace_id: B }];
  await assert.rejects(multica.resolveAgent("legacy choice"));
  assert.equal(f.robot.agentId, A);
});
