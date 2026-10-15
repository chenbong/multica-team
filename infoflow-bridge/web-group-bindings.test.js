import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, rmSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { IssueGroups, groupRoster, groupMembers } from "./src/issue-groups.js";
import { inspectWebGroupJob, validateWebGroupJob, pollWebGroupJobs, MAX_WEB_GROUP_CANDIDATES } from "./src/web-group-bindings.js";
import { IssueRelay } from "./src/issue-relay.js";
import { registerHandlers, RobotRuntime } from "./src/robots.js";
import { groupRobotEntries } from "./src/group-robot-entries.js";

const A = "11111111-1111-4111-8111-111111111111", B = "22222222-2222-4222-8222-222222222222";
const C = "33333333-3333-4333-8333-333333333333";
const REQUEST = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", GENERATION = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb";
const ISSUE = "cccccccc-cccc-4ccc-8ccc-cccccccccccc";
const LEASE = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", LEASE2 = "ffffffff-ffff-4fff-8fff-ffffffffffff";
const GROUP = "1859897297556589312";
const scope = { workspace_id: "workspace", robot_id: "c", agent_id: C };
const quiet = { warn() {}, log() {}, error() {} };
function makeJob() {
  return { request_id: REQUEST, generation: GENERATION, lease_token: LEASE, group_id: GROUP,
    sender: "alice", inviter_robot_ids: ["c"], binding: { binding_id: REQUEST, generation: GENERATION, issue_id: ISSUE,
      identifier: "TEST-1", workspace_slug: "team", group: { state: "pending", leader_agent_id: A,
        agent_ids: [A, B], agent_names: { [A]: "leader", [B]: "member" } } } };
}

function fixture(t) {
  const dir = mkdtempSync(join(tmpdir(), "web-infoflow-groups-")), statePath = join(dir, "state");
  const entries = [["a", A, "101"], ["b", B, "202"], ["c", C, "303"]].map(([robotId, agentId, appId]) =>
    ({ robotId, agentId, appId, client: { id: robotId }, send: async () => assert.fail("no group messages in binding job") }));
  const state = { bots: ["303"], members: ["alice"], invites: [], reads: 0, now: 1000, outcome: "membership_unconfirmed",
    requests: [], observations: [] };
  const options = { statePath, now: () => state.now,
    candidateRobotIds: () => state.candidates ?? [],
    entries: async (ws, ids, opts = {}) => {
      assert.equal(ws, scope.workspace_id);
      return entries.filter(entry => ids.includes(entry.agentId) || opts.includeRobotId === entry.robotId);
    },
    roster: async (client, id) => {
      assert.equal(client.id, "c"); assert.equal(id, GROUP);
      state.reads++;
      if (state.onRead) await state.onRead(state.reads);
      return state.roster ?? { bots: state.bots.map(appId => ({ appId, imId: null })), members: [...state.members] };
    },
    invite: async (client, id, appId) => {
      assert.equal(client.id, "c"); assert.equal(id, GROUP);
      assert.ok(state.members.includes("alice")); assert.ok(state.bots.includes("303"));
      state.invites.push(appId);
      if (state.onInvite) return await state.onInvite(appId);
      if (state.join) state.bots.push(appId);
      return state.outcome;
    } };
  let groups = new IssueGroups(options);
  const relay = new IssueRelay({ robotId: "c", statePath, issueRelay: { enabled: true }, pollIntervalMs: 3600000,
    multica: { defaultAgent: C, workspaceId: scope.workspace_id, serverUrl: "https://api.example.invalid" } }, {},
  async () => assert.fail("web binding must not send messages or run agents"), quiet, { groups });
  const job = makeJob();
  relay.request = async (path, body) => {
    state.requests.push({ path, body: structuredClone(body) });
    if (path === "web-group-jobs") return { jobs: state.noJob ? [] : [structuredClone(job)] };
    if (path === "web-group-result") return { ok: true, state: body.actor_present && body.leader_present ? "active" : "pending" };
    if (path === "infoflow-group-observation") { state.observations.push(body); return { ok: true }; }
    if (path === "issue-groups") return { groups: [] };
    if (path === "issue-deliveries") return { deliveries: [] };
    throw new Error("unexpected endpoint");
  };
  t.after(() => { relay.stop(); rmSync(dir, { recursive: true, force: true }); });
  return { state, entries, relay, job, dir, get groups() { return groups; },
    inspect: () => inspectWebGroupJob(groups, scope, job),
    restore: () => { groups = new IssueGroups(options); relay.groups = groups; } };
}

test("official userInfoList.userId is parsed, canonicalized, bounded and exact bot IDs retained", async () => {
  const calls = [];
  const client = { httpClient: { post: async (...args) => { calls.push(args); return { errcode: 0, data: {
    agentInfoList: [{ agentId: GROUP, imId: "1859897297556589322" }], userInfoList: [{ userId: "Alice" }, { userId: "bob01" }] } }; } } };
  assert.deepEqual(await groupRoster(client, GROUP), { bots: [{ appId: GROUP, imId: "1859897297556589322" }], members: ["alice", "bob01"] });
  assert.deepEqual(await groupMembers(client, GROUP), [{ appId: GROUP, imId: "1859897297556589322" }]);
  assert.equal(calls[0][1], `{"groupId":${GROUP},"recallType":0}`);
  assert.equal(calls[0][2].disableRetry, true);
});

test("malformed or forged roster account fields fail closed instead of assuming applicant presence", async () => {
  for (const users of [null, [{ userName: "alice" }], [{ email: "alice@example.test" }], [{ userId: 1 }],
    [{ userId: " alice" }], [{ userId: "alice@example.test" }], [{ userId: "../alice" }],
    [{ userId: "alice" }, { userId: "ALICE" }], [{ userId: "a".repeat(129) }], Array(5001).fill({ userId: "alice" })]) {
    await assert.rejects(groupRoster({ httpClient: { post: async () => ({ errcode: 0, data: { agentInfoList: [], userInfoList: users } }) } }, GROUP));
  }
  await assert.rejects(groupRoster({ httpClient: { post: async () => ({ errcode: 0, data: {
    agentInfoList: [{ agentId: "101" }, { agentId: "101" }], userInfoList: [] } }) } }, GROUP));
  await assert.rejects(groupRoster({ httpClient: { post: async () => ({ code: "ok", data: {
    errcode: 40000, errmsg: "robot outside group", data: { agentInfoList: [], userInfoList: [] } } }) } }, GROUP));
});

test("outside applicant, outsider inviter and malformed roster produce zero invitation side effects", async t => {
  for (const failure of ["actor", "caller", "malformed", "mapping", "wrong-scope"]) await t.test(failure, async t => {
    const f = fixture(t);
    if (failure === "actor") f.state.members = ["bob"];
    if (failure === "caller") f.state.bots = [];
    if (failure === "malformed") f.state.roster = { bots: [{ appId: "303" }], members: ["alice\nowner"] };
    if (failure === "mapping") f.entries[2].appId = Number(GROUP);
    if (failure === "wrong-scope") f.entries[2].agentId = B;
    const result = await f.inspect();
    assert.equal(result.leader_present, false);
    assert.ok(["actor_not_in_group", "inviter_not_in_group", "membership_check_failed"].includes(result.reason));
    assert.deepEqual(f.state.invites, []);
  });
});

test("required leader and configured squad member are invited, confirmed only after final fresh read", async t => {
  const f = fixture(t); f.state.join = true;
  const result = await f.inspect();
  assert.deepEqual(f.state.invites, ["101", "202"]);
  assert.equal(result.actor_present, true); assert.equal(result.leader_present, true);
  assert.equal(result.leader_robot_id, "a"); assert.equal(result.reason, "");
  assert.ok(f.state.reads >= 4);
});

test("single-agent task needs only its robot and never invites unrelated configured bots", async t => {
  const f = fixture(t); f.state.join = true; f.job.binding.group.agent_ids = [A];
  assert.equal((await f.inspect()).leader_present, true);
  assert.deepEqual(f.state.invites, ["101"]);
});

test("approval, failed and uncertain invitation never become binding success without leader presence", async t => {
  for (const outcome of ["approval_pending", "invitation_failed", "membership_unconfirmed", "garbage"]) await t.test(outcome, async t => {
    const f = fixture(t); f.state.outcome = outcome;
    const result = await f.inspect();
    assert.equal(result.actor_present, true); assert.equal(result.leader_present, false);
    assert.equal(result.reason, outcome === "garbage" ? "invitation_failed" : outcome);
  });
});

test("optional member failure, missing or ambiguous configuration does not block confirmed leader", async t => {
  for (const failure of ["failed", "missing", "ambiguous", "unavailable", "lookup"]) await t.test(failure, async t => {
    const f = fixture(t); f.state.bots.push("101");
    if (failure === "missing") f.entries.splice(1, 1);
    if (failure === "ambiguous") f.entries.push({ ...f.entries[1], robotId: "b2", appId: "404" });
    if (failure === "unavailable") f.entries[1].client = null;
    if (failure === "lookup") {
      const original = f.groups.entries;
      f.groups.entries = async (...args) => { if (args[1].includes(B)) throw new Error("optional lookup unavailable"); return original(...args); };
    }
    f.state.onInvite = async () => { throw new Error("optional invitation failed"); };
    assert.equal((await f.inspect()).leader_present, true);
  });
});

test("leader missing, ambiguous, offline or colliding with inviter cannot be activated or invited", async t => {
  for (const failure of ["missing", "ambiguous", "offline", "collision"]) await t.test(failure, async t => {
    const f = fixture(t);
    if (failure === "missing") f.entries.splice(0, 1);
    if (failure === "ambiguous") f.entries.push({ ...f.entries[0], robotId: "a2", appId: "404" });
    if (failure === "offline") f.entries[0].client = null;
    if (failure === "collision") f.entries[0].appId = "303";
    const result = await f.inspect();
    assert.equal(result.leader_present, false);
    assert.ok(["leader_robot_missing_or_ambiguous", "leader_robot_unavailable"].includes(result.reason));
    assert.deepEqual(f.state.invites, []);
  });
});

test("actor or inviter leaving before any later invite stops optional side effects and activation", async t => {
  for (const identity of ["actor", "inviter"]) await t.test(identity, async t => {
    const f = fixture(t); f.state.join = true;
    f.state.onRead = count => { if (count === 3) { if (identity === "actor") f.state.members = []; else f.state.bots = ["101"]; } };
    const result = await f.inspect();
    assert.equal(result.leader_present, false);
    assert.equal(result.reason, identity === "actor" ? "actor_not_in_group" : "inviter_not_in_group");
    assert.deepEqual(f.state.invites, ["101"]);
  });
});

test("final roster failure or applicant departure cannot activate after successful invites", async t => {
  for (const failure of ["departed", "malformed", "throw"]) await t.test(failure, async t => {
    const f = fixture(t); f.state.join = true;
    f.state.onRead = count => { if (count === 4) {
      if (failure === "departed") f.state.members = [];
      if (failure === "malformed") f.state.roster = {};
      if (failure === "throw") throw new Error("roster read timed out");
    } };
    assert.equal((await f.inspect()).leader_present, false);
    assert.deepEqual(f.state.invites, ["101", "202"]);
  });
});

test("generation invite budget and exponential delay survive restoration and new leases", async t => {
  const f = fixture(t); f.job.binding.group.agent_ids = [A];
  await f.inspect(); assert.equal(f.state.invites.length, 1);
  f.restore(); f.job.lease_token = LEASE2;
  await f.inspect(); assert.equal(f.state.invites.length, 1);
  for (let i = 0; i < 5; i++) { f.state.now += 5000000; await f.inspect(); }
  assert.equal(f.state.invites.length, 3);
  f.job.generation = f.job.binding.generation = "dddddddd-dddd-4ddd-8ddd-dddddddddddd";
  await f.inspect(); assert.equal(f.state.invites.length, 4);
  assert.ok(!readFileSync(f.groups.path, "utf8").includes(LEASE2));
  assert.doesNotMatch(readFileSync(f.groups.path, "utf8"), /alice|secret|member text/);
});

test("same generation cannot change actor, group or task topology to reset invitation authority", async t => {
  const f = fixture(t); await f.inspect();
  f.job.sender = "bob";
  await assert.rejects(f.inspect(), /generation target changed/);
  assert.equal(f.state.invites.length, 2);
});

test("job schema refuses rounded IDs, changed generation/identity and invented roster metadata", () => {
  for (const mutate of [job => { job.group_id = Number(GROUP); }, job => { job.group_id = "01"; },
    job => { job.group_id = "9223372036854775808"; }, job => { job.lease_token = ""; }, job => { job.sender = "Alice"; },
    job => { job.binding.generation = REQUEST; }, job => { job.binding.binding_id = ISSUE; },
    job => { job.binding.group.agent_ids = [B]; }, job => { delete job.binding.group.agent_names[A]; },
    job => { job.inviter_robot_ids = ["c", "c"]; }, job => { job.sender = "alice@example.test"; }]) {
    const job = makeJob(); mutate(job); assert.throws(() => validateWebGroupJob(job));
  }
});

test("polling posts exact lease/generation with unchanged caller scope and independently resolved leader", async t => {
  const f = fixture(t); f.state.join = true;
  await f.relay.pollWebBindings();
  const result = f.state.requests.find(item => item.path === "web-group-result").body;
  assert.deepEqual(result, { ...scope, request_id: REQUEST, generation: GENERATION, lease_token: LEASE, group_id: GROUP,
    actor_present: true, leader_present: true, leader_agent_id: A, leader_robot_id: "a", reason: "" });
  assert.ok(!f.state.requests.some(item => item.path === "issue-deliveries"));
});

test("two workspace timers cannot poll or execute web binding jobs concurrently", async t => {
  const f = fixture(t); let release;
  const gate = new Promise(resolve => { release = resolve; });
  const original = f.relay.request;
  f.relay.request = async (...args) => { if (args[0] === "web-group-jobs") await gate; return original(...args); };
  const first = pollWebGroupJobs(f.relay, scope);
  await Promise.resolve();
  await pollWebGroupJobs(f.relay, scope);
  release(); await first;
  assert.equal(f.state.requests.filter(item => item.path === "web-group-jobs").length, 1);
  assert.equal(f.state.invites.length, 2);
});

test("result transport failure/reclaimed lease performs fresh proof but does not repeat invitations", async t => {
  const f = fixture(t); f.state.join = true;
  const original = f.relay.request;
  f.relay.request = async (path, body) => { if (path === "web-group-result") throw Object.assign(new Error("stale lease"), { status: 409 }); return original(path, body); };
  await f.relay.pollWebBindings(); assert.equal(f.state.invites.length, 2);
  f.restore(); f.job.lease_token = LEASE2; f.state.members = [];
  f.relay.request = original;
  await f.relay.pollWebBindings();
  assert.equal(f.state.invites.length, 2);
  assert.equal(f.state.requests.findLast(item => item.path === "web-group-result").body.actor_present, false);
});

test("malformed job responses cannot block established relay polling", async t => {
  for (const response of [null, {}, { jobs: [makeJob(), makeJob()] }, { jobs: [{ group_id: GROUP }] }]) await t.test(String(response), async t => {
    const f = fixture(t), original = f.relay.request;
    f.relay.request = async (...args) => args[0] === "web-group-jobs" ? response : original(...args);
    await f.relay.pollWebBindings();
    await f.relay.poll();
    assert.equal(f.state.invites.length, 0);
    assert.ok(f.state.requests.some(item => item.path === "issue-deliveries"));
  });
});

test("stopping before or during pre-invite membership I/O prevents invitations and result submission", async t => {
  for (const stopRead of [1, 2]) await t.test(String(stopRead), async t => {
    const f = fixture(t); f.state.onRead = count => { if (count === stopRead) f.relay.stop(); };
    await f.relay.pollWebBindings();
    assert.equal(f.state.invites.length, 0);
    assert.equal(f.state.requests.filter(item => item.path === "web-group-result").length, 0);
  });
});

test("discovery posts only genuinely verified roster, throttles event bursts and excludes outsider robots", async t => {
  const f = fixture(t); const robot = { robotId: "c", appId: "303", client: { id: "c" } };
  const publish = body => f.state.observations.push(body);
  await Promise.all(Array.from({ length: 4 }, () => f.groups.observeGroup(scope, robot, GROUP, publish)));
  assert.deepEqual(f.state.observations, [{ ...scope, group_id: GROUP, name: "", members: ["alice"] }]);
  assert.equal(f.state.reads, 1);
  f.state.now += 60001; f.state.bots = [];
  await assert.rejects(f.groups.observeGroup(scope, robot, GROUP, publish), /not in group/);
  assert.equal(f.state.observations.length, 1);
});

test("genuine forwarding events discover groups without dispatch or trusting event sender", async () => {
  const handlers = new Map(), observed = [], delivered = [];
  registerHandlers({ wsClient: { on: (event, callback) => handlers.set(event, callback) }, robot: { id: "c", appId: "303", name: "test" },
    client: { fixture: true }, logger: quiet, bridge: { issueRelay: { observeGroupEvent: async data => observed.push(data) }, handleMessage: async data => delivered.push(data) } });
  await handlers.get("group.text")({ data: { raw: { eventtype: "ALL_MESSAGE_FORWARD", groupid: GROUP, message: {
    header: { fromuserid: "claimed-outsider" }, body: [{ type: "TEXT", content: "not an @ request" }] } } } });
  assert.equal(observed[0].groupId, GROUP);
  assert.equal(Object.hasOwn(observed[0], "sender"), false);
  assert.deepEqual(delivered, []);
});

test("discovery failures are isolated from normal input and never expose roster or SDK errors", async t => {
  const f = fixture(t); const warnings = [];
  f.relay.logger = { warn: value => warnings.push(value) };
  f.groups.roster = async () => { throw new Error("sensitive-sdk-token"); };
  await f.relay.observeGroupEvent({ groupId: GROUP, appId: "303", client: { id: "c" } });
  assert.equal(f.state.observations.length, 0);
  assert.equal(warnings.length, 1); assert.doesNotMatch(warnings[0], /sensitive/);
});

test("malformed discovery acknowledgement is not accepted as a stored observation", async t => {
  const f = fixture(t), warnings = [];
  f.relay.logger = { warn: value => warnings.push(value) };
  f.relay.request = async () => ({});
  await f.relay.observeGroupEvent({ groupId: GROUP, appId: "303", client: { id: "c" } });
  assert.equal(warnings.length, 1);
});

test("caller-only lookup excludes unrelated ambiguous names instead of using them for authorization", async () => {
  const config = { multica: { workspaceId: "workspace", cli: "/missing-fake-cli" }, robots: [
    { id: "c", agent: C, appId: "303", enabled: true }, { id: "obsolete", agent: "deleted-name", appId: "404", enabled: true } ] };
  const instances = new Map([["c", { client: {}, send() {} }], ["obsolete", {
    bridge: { multica: { listAgents: async () => assert.fail("unrelated name lookup must not run") } } }]]);
  const entries = await groupRobotEntries(config, instances, "workspace", [], quiet, { agentNames: {}, includeRobotId: "c" });
  assert.deepEqual(entries.map(entry => entry.robotId), ["c"]);
});

test("manual group without prior observations is freshly probed and reports actionable non-membership", async t => {
  const f = fixture(t); f.job.inviter_robot_ids = [];
  f.state.bots = [];
  assert.equal((await f.inspect()).reason, "inviter_not_in_group");
  assert.deepEqual(f.state.invites, []);
  f.state.bots = ["303"]; f.state.join = true;
  assert.equal((await f.inspect()).leader_present, true);
  assert.deepEqual(f.state.invites, ["101", "202"]);
});

test("known in-group SDK inviter may execute a different robot's lease, with reporting scope unchanged", async t => {
  const f = fixture(t);
  f.entries.push({ robotId: "d", agentId: "44444444-4444-4444-8444-444444444444", appId: "404", client: { id: "d" }, send() {} });
  f.job.inviter_robot_ids = ["d"];
  f.state.bots = ["404"];
  f.groups.roster = async (client, id) => {
    assert.equal(id, GROUP);
    if (client.id === "c") throw new Error("caller cannot query outside group");
    assert.equal(client.id, "d");
    return { bots: f.state.bots.map(appId => ({ appId })), members: ["alice"] };
  };
  f.groups.invite = async (client, id, appId) => {
    assert.equal(client.id, "d"); assert.equal(id, GROUP);
    f.state.invites.push(appId); f.state.bots.push(appId);
    return "membership_unconfirmed";
  };
  await f.relay.pollWebBindings();
  const result = f.state.requests.find(item => item.path === "web-group-result").body;
  assert.equal(result.robot_id, "c"); assert.equal(result.agent_id, C);
  assert.equal(result.leader_robot_id, "a"); assert.equal(result.leader_present, true);
  assert.deepEqual(f.state.invites, ["101", "202"]);
});

test("cached inviter observation never permits invitation when that robot has since left", async t => {
  const f = fixture(t);
  f.entries.push({ robotId: "d", agentId: "44444444-4444-4444-8444-444444444444", appId: "404", client: { id: "d" }, send() {} });
  f.job.inviter_robot_ids = ["d"];
  f.groups.roster = async () => ({ bots: [{ appId: "101" }], members: ["alice"] });
  const result = await f.inspect();
  assert.equal(result.reason, "inviter_not_in_group"); assert.equal(result.leader_present, false);
  assert.deepEqual(f.state.invites, []);
});

test("malformed unrelated inviter configuration is skipped without becoming authorization or hiding real caller", async t => {
  const f = fixture(t); f.state.join = true;
  f.job.inviter_robot_ids = ["obsolete", "c"];
  const original = f.groups.entries;
  f.groups.entries = async (...args) => {
    if (args[2]?.includeRobotId === "obsolete") throw new Error("unresolved private name");
    return original(...args);
  };
  assert.equal((await f.inspect()).leader_present, true);
  assert.deepEqual(f.state.invites, ["101", "202"]);
});

test("actual SDK partial invitation failure remains pending across restored polling, no blind resend", async t => {
  const f = fixture(t); f.job.binding.group.agent_ids = [A];
  const sdkCalls = [];
  const client = { httpClient: { post: async (path, raw) => {
    sdkCalls.push([path, raw]);
    if (path === "/robot/group/memberList") return { code: "ok", data: { errcode: 0, data: {
      agentInfoList: [{ agentId: "303", imId: "9303" }], userInfoList: [{ userId: "alice" }] } } };
    if (path === "/robot/group/addMembers") return { code: "ok", data: { errcode: 0, data: { failAgentIds: [101] } } };
    assert.fail("unexpected fake SDK path");
  } } };
  f.entries[2].client = client;
  f.groups.roster = groupRoster;
  const { inviteGroupLeader } = await import("./src/issue-groups.js");
  f.groups.invite = inviteGroupLeader;
  const checked = await f.inspect();
  assert.equal(checked.leader_present, false); assert.equal(checked.reason, "invitation_failed");
  assert.equal(sdkCalls.filter(([path]) => path === "/robot/group/addMembers").length, 1);
  f.restore(); f.groups.roster = groupRoster; f.groups.invite = inviteGroupLeader;
  assert.equal((await f.inspect()).reason, "invitation_failed");
  assert.equal(sdkCalls.filter(([path]) => path === "/robot/group/addMembers").length, 1);
});

test("a successful result response cannot claim active if worker did not prove actor and leader", async t => {
  const f = fixture(t); const original = f.relay.request;
  f.relay.request = async (...args) => args[0] === "web-group-result" ? { ok: true, state: "active" } : original(...args);
  await assert.rejects(pollWebGroupJobs(f.relay, scope), /invalid web binding result/);
});

test("discovery preserves bounded cache and retries only after the event throttle period", async t => {
  const f = fixture(t), robot = { robotId: "c", appId: "303", client: { id: "c" } };
  f.groups.roster = async () => ({ bots: [{ appId: "303" }], members: ["alice"] });
  for (let id = 1; id <= 1002; id++) await f.groups.observeGroup(scope, robot, String(id), () => {});
  assert.equal(f.groups.discoveryAttempts.size, 1000);
  assert.equal(f.groups.discoveryAttempts.has(JSON.stringify([scope.workspace_id, "c", "1"])), false);
});

test("expired local processing budget cannot invite after slow membership proof", async t => {
  const f = fixture(t), original = Date.now;
  let now = 1000;
  Date.now = () => now;
  try {
    f.state.onRead = count => { if (count === 2) now += 60001; };
    const checked = await f.inspect();
    assert.equal(checked.leader_present, false);
    assert.equal(checked.reason, "membership_check_failed");
    assert.deepEqual(f.state.invites, []);
  } finally { Date.now = original; }
});

test("unobserved manual group finds an unrelated configured in-group robot despite outside worker claiming first", async t => {
  const f = fixture(t);
  const D = "44444444-4444-4444-8444-444444444444";
  f.entries.push({ robotId: "d", agentId: D, appId: "404", client: { id: "d" }, send() {} });
  f.job.inviter_robot_ids = []; f.state.candidates = ["c", "obsolete", "d"];
  f.state.bots = ["404"];
  const resolved = [], queried = [];
  const original = f.groups.entries;
  f.groups.entries = async (...args) => {
    const id = args[2]?.includeRobotId;
    if (id) resolved.push(id);
    if (id === "obsolete") throw new Error("ambiguous unrelated name");
    return original(...args);
  };
  f.groups.roster = async (client, id) => {
    assert.equal(id, GROUP); queried.push(client.id);
    if (client.id === "c") {
      // A queryable outside client must still never be used to invite.
      return { bots: [{ appId: "404" }], members: ["alice"] };
    }
    assert.equal(client.id, "d");
    return { bots: f.state.bots.map(appId => ({ appId })), members: ["alice"] };
  };
  f.groups.invite = async (client, id, appId) => {
    assert.equal(client.id, "d"); assert.equal(id, GROUP);
    f.state.invites.push(appId); f.state.bots.push(appId);
    return "membership_unconfirmed";
  };
  await f.relay.pollWebBindings();
  assert.deepEqual(resolved, ["c", "obsolete", "d"]);
  assert.deepEqual(queried.slice(0, 2), ["c", "d"]);
  assert.deepEqual(f.state.invites, ["101", "202"]);
  const result = f.state.requests.find(item => item.path === "web-group-result").body;
  assert.equal(result.leader_present, true); assert.equal(result.actor_present, true);
  assert.equal(result.robot_id, "c"); assert.equal(result.lease_token, LEASE);
});

test("all configured callers outside with structured 40000 give actionable no-in-group-robot reason", async t => {
  const f = fixture(t);
  f.job.inviter_robot_ids = []; f.state.candidates = ["a", "b", "c"];
  f.groups.roster = groupRoster;
  for (const entry of f.entries) entry.client = { httpClient: { post: async () => ({
    code: "ok", data: { errcode: 40000, errmsg: "机器人未在群成员列表中" },
  }) } };
  const checked = await f.inspect();
  assert.equal(checked.reason, "inviter_not_in_group");
  assert.equal(checked.leader_present, false); assert.equal(checked.actor_present, false);
  assert.deepEqual(f.state.invites, []);
  f.entries[0].client.httpClient.post = async () => ({ code: "ok", data: { errcode: 40000, errmsg: "different failure" } });
  assert.equal((await f.inspect()).reason, "membership_check_failed");
});

test("runtime candidates are enabled business robot IDs scoped dynamically without resolving names", async t => {
  const f = fixture(t);
  const config = { statePath: join(f.dir, "candidate-state"), issueRelay: { enabled: true }, multica: { workspaceId: "workspace" }, robots: [
    { id: "a", enabled: true, agent: "ambiguous-legacy-name" },
    { id: "b", enabled: true, workspaceId: "workspace" },
    { id: "disabled", enabled: false }, { id: "verification", enabled: true, purpose: "verification" },
    { id: "other", enabled: true, workspaceId: "another-workspace" },
  ] };
  const runtime = new RobotRuntime(config, quiet);
  assert.deepEqual(runtime.issueGroups.candidateRobotIds("workspace"), ["a", "b"]);
  assert.deepEqual(runtime.issueGroups.candidateRobotIds("another-workspace"), ["other"]);
  runtime.config = { ...config, robots: [{ id: "new", enabled: true }] };
  assert.deepEqual(runtime.issueGroups.candidateRobotIds("workspace"), ["new"]);
});

test("candidate probes are bounded and advance durably to robots beyond the first batch", async t => {
  const f = fixture(t);
  f.job.inviter_robot_ids = [];
  const ids = Array.from({ length: MAX_WEB_GROUP_CANDIDATES + 5 }, (_, index) => `candidate-${index}`);
  f.state.candidates = ids;
  for (const id of ids) f.entries.push({ robotId: id, agentId: C, appId: "404", client: { id }, send() {} });
  const inside = ids.at(-1), queries = [];
  f.groups.roster = async client => {
    queries.push(client.id);
    if (client.id !== inside) return { bots: [], members: ["alice"] };
    return { bots: [{ appId: "404" }, { appId: "101" }, { appId: "202" }], members: ["alice"] };
  };
  assert.equal((await f.inspect()).leader_present, false);
  assert.equal(queries.length, MAX_WEB_GROUP_CANDIDATES);
  assert.ok(!queries.includes(inside));
  // Restore the coordinator to prove the scan progress was saved, not local.
  const roster = f.groups.roster;
  f.restore(); f.groups.roster = roster;
  queries.length = 0;
  assert.equal((await f.inspect()).leader_present, true);
  assert.ok(queries.includes(inside)); assert.ok(queries.length <= MAX_WEB_GROUP_CANDIDATES);
  assert.deepEqual(f.state.invites, []);
});

test("invalid or oversized configured candidate lists fail closed without global catalog lookups", async t => {
  const f = fixture(t);
  f.groups.candidateRobotIds = async () => Array(1001).fill("c");
  assert.equal((await f.inspect()).reason, "membership_check_failed");
  assert.equal(f.state.reads, 0); assert.deepEqual(f.state.invites, []);
});

test("leader and member rename while approval pending keeps generation budgets and freshly resolves UUID mappings", async t => {
  const f = fixture(t); f.state.outcome = "approval_pending";
  const observedNames = [], original = f.groups.entries;
  f.groups.entries = async (...args) => {
    if (args[1].length) observedNames.push({ ids: [...args[1]], names: { ...args[2].agentNames } });
    return original(...args);
  };
  assert.equal((await f.inspect()).reason, "approval_pending");
  assert.deepEqual(f.state.invites, ["101", "202"]);
  f.job.binding.group.agent_names = { [A]: "renamed-leader", [B]: "renamed-member" };
  f.job.lease_token = LEASE2;
  assert.equal((await f.inspect()).reason, "approval_pending");
  assert.deepEqual(f.state.invites, ["101", "202"]);
  assert.equal(observedNames.at(-1).names[B], "renamed-member");
  assert.ok(observedNames.some(item => item.ids.includes(A) && item.names[A] === "renamed-leader"));
  f.state.bots.push("101");
  assert.equal((await f.inspect()).leader_present, true);
  assert.deepEqual(f.state.invites, ["101", "202"]);
});

test("blocked web membership job cannot delay existing group reconciliation, delivery or acknowledgement", async t => {
  const f = fixture(t), original = f.relay.request;
  let release, entered;
  const blocked = new Promise(resolve => { release = resolve; });
  const started = new Promise(resolve => { entered = resolve; });
  const originalRoster = f.groups.roster;
  f.groups.roster = async (...args) => { entered(); await blocked; return originalRoster(...args); };
  f.groups.members = async () => [{ appId: "303", imId: null }];
  f.relay.config.multica.webUrl = "https://web.example.invalid";
  const binding = { binding_id: "existing-binding", generation: "existing-generation", issue_id: ISSUE,
    identifier: "TEST-2", workspace_slug: "team", group: { state: "active", leader_agent_id: C,
      leader_robot_id: "c", agent_ids: [C], agent_names: { [C]: "existing-worker" } } };
  const conversation = { kind: "group", id: GROUP }, progress = [];
  f.entries[2].send = async () => { progress.push("sent"); return { messageid: "987" }; };
  f.relay.request = async (path, body) => {
    if (path === "issue-groups") return { groups: [{ conversation, binding: structuredClone(binding) }] };
    if (path === "issue-group-status") {
      progress.push("reconciled");
      return { handled: true, silent: true, binding: structuredClone(binding) };
    }
    if (path === "issue-deliveries") return { deliveries: [{ id: "existing-delivery", conversation,
      binding: structuredClone(binding), author_agent_id: C, author_name: "existing-worker", content: "已有任务继续回复" }] };
    if (path.endsWith("/ack")) { progress.push("acked"); return { handled: true, silent: true }; }
    return original(path, body);
  };
  const web = f.relay.pollWebBindings();
  await started;
  const output = f.relay.poll();
  let timeout;
  try {
    await Promise.race([output, new Promise((_, reject) => {
      timeout = setTimeout(() => reject(new Error("existing traffic waited on web job")), 1000);
    })]);
    assert.equal(f.relay.webPolling, true);
    assert.ok(progress.includes("reconciled"));
    assert.deepEqual(progress.slice(-2), ["sent", "acked"]);
    assert.equal(f.state.requests.filter(item => item.path === "web-group-result").length, 0);
  } finally {
    clearTimeout(timeout); release(); await web; await output;
  }
});

test("independent web poll is non-reentrant and stopped relays never claim further jobs", async t => {
  const f = fixture(t), original = f.relay.request;
  let release, entered;
  const blocked = new Promise(resolve => { release = resolve; });
  const started = new Promise(resolve => { entered = resolve; });
  f.relay.request = async (...args) => {
    if (args[0] === "web-group-jobs") { entered(); await blocked; }
    return original(...args);
  };
  const first = f.relay.pollWebBindings();
  await started;
  await f.relay.pollWebBindings();
  f.relay.stop(); release(); await first;
  assert.equal(f.state.requests.filter(item => item.path === "web-group-jobs").length, 1);
  assert.deepEqual(f.state.invites, []);
  assert.equal(f.state.requests.filter(item => item.path === "web-group-result").length, 0);
  await f.relay.pollWebBindings();
  assert.equal(f.state.requests.filter(item => item.path === "web-group-jobs").length, 1);
});

test("stop clears all three independent timers and callbacks use separate methods", async t => {
  const timers = new Map();
  let unrefs = 0, outputPolls = 0, webPolls = 0, privatePolls = 0;
  t.mock.method(globalThis, "setInterval", (callback, interval) => {
    const token = { unref() { unrefs++; } };
    timers.set(token, { callback, interval }); return token;
  });
  t.mock.method(globalThis, "clearInterval", token => { timers.delete(token); });
  const f = fixture(t);
  assert.equal(timers.size, 3); assert.equal(unrefs, 3);
  f.relay.poll = async () => { outputPolls++; };
  f.relay.pollWebBindings = async () => { webPolls++; };
  f.relay.pollWebPrivateBindings = async () => { privatePolls++; };
  for (const timer of timers.values()) timer.callback();
  await Promise.resolve();
  assert.equal(outputPolls, 1); assert.equal(webPolls, 1); assert.equal(privatePolls, 1);
  f.relay.stop();
  assert.equal(timers.size, 0);
  assert.equal(f.relay.timer, null); assert.equal(f.relay.webJobTimer, null);
  assert.equal(f.relay.webPrivateJobTimer, null);
});
