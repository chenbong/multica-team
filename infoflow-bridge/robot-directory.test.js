import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { IssueRelay } from "./src/issue-relay.js";

function fixture(t, allowUsers = ["alice"]) {
  const directory = mkdtempSync(join(tmpdir(), "infoflow-directory-"));
  const state = { time: 1000, reads: 0, posts: [], warnings: [], available: true,
    info: { name: " SDK Name ", openChatDouble: 1, puid: "123456", rawResponse: { appSecret: "fake-secret" } } };
  const relay = new IssueRelay({ robotId: "self", statePath: join(directory, "state"), allowUsers,
    issueRelay: { enabled: true }, pollIntervalMs: 3600000,
    multica: { defaultAgent: "11111111-1111-4111-8111-111111111111", workspaceId: "workspace" } }, {},
  async () => assert.fail("directory must not send"), { warn: message => state.warnings.push(message) }, {
    now: () => state.time, getRobotAvailable: () => state.available,
    getPrivateRobotInfo: async () => { state.reads++; return typeof state.info === "function" ? state.info() : state.info; },
    privateRobotInfoTimeoutMs: 20,
  });
  relay.request = async (path, body) => { assert.equal(path, "robot-directory"); state.posts.push(body); return { ok: true }; };
  t.after(() => { relay.stop(); rmSync(directory, { recursive: true, force: true }); });
  return { state, relay };
}

test("directory publishes only own trusted SDK name and public metadata every 60 seconds", async t => {
  const f = fixture(t);
  await f.relay.pollRobotDirectory();
  assert.deepEqual(f.state.posts[0], { robot_id: "self", agent_id: "11111111-1111-4111-8111-111111111111",
    workspace_id: "workspace", robot_name: "SDK Name", robot_chat_id: "123456", private_enabled: true, available: true });
  f.state.time += 59999; await f.relay.pollRobotDirectory();
  assert.equal(f.state.reads, 1);
  f.state.time++; await f.relay.pollRobotDirectory();
  assert.equal(f.state.posts.length, 2);
  assert.doesNotMatch(JSON.stringify(f.state.posts), /secret|rawResponse|allowUsers/);
});

for (const [users, sdkEnabled, available, expected] of [
  [[], 1, true, false], [["alice"], 0, true, false], [["alice"], 1, false, true],
]) test("private enablement reflects SDK switch AND configured allowed users; availability reflects live transport", async t => {
  const f = fixture(t, users); f.state.info.openChatDouble = sdkEnabled; f.state.available = available;
  await f.relay.pollRobotDirectory();
  assert.equal(f.state.posts[0].private_enabled, expected);
  assert.equal(f.state.posts[0].available, available);
  assert.equal(f.state.posts[0].robot_name, "SDK Name");
});

for (const info of [null, { name: "SDK Name", openChatDouble: 1 },
  { name: "SDK Name", puid: "123", openChatDouble: "unknown" },
  () => { throw new Error("fake-secret"); }, () => new Promise(() => {})]) {
  test("metadata failures stay unknown, not absent/offline, and never leak SDK details", async t => {
    const f = fixture(t); f.state.info = info;
    await f.relay.pollRobotDirectory();
    assert.deepEqual(f.state.posts, []);
    assert.doesNotMatch(f.state.warnings.join(" "), /secret/);
  });
}

test("directory coalesces in-flight reads and does not block issue poll; late stop prevents POST", async t => {
  const f = fixture(t);
  let entered, release;
  const started = new Promise(resolve => { entered = resolve; });
  f.relay.privateRobotInfoTimeoutMs = 1000;
  f.state.info = () => { entered(); return new Promise(resolve => { release = resolve; }); };
  f.relay.request = async path => { assert.equal(path, "issue-deliveries"); return { deliveries: [] }; };
  const pending = f.relay.pollRobotDirectory(); await started;
  await f.relay.pollRobotDirectory(); await f.relay.poll();
  assert.equal(f.state.reads, 1);
  f.relay.stop();
  release({ name: "SDK Name", puid: "123456", openChatDouble: 1 }); await pending;
  assert.equal(f.state.posts.length, 0);
  assert.equal(f.relay.directoryTimer, null);
});

for (const status of [404, 500]) test(`directory HTTP ${status} is sanitized and only old API 404 is ignored`, async t => {
  const f = fixture(t);
  f.relay.request = async () => { throw Object.assign(new Error("fake-service-secret"), { status }); };
  await f.relay.pollRobotDirectory();
  assert.equal(f.state.warnings.length, status === 404 ? 0 : 1);
  assert.doesNotMatch(f.state.warnings.join(" "), /secret/);
});
