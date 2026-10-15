import assert from "node:assert/strict";
import test from "node:test";
import { ownedAgents, resolveOwnedAgent } from "../src/admin-agents.js";

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
