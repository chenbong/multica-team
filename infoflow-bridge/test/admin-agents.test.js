import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";
import vm from "node:vm";
import { ownedAgents, resolveOwnedAgent, agentBindingIds, assertBindingAvailable } from "../src/admin-agents.js";

const account = { userId: "alice", profile: "alice" };
const workspaces = [{ id: "a", slug: "a" }, { id: "b", slug: "b" }];
const rows = [
  { name: "Mine", owner_id: "alice" },
  { name: "Shared", owner_id: "bob", visibility: "workspace" },
  { name: "Unknown", owner_id: null },
];

test("binding candidates are always creator-owned, even for workspace administrators", () => {
  assert.deepEqual(ownedAgents(rows, account.userId), [rows[0]]);
  assert.deepEqual(ownedAgents(rows, ""), []);
  assert.deepEqual(ownedAgents(null, account.userId), []);
  assert.deepEqual(ownedAgents(rows, "bob"), [rows[1]]);
});

test("binding resolves the exact workspace and owned agent without mutating stored robots", async () => {
  const calls = [];
  const result = await resolveOwnedAgent({ account, workspaces, workspaceId: "b", agentName: " Mine ",
    listAgents: async (id) => { calls.push(id); return rows; } });
  assert.equal(result.agent, rows[0]);
  assert.equal(result.workspace, workspaces[1]);
  assert.deepEqual(calls, ["b"]);
  assert.deepEqual(rows.map((row) => row.name), ["Mine", "Shared", "Unknown"]);
});

test("crafted requests cannot bind shared, missing-owner or missing agents", async (t) => {
  for (const agentName of ["Shared", "Unknown", "Missing", ""]) {
    await t.test(agentName || "empty", async () => {
      await assert.rejects(resolveOwnedAgent({ account, workspaces, workspaceId: "a", agentName,
        listAgents: async () => rows }), /当前账号创建的智能体/);
    });
  }
});

test("inaccessible workspace fails before any agent lookup and never falls back silently", async () => {
  let called = false;
  await assert.rejects(resolveOwnedAgent({ account, workspaces, workspaceId: "foreign", agentName: "Mine",
    listAgents: async () => { called = true; return rows; } }), /工作区/);
  assert.equal(called, false);
});

test("a matching name in another workspace or with another owner is not a binding candidate", async () => {
  await assert.rejects(resolveOwnedAgent({ account, workspaces, workspaceId: "b", agentName: "Mine",
    listAgents: async () => [{ name: "Mine", owner_id: "bob" }] }), /当前账号创建的智能体/);
});

test("bindings are occupied across profiles and disabled entries, but separated by workspace", () => {
  const robots = [
    { id: "existing", workspaceId: "a", agent: "Mine", appId: "app-a", enabled: false, profile: "other" },
    { id: "verification", purpose: "verification", workspaceId: "a", agent: "Mine", appId: "verification-app" },
  ];
  assert.deepEqual(agentBindingIds(robots, "a", "Mine"), ["existing"]);
  assert.deepEqual(agentBindingIds(robots, "b", "Mine"), []);
  assert.throws(() => assertBindingAvailable(robots, { id: "new", workspaceId: "a", agent: "Mine", appId: "new-app" }), /智能体已绑定/);
  assert.doesNotThrow(() => assertBindingAvailable(robots, { id: "new", workspaceId: "b", agent: "Mine", appId: "new-app" }));
  assert.throws(() => assertBindingAvailable(robots, { id: "new", workspaceId: "b", agent: "Mine", appId: "app-a" }), /如流机器人已被绑定/);
  assert.throws(() => assertBindingAvailable(robots, { id: "new", workspaceId: "b", agent: "Mine", appId: "verification-app" }), /如流机器人已被绑定/);
  assert.deepEqual(agentBindingIds([{ id: "legacy", agent: "Mine" }], "a", "Mine", "a"), ["legacy"]);
});

test("current and legacy duplicate bindings remain editable without enabling new duplicates", () => {
  const robots = [
    { id: "one", workspaceId: "a", agent: "Mine", appId: "one" },
    { id: "two", workspaceId: "a", agent: "Mine", appId: "two" },
  ];
  assert.doesNotThrow(() => assertBindingAvailable(robots, { ...robots[0], appKey: "changed" }));
  assert.throws(() => assertBindingAvailable(robots, { ...robots[0], appId: "two" }), /如流机器人已被绑定/);
  assert.throws(() => assertBindingAvailable(robots, { id: "new", workspaceId: "a", agent: "Mine", appId: "new" }), /智能体已绑定/);
  assert.doesNotThrow(() => assertBindingAvailable(robots.filter(r => r.id !== "one" && r.id !== "two"), { id: "new", workspaceId: "a", agent: "Mine", appId: "one" }));
});

const source = readFileSync(new URL("../src/admin.js", import.meta.url), "utf8");
const saveRoute = source.slice(source.indexOf('  if (request.method === "POST" && url.pathname === "/api/robot")'),
  source.indexOf('  if (request.method === "POST" && url.pathname === "/api/robot/delete")'));
async function runSave(input, existing, onCredentials) {
  const replies = [], saved = [], calls = [];
  const runtime = { config: { robots: existing, multica: { workspaceId: "a", serverUrls: [] } } };
  const context = vm.createContext({ request: { method: "POST" }, url: { pathname: "/api/robot" }, response: {}, runtime,
    accounts: { workspaces: async () => workspaces }, session: { email: "alice@example.test" },
    visibleAccounts: async () => ({ mine: [{ ...account, workspaceId: "a" }] }), ownsRobot: async () => true,
    body: async () => input, send: (_res, status, payload) => replies.push({ status, payload }),
    resolveOwnedAgent: async () => ({ workspace: workspaces[0], agent: { name: "Mine" } }),
    assertBindingAvailable, DEFAULT_BASE_URL: "https://im.example.test", randomUUID: () => "new-robot",
    fetchRobotName: async () => { calls.push("credentials"); onCredentials?.(runtime); return "New robot"; },
    saveRobotBinding: async options => { saved.push(options); return { status: "sent" }; },
    snapshot: async () => ({}), logger: { log() {} }, splitList: () => [],
  });
  await vm.runInContext('(async () => { ' + saveRoute + ' })()', context);
  return { replies, saved, calls };
}
const request = { profile: "alice", workspaceId: "a", agent: "Mine", appId: "new-app", appKey: "fixture-key", appSecret: "fixture-secret" };
test("save endpoint rejects either occupied identity before credential calls or persistence", async () => {
  for (const existing of [
    { id: "existing", workspaceId: "a", agent: "Mine", appId: "different-app" },
    { id: "existing", workspaceId: "b", agent: "Other", appId: "new-app" },
  ]) {
    const result = await runSave(request, [existing]);
    assert.equal(result.replies[0].status, 409);
    assert.match(result.replies[0].payload.error, /已.*绑定/);
    assert.deepEqual(result.calls, []);
    assert.deepEqual(result.saved, []);
  }
});
test("save endpoint rechecks both occupied identities after asynchronous credential lookup", async () => {
  for (const occupied of [
    { id: "concurrent", workspaceId: "a", agent: "Mine", appId: "different-app" },
    { id: "concurrent", workspaceId: "b", agent: "Other", appId: "new-app" },
  ]) {
    const result = await runSave(request, [], runtime => { runtime.config.robots = [occupied]; });
    assert.equal(result.replies[0].status, 409);
    assert.deepEqual(result.saved, []);
  }
});
test("uncontested save retains entries added by another request; edits keep the current identity", async () => {
  const unrelated = { id: "unrelated", workspaceId: "b", agent: "Other", appId: "other-app" };
  const result = await runSave(request, [], runtime => { runtime.config.robots = [unrelated]; });
  assert.equal(result.replies[0].status, 200);
  assert.equal(result.saved[0].robots.length, 2);
  assert.equal(result.saved[0].robots[0], unrelated);
  const current = { id: "editing", profile: "alice", workspaceId: "a", agent: "Mine", appId: "new-app" };
  const edited = await runSave({ ...request, id: "editing" }, [current]);
  assert.equal(edited.replies[0].status, 200);
});
