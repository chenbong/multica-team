import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { inspectWebPrivateJob, pollWebPrivateJobs, privateRobotMetadata, validateWebPrivateJob } from "./src/web-private-bindings.js";
import { IssueGroups } from "./src/issue-groups.js";
import { IssueRelay } from "./src/issue-relay.js";
import { groupRobotEntries } from "./src/group-robot-entries.js";

const A = "11111111-1111-4111-8111-111111111111", C = "33333333-3333-4333-8333-333333333333";
const REQUEST = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", GEN = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb";
const LEASE = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", ISSUE = "cccccccc-cccc-4ccc-8ccc-cccccccccccc";
const PUID = "1859897297556589312";
const scope = { robot_id: "caller", agent_id: C, workspace_id: "ws" };
const quiet = { warn() {}, log() {}, error() {} };
const job = () => ({ request_id: REQUEST, generation: GEN, lease_token: LEASE, issue_id: ISSUE,
  sender: "alice", agent_id: A, agent_name: "Assigned Agent", assignment_version: "9" });

function fixture(t) {
  const dir = mkdtempSync(join(tmpdir(), "web-private-bindings-"));
  const state = { calls: [], reads: [], metadata: { name: "Private Robot", puid: PUID, openChatDouble: 1,
    ak: "secret-ak", sk: "secret-sk", iconUrl: "sensitive-avatar", rawResponse: { private: true } }, warnings: [] };
  const entries = [{ robotId: "target", agentId: A, client: { robot: { getInfo: async (...args) => {
    state.reads.push(args);
    if (state.onRead) await state.onRead();
    return state.metadata;
  } } } }];
  const groups = new IssueGroups({ statePath: join(dir, "state"), entries: async (workspace, ids, options) => {
    assert.equal(workspace, scope.workspace_id); assert.deepEqual(ids, [A]);
    assert.deepEqual(options, { agentNames: { [A]: "Assigned Agent" } });
    if (state.onEntries) await state.onEntries();
    return entries;
  }, invite: () => assert.fail("private job cannot invite"), roster: () => assert.fail("private job cannot list groups") });
  const relay = new IssueRelay({ robotId: scope.robot_id, statePath: join(dir, "state"), pollIntervalMs: 3600000,
    issueRelay: { enabled: true }, multica: { defaultAgent: C, workspaceId: "ws" } },
  {}, () => assert.fail("private job cannot send"), { ...quiet, warn: text => state.warnings.push(text) }, { groups });
  relay.request = async (path, body) => {
    state.calls.push({ path, body: structuredClone(body) });
    if (path === "web-private-jobs") return { jobs: state.jobs ?? [job()] };
    if (path === "web-private-result") return state.result ?? { ok: true, state: body.reason ? "pending" : "active" };
    if (path === "web-group-jobs") return { jobs: [] };
    if (path === "issue-groups") return { groups: [] };
    if (path === "issue-deliveries") return { deliveries: [] };
    assert.fail(`unexpected endpoint ${path}`);
  };
  t.after(() => { relay.stop(); rmSync(dir, { recursive: true, force: true }); });
  return { relay, groups, state, entries, inspect: opts => inspectWebPrivateJob(groups, scope, job(), () => relay.stopped, opts) };
}

test("private web job reports target separately from unchanged caller and strips all SDK secrets", async t => {
  const f = fixture(t);
  await f.relay.pollWebPrivateBindings();
  assert.deepEqual(f.state.reads, [[]], "getInfo never receives a recipient or browser fields");
  assert.deepEqual(f.state.calls.map(c => c.path), ["web-private-jobs", "web-private-result"]);
  assert.deepEqual(f.state.calls[1].body, { ...scope, request_id: REQUEST, generation: GEN, lease_token: LEASE,
    target_agent_id: A, target_robot_id: "target", robot_chat_id: PUID, robot_name: "Private Robot", reason: "" });
  assert.doesNotMatch(JSON.stringify(f.state.calls), /secret|avatar|rawResponse|ak|sk/);
});

test("private metadata requires enabled chat, exact positive int64 PUID and a public name", () => {
  for (const enabled of [1, true]) assert.deepEqual(privateRobotMetadata({ name: " Robot ", puid: 123, openChatDouble: enabled }),
    { robot_chat_id: "123", robot_name: "Robot", reason: "" });
  for (const disabled of [0, false, undefined, "1", 2]) assert.deepEqual(privateRobotMetadata({ openChatDouble: disabled }), { reason: "private_chat_disabled" });
  for (const puid of [undefined, null, "", 0, "01", Number(PUID), "9223372036854775808", "infoflow://evil", -1]) {
    assert.throws(() => privateRobotMetadata({ name: "Robot", puid, openChatDouble: 1 }));
  }
  for (const name of [null, 123, "", " ", "x".repeat(256), "Robot\nInjected", "Robot\u0000"]) {
    assert.throws(() => privateRobotMetadata({ name, puid: PUID, openChatDouble: true }));
  }
});

test("private job schema rejects missing identity, forged sender and unsafe assignment versions", () => {
  for (const mutate of [j => { j.request_id = ""; }, j => { j.generation = null; }, j => { j.lease_token = GEN + "x"; },
    j => { j.issue_id = "INFE-1"; }, j => { j.agent_id = "Agent"; }, j => { j.sender = "Alice"; },
    j => { j.sender = "alice@example.test"; }, j => { j.assignment_version = 9; },
    j => { j.assignment_version = "01"; }, j => { j.assignment_version = "9223372036854775808"; }, j => { j.agent_name = ""; }]) {
    const input = job(); mutate(input); assert.throws(() => validateWebPrivateJob(input));
  }
  assert.throws(() => validateWebPrivateJob(null));
  assert.equal(validateWebPrivateJob({ ...job(), assignment_version: "0" }).assignment_version, "0");
});

test("missing or duplicate robots include offline configurations and never query arbitrary metadata", async t => {
  for (const kind of ["missing", "ambiguous-offline", "invalid-id", "lookup-failure", "wrong-agent"]) await t.test(kind, async t => {
    const f = fixture(t);
    if (kind === "missing") f.entries.length = 0;
    if (kind === "ambiguous-offline") f.entries.push({ robotId: "offline", agentId: A, client: null });
    if (kind === "invalid-id") f.entries[0].robotId = "bad\nrobot";
    if (kind === "lookup-failure") f.state.onEntries = () => { throw new Error("secret lookup failure"); };
    if (kind === "wrong-agent") f.entries[0].agentId = C;
    const checked = await f.inspect();
    assert.equal(checked.reason, "private_robot_missing_or_ambiguous");
    assert.equal(checked.target_robot_id, ""); assert.equal(f.state.reads.length, 0);
  });
});

test("offline, disabled and malformed SDK metadata never claim a successful private association", async t => {
  for (const failure of ["offline", "disabled", "sdk-error", "missing-puid", "rounded-puid"]) await t.test(failure, async t => {
    const f = fixture(t);
    if (failure === "offline") f.entries[0].client = null;
    if (failure === "disabled") f.state.metadata.openChatDouble = 0;
    if (failure === "sdk-error") f.state.onRead = () => { throw new Error("sensitive SDK failure"); };
    if (failure === "missing-puid") delete f.state.metadata.puid;
    if (failure === "rounded-puid") f.state.metadata.puid = Number(PUID);
    const checked = await f.inspect();
    assert.equal(checked.reason, failure === "offline" ? "private_robot_unavailable" : failure === "disabled" ? "private_chat_disabled" : "private_metadata_failed");
    assert.equal(checked.target_robot_id, "target"); assert.equal(checked.robot_chat_id, "");
    assert.doesNotMatch(JSON.stringify(checked), /sensitive|secret/);
  });
});

test("stopping during target resolution or metadata read prevents result submission", async t => {
  for (const step of ["entries", "metadata"]) await t.test(step, async t => {
    const f = fixture(t);
    if (step === "entries") f.state.onEntries = () => f.relay.stop();
    else f.state.onRead = () => f.relay.stop();
    await f.relay.pollWebPrivateBindings();
    assert.equal(f.state.calls.length, 1);
    assert.equal(f.state.reads.length, step === "entries" ? 0 : 1);
  });
});

test("hung lookup or metadata cannot monopolize the leased workspace worker", async t => {
  for (const step of ["entries", "metadata"]) await t.test(step, async t => {
    const f = fixture(t);
    if (step === "entries") f.state.onEntries = () => new Promise(() => {});
    else f.state.onRead = () => new Promise(() => {});
    const checked = await f.inspect({ timeoutMs: 10 });
    assert.equal(checked.reason, step === "entries" ? "private_robot_missing_or_ambiguous" : "private_metadata_failed");
  });
});

test("private metadata refresh survives new leases without caching another user's destination", async t => {
  const f = fixture(t); await f.relay.pollWebPrivateBindings();
  f.state.jobs = [{ ...job(), lease_token: "ffffffff-ffff-4fff-8fff-ffffffffffff", sender: "bob" }];
  f.state.metadata = { name: "Renamed", puid: "124", openChatDouble: true };
  await f.relay.pollWebPrivateBindings();
  assert.equal(f.state.reads.length, 2);
  assert.equal(f.state.calls.at(-1).body.lease_token, f.state.jobs[0].lease_token);
  assert.equal(f.state.calls.at(-1).body.robot_chat_id, "124");
  assert.equal(f.state.calls.at(-1).body.robot_name, "Renamed");
  assert.equal(Object.hasOwn(f.state.calls.at(-1).body, "sender"), false, "only server owns the leased sender");
});

test("private workers skip concurrent stale polls but remain independent of group jobs and deliveries", async t => {
  const f = fixture(t); let release;
  const gate = new Promise(resolve => { release = resolve; }), original = f.relay.request;
  f.relay.request = async (...args) => { if (args[0] === "web-private-jobs") await gate; return original(...args); };
  const first = pollWebPrivateJobs(f.relay, scope);
  await Promise.resolve();
  await pollWebPrivateJobs(f.relay, scope);
  await f.relay.pollWebBindings(); await f.relay.poll();
  assert.ok(f.state.calls.some(call => call.path === "web-group-jobs"));
  assert.ok(f.state.calls.some(call => call.path === "issue-deliveries"));
  release(); await first;
  assert.equal(f.state.calls.filter(call => call.path === "web-private-jobs").length, 1);
});

test("invalid job lists and results stay isolated, sanitized and never fall into Chat", async t => {
  for (const jobs of [null, [job(), job()], [{}]]) await t.test(JSON.stringify(jobs), async t => {
    const f = fixture(t); f.state.jobs = jobs;
    // null is deliberately an invalid envelope, not an empty job list.
    if (jobs === null) { const request = f.relay.request; f.relay.request = (path, body) => path === "web-private-jobs" ? null : request(path, body); }
    await f.relay.pollWebPrivateBindings(); await f.relay.poll();
    assert.equal(f.state.reads.length, 0); assert.ok(f.state.warnings.includes("web private binding pending"));
    assert.ok(f.state.calls.some(call => call.path === "issue-deliveries"));
  });
  const f = fixture(t); f.state.metadata.openChatDouble = 0; f.state.result = { ok: true, state: "active" };
  await assert.rejects(pollWebPrivateJobs(f.relay, scope), /invalid web private binding result/);
  assert.equal(f.groups.webJobLocks.size, 0);
});

test("private resolution uses each robot's own account, excludes other workspaces and retains offline ambiguity", async () => {
  const calls = [];
  const config = { multica: { workspaceId: "ws", cli: "/missing-fake-cli" }, robots: [
    { id: "caller", agent: "other", profile: "caller-profile", workspaceId: "ws", enabled: true },
    { id: "target", agent: "Assigned Agent", profile: "owner-profile", workspaceId: "ws", enabled: true },
    { id: "offline", agent: A, profile: "offline-profile", workspaceId: "ws", enabled: false },
    { id: "elsewhere", agent: A, workspaceId: "other-ws", enabled: true },
  ] };
  const instances = new Map([["target", { client: { fake: true }, bridge: { multica: { listAgents: async () => {
    calls.push("owner-profile"); return [{ id: A, name: "Assigned Agent" }];
  } } } }], ["caller", { bridge: { multica: { listAgents: () => assert.fail("caller account must not resolve target") } } }]]);
  const resolved = await groupRobotEntries(config, instances, "ws", [A], quiet, { agentNames: { [A]: "Assigned Agent" } });
  assert.deepEqual(calls, ["owner-profile"]);
  assert.deepEqual(resolved.map(entry => entry.robotId), ["target", "offline"]);
  assert.equal(resolved[1].client, null);
});
