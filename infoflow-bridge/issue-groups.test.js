import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createHash } from "node:crypto";
import { IssueGroups, exactGroupId, groupMembers, inviteGroupLeader, sendGroupExact, botOrigin } from "./src/issue-groups.js";
import { Bridge } from "./src/bridge.js";

const A = "11111111-1111-4111-8111-111111111111";
const B = "22222222-2222-4222-8222-222222222222";
const C = "33333333-3333-4333-8333-333333333333";
const GROUP = "1859897297556589312";
const conversation = { kind: "group", id: GROUP };
const quiet = { log() {}, error() {}, warn() {} };
const pending = () => ({ binding_id: "binding-1", issue_id: "issue-1", identifier: "TEST-1", workspace_slug: "team",
  generation: "11111111-0000-4000-8000-000000000001", group: { state: "pending", leader_agent_id: A, leader_robot_id: "", agent_ids: [A, B] } });

// Transport-free integration harness. Every external action is an explicit
// fake and the service rejects fields/scopes absent from the Go wire contract.
function fixture(t) {
  const directory = mkdtempSync(join(tmpdir(), "infoflow-issue-groups-"));
  const statePath = join(directory, "state.json");
  const state = { binding: pending(), members: ["101"], invitations: [], memberQueries: [], requests: [],
    sent: [], deliveries: [], acked: new Set(), inviteResult: "approval_pending", joinOnInvite: false,
    canReconcile: true, canComment: true, failMembers: false, failAck: false, failPart: 0, now: 1000 };
  const entries = [
    { robotId: "a", agentId: A, appId: "101" }, { robotId: "b", agentId: B, appId: "202" },
    { robotId: "c", agentId: C, appId: "303" },
  ];
  for (const entry of entries) {
    entry.client = { id: entry.robotId };
    entry.send = async (target, content, options) => {
      if (state.failPart && state.sent.length + 1 === state.failPart) throw new Error("fake send failure");
      state.sent.push({ robotId: entry.robotId, target, content, options });
      if (state.afterSend) await state.afterSend();
      return { messageid: String(1859897297556590000n + BigInt(state.sent.length)) };
    };
  }
  const groupOptions = {
    statePath, now: () => state.now,
    entries: async (workspace, ids) => workspace === "workspace" ? entries.filter(entry => !ids || ids.includes(entry.agentId)) : [],
    members: async (client, groupId) => {
      assert.equal(groupId, GROUP);
      state.memberQueries.push(client.id);
      if (state.failMembers || client.id === "c") throw new Error("fake permission denied");
      return state.members.map(appId => ({ appId, imId: `9${appId}` }));
    },
    invite: async (client, groupId, appId) => {
      state.invitations.push({ robot: client.id, groupId, appId });
      if (state.joinOnInvite) state.members.push(String(appId));
      return state.inviteResult;
    },
  };
  let groups = new IssueGroups(groupOptions);
  const bridges = [];
  const make = (id = "a") => {
    const entry = entries.find(entry => entry.robotId === id);
    const config = { robotId: id, statePath, issueRelay: { enabled: true }, pollIntervalMs: 3600000,
      allowUsers: [], allowAllInGroups: true,
      multica: { defaultAgent: entry.agentId, workspaceId: "workspace", workspaceSlug: "team",
        webUrl: "https://team.example.invalid", serverUrl: "https://api.example.invalid", cli: "/no-real-cli" } };
    const bridge = new Bridge(config, entry.send, quiet, { issueGroups: groups });
    bridge.chat.send = async () => { throw new Error("unexpected legacy Chat fallback"); };
    bridge.issueRelay.request = async (path, body) => {
      state.requests.push({ path, body: structuredClone(body) });
      if (path === "issue-groups") return { groups: state.binding ? [{ conversation, binding: structuredClone(state.binding) }] : [] };
      if (path === "issue-group-status") {
        assert.deepEqual(Object.keys(body).sort(), ["robot_id", "agent_id", "workspace_id", "conversation", "binding_id", "generation", "leader_agent_id", "leader_robot_id", "leader_present"].sort());
        assert.equal(body.binding_id, state.binding.binding_id);
        assert.equal(body.generation, state.binding.generation);
        if (body.leader_robot_id) {
          assert.equal(body.robot_id, body.leader_robot_id);
          assert.equal(body.agent_id, body.leader_agent_id);
        }
        if (body.leader_agent_id !== state.binding.group.leader_agent_id) throw new Error("stale topology");
        state.binding.group.state = body.leader_present ? "active" : "pending";
        state.binding.group.leader_robot_id = body.leader_robot_id;
        return { handled: true, silent: false, binding: structuredClone(state.binding),
          notice: body.leader_present ? "群聊任务绑定已启用" : "等待队长或负责智能体的机器人入群" };
      }
      if (path === "issue-relay") {
        const bound = structuredClone(state.binding);
        const canComment = state.canComment && (body.conversation.kind === "private" || bound?.group.state === "active");
        return { handled: true, silent: false, binding: bound, can_comment: canComment,
          can_reconcile_group: state.canReconcile,
          notice: !state.canComment ? "没有工作区权限，无法评论" : body.operation === "bind" && !canComment ? "任务已关联，等待队长入群" : undefined };
      }
      if (path === "issue-deliveries") return { deliveries: state.deliveries.filter(item => !state.acked.has(item.id)).map(item =>
        ({ ...structuredClone(item), binding: structuredClone(state.binding) })) };
      if (path.endsWith("/ack")) {
        assert.equal(Object.hasOwn(body, "sender_robot_id"), false);
        if (state.failAck) throw new Error("fake ack failure");
        state.acked.add(path.split("/")[1]);
        return { handled: true, silent: true };
      }
      throw new Error(`unexpected fake endpoint ${path}`);
    };
    bridges.push(bridge);
    return bridge;
  };
  const addDelivery = (author = B, content = "成员完成测试", id = "delivery-1") => {
    state.deliveries.push({ id, conversation, author_agent_id: author, author_name: author === B ? "成员 B" : "队长 A", content });
  };
  t.after(() => { for (const bridge of bridges) bridge.stop(); rmSync(directory, { recursive: true, force: true }); });
  return { state, entries, directory, make, addDelivery, get groups() { return groups; },
    restoreGroups() { groups = new IssueGroups(groupOptions); return groups; },
    input: (bridge, text = "/bind TEST-1", messageId = "message-1") => bridge.handleMessage({ conversation, user: "alice", text, messageId }) };
}

function legacyCheckpoint(f, bridge) {
  const content = f.state.deliveries[0].content;
  const parts = bridge.issueRelay.replyParts(content, f.state.binding, 1200);
  f.groups.deliveries["delivery-1"] = { next: 0, digest: createHash("sha256").update(parts.join("")).digest("hex"), messageIds: [],
    route: { robot_id: "a", agent_id: A, delegated: true, label: "【成员 B · 队长代发】\n" } };
  f.groups.saveDeliveries();
  return parts;
}

test("group user mirror uses coordinator without member delegation while agents still use their own robot", async t => {
  const f = fixture(t), bridge = f.make();
  f.state.members.push("202");
  f.addDelivery(A, "网页用户评论", "user-mirror");
  Object.assign(f.state.deliveries[0], { author_type: "user", author_name: "alice", comment_id: "comment1" });
  f.addDelivery(B, "成员回复", "agent-reply");
  await bridge.issueRelay.poll();
  assert.equal(f.state.sent.length, 2);
  assert.equal(f.state.sent[0].robotId, "a");
  assert.match(f.state.sent[0].content, /\n\n@alice：网页用户评论/);
  assert.doesNotMatch(f.state.sent[0].content, /队长代发/);
  assert.match(f.state.sent[0].content, /#comment-comment1\)/);
  assert.deepEqual(f.state.sent[0].options.atUsers || [], []);
  assert.equal(f.state.sent[1].robotId, "b");
  assert.doesNotMatch(f.state.sent[1].content, /@alice|队长代发/);
});

test("group user mirrors cannot route as a squad member or reuse a delegated-member pin", async t => {
  const f = fixture(t), bridge = f.make();
  f.addDelivery(B, "user content");
  Object.assign(f.state.deliveries[0], { author_type: "user", author_name: "alice" });
  await bridge.issueRelay.poll();
  assert.equal(f.state.sent.length, 0);
  const leader = f.entries[0], member = f.entries[1];
  const observation = { leader_present: true, leader, entries: [leader, member], members: [{ appId: "101" }, { appId: "202" }] };
  const item = { ...f.state.deliveries[0], author_agent_id: A, binding: f.state.binding };
  assert.throws(() => f.groups.selectSender(observation, item,
    { robot_id: "b", agent_id: B, delegated: false, label: "" }), /coordinator changed/);
  assert.throws(() => f.groups.selectSender(observation, item,
    { robot_id: "a", agent_id: A, delegated: true, label: "delegated" }), /coordinator changed/);
});

test("group user mirror coordinator and username survive ACK failure and shared checkpoint restoration", async t => {
  const f = fixture(t), bridge = f.make();
  f.addDelivery(A, "user comment", "user-mirror");
  Object.assign(f.state.deliveries[0], { author_type: "user", author_name: "alice", comment_id: "comment1" });
  f.state.failAck = true;
  await bridge.issueRelay.poll();
  assert.equal(f.state.sent.length, 1);
  assert.deepEqual(f.groups.deliveries["user-mirror"].route, { robot_id: "a", agent_id: A, delegated: false, label: "" });
  bridge.stop(); f.restoreGroups();
  f.state.failAck = false;
  await f.make().issueRelay.poll();
  assert.equal(f.state.sent.length, 1);
  assert.equal(f.groups.deliveries["user-mirror"].acked, true);
});

test("group member transport preserves exact numeric IDs and compares app IDs, not IM IDs", async () => {
  const calls = [];
  const client = { httpClient: { post: async (...args) => { calls.push(args); return { errcode: 0, data: {
    agentInfoList: [{ agentId: 101, imId: "1859897297556589322" }], userInfoList: [] } }; } } };
  assert.deepEqual(await groupMembers(client, GROUP), [{ appId: "101", imId: "1859897297556589322" }]);
  assert.equal(calls[0][1], `{"groupId":${GROUP},"recallType":0}`);
  assert.deepEqual(calls[0][2], { disableRetry: true });
  assert.throws(() => exactGroupId(Number(GROUP)), /unsafe/);
  for (const bad of ["-1", "0", "1e3", "01", "9223372036854775808", "1,\"x\":0"]) assert.throws(() => exactGroupId(bad));
});

test("missing/malformed membership never becomes an empty confirmed group", async () => {
  for (const response of [{}, { errcode: 0, data: {} }, { errcode: 201, data: { agentInfoList: [], userInfoList: [] } },
    { errcode: 0, data: { agentInfoList: [{ agentId: Number(GROUP) }], userInfoList: [] } }, { errcode: 403 }]) {
    await assert.rejects(groupMembers({ httpClient: { post: async () => response } }, GROUP));
  }
});

test("invitation inspects approval and partial failure, posts only the leader ID", async () => {
  const calls = [];
  for (const [response, expected] of [
    [{ errcode: 201 }, "approval_pending"],
    [{ errcode: 0, data: { failAgentIds: [101] } }, "invitation_failed"],
    [{ code: "ok", data: { errcode: 0, data: { failAgentIds: [] } } }, "membership_unconfirmed"],
  ]) {
    const client = { httpClient: { post: async (...args) => { calls.push(args); return response; } } };
    assert.equal(await inviteGroupLeader(client, GROUP, "101"), expected);
  }
  for (const call of calls) {
    assert.equal(call[0], "/robot/group/addMembers");
    assert.equal(call[1], `{"groupId":${GROUP},"agentIdList":"101"}`);
    assert.equal(call[2].disableRetry, true);
  }
});

test("group outbound transport retains exact destination and disables implicit SDK retry", async () => {
  const calls = [];
  const client = { httpClient: { post: async (...args) => { calls.push(args); return { errcode: 0, data: { messageid: GROUP } }; } } };
  assert.equal((await sendGroupExact(client, conversation, "hello")).messageid, GROUP);
  assert.ok(calls[0][1].includes(`"toid":${GROUP}`));
  assert.equal(calls[0][2].disableRetry, true);
  assert.ok(calls[0][1].includes('"role":"robot"'));
});

test("SDK code-ok flat and nested envelopes work for all group operations", async () => {
  for (const nested of [false, true]) {
    const envelope = data => ({ code: "ok", data: nested ? { data } : data });
    const members = await groupMembers({ httpClient: { post: async () => envelope({ agentInfoList: [{ agentId: 101, imId: 999 }], userInfoList: [] }) } }, GROUP);
    assert.equal(members[0].appId, "101");
    assert.equal(await inviteGroupLeader({ httpClient: { post: async () => envelope({ failAgentIds: [] }) } }, GROUP, "101"), "membership_unconfirmed");
    assert.equal((await sendGroupExact({ httpClient: { post: async () => envelope({ messageid: GROUP }) } }, conversation, "reply")).messageid, GROUP);
  }
  for (const bad of [{ code: "ok", errcode: 403, data: {} }, { code: "ok", data: { errcode: 403 } }, { code: "error", errcode: 0 }]) {
    await assert.rejects(sendGroupExact({ httpClient: { post: async () => bad } }, conversation, "reply"));
  }
});

test("same-tick concurrent sends generate unique safe client message IDs", async () => {
  const original = Date.now;
  const ids = [];
  Date.now = () => 1700000000000;
  try {
    const client = { httpClient: { post: async (path, raw) => {
      ids.push(JSON.parse(raw).message.header.clientmsgid);
      return { code: "ok", data: { messageid: GROUP } };
    } } };
    await Promise.all(Array.from({ length: 10 }, () => sendGroupExact(client, conversation, "reply")));
    assert.equal(new Set(ids).size, 10);
    assert.ok(ids.every(Number.isSafeInteger));
  } finally { Date.now = original; }
});

test("authorized pending bind invites and rereads before activation even can_comment=false", async t => {
  const f = fixture(t); f.state.members = ["202"]; f.state.joinOnInvite = true;
  const bridge = f.make("b");
  await f.input(bridge);
  assert.deepEqual(f.state.invitations, [{ robot: "b", groupId: GROUP, appId: "101" }]);
  assert.ok(f.state.memberQueries.length >= 2);
  assert.equal(f.state.binding.group.state, "active");
  assert.match(f.state.sent[0].content, /^\*\*\[任务模式\]/);
  assert.match(f.state.sent[0].content, /绑定已启用/);
  assert.match(f.state.sent[0].content, /完整对话链接\]\(https:\/\/team.example.invalid\/team\/issues\/TEST-1\)\*\*$/);
});

test("single assignee requires only its own robot, not the invoking robot", async t => {
  const f = fixture(t); f.state.binding.group.agent_ids = [A]; f.state.members = ["101"];
  await f.input(f.make("b"));
  assert.equal(f.state.binding.group.state, "active");
  assert.equal(f.state.invitations.length, 0);
  f.addDelivery(A); await f.make("a").issueRelay.poll();
  assert.equal(f.state.sent.at(-1).robotId, "a");
  assert.doesNotMatch(f.state.sent.at(-1).content, /代发/);
});

test("approval, partial failure and unconfirmed success never activate missing leader", async t => {
  for (const result of ["approval_pending", "invitation_failed", "membership_unconfirmed"]) {
    await t.test(result, async t => {
      const f = fixture(t); f.state.members = ["202"]; f.state.inviteResult = result;
      await f.input(f.make("b"));
      assert.equal(f.state.binding.group.state, "pending");
      assert.doesNotMatch(f.state.sent[0].content, /绑定已启用/);
      assert.match(f.state.sent[0].content, /等待|尚未|失败/);
      f.addDelivery(); await f.make("a").issueRelay.poll();
      assert.equal(f.state.acked.size, 0);
      assert.equal(f.state.sent.length, 1);
    });
  }
});

test("denied bind cannot invite and retains the original permission notice", async t => {
  const f = fixture(t); f.state.members = ["202"]; f.state.canReconcile = false; f.state.canComment = false;
  await f.input(f.make("b"));
  assert.equal(f.state.invitations.length, 0);
  assert.match(f.state.sent[0].content, /没有工作区权限/);
  assert.equal(f.state.requests.filter(r => r.path === "issue-group-status").length, 0);
});

test("partial invitation failure explains target visibility, group switch and caller API permission", async t => {
  const f = fixture(t); f.state.members = ["202"]; f.state.inviteResult = "invitation_failed";
  await f.input(f.make("b"));
  assert.equal(f.state.binding.group.state, "pending");
  assert.match(f.state.sent[0].content, /目标机器人的群可见范围与群聊开关/);
  assert.match(f.state.sent[0].content, /邀请机器人的添加群成员 API 权限/);
  assert.doesNotMatch(f.state.sent[0].content, /绑定已启用|supportAuth/);
});

test("invitation backoff and three-attempt budget survive coordinator restoration", async t => {
  const f = fixture(t); f.state.members = ["202"];
  const scope = { robot_id: "b", agent_id: B, workspace_id: "workspace" };
  const options = { invite: true, bootstrap: true };
  for (let i = 0; i < 5; i++) await f.groups.inspect(scope, conversation, f.state.binding, options);
  assert.equal(f.state.invitations.length, 1);
  f.restoreGroups(); await f.groups.inspect(scope, conversation, f.state.binding, options);
  assert.equal(f.state.invitations.length, 1);
  for (let i = 0; i < 5; i++) { f.state.now += 5000000; await f.groups.inspect(scope, conversation, f.state.binding, options); }
  assert.equal(f.state.invitations.length, 3);
});

test("an outside bootstrap or background robot cannot invite despite a queryable member list", async t => {
  const f = fixture(t); f.state.members = [];
  for (const [robot_id, agent_id, bootstrap] of [["a", A, false], ["b", B, true]]) {
    const observed = await f.groups.inspect({ robot_id, agent_id, workspace_id: "workspace" }, conversation, f.state.binding, { invite: true, bootstrap });
    assert.equal(observed.leader_present, false);
    assert.equal(observed.reason, "inviter_not_in_group");
  }
  assert.deepEqual(f.state.invitations, []);
});

test("missing, duplicate, unavailable leaders and malformed membership fail closed", async t => {
  for (const failure of ["missing", "duplicate", "offline", "membership"]) {
    await t.test(failure, async t => {
      const f = fixture(t), bridge = f.make();
      if (failure === "missing") f.entries.splice(0, 1);
      if (failure === "duplicate") f.entries.push({ ...f.entries[0], robotId: "a2", appId: "999" });
      if (failure === "offline") f.entries[0].client = null;
      if (failure === "membership") f.state.failMembers = true;
      await f.input(bridge);
      assert.equal(f.state.binding.group.state, "pending");
      assert.equal(f.state.invitations.length, 0);
      assert.match(f.state.sent[0].content, /未配置|多个匹配|不可用|无法确认/);
    });
  }
});

test("unrelated C polling cannot clobber leader state or multiply membership queries", async t => {
  const f = fixture(t); const a = f.make("a"), c = f.make("c");
  await a.issueRelay.poll();
  assert.equal(f.state.binding.group.state, "active");
  const count = f.state.memberQueries.length;
  for (let i = 0; i < 4; i++) await c.issueRelay.poll();
  assert.equal(f.state.memberQueries.length, count);
  assert.equal(f.state.binding.group.state, "active");
  assert.deepEqual(f.state.memberQueries, ["a"]);
});

test("member in group sends as member; confirmed absent member uses labelled leader", async t => {
  for (const present of [true, false]) {
    await t.test(String(present), async t => {
      const f = fixture(t); if (present) f.state.members.push("202");
      f.addDelivery(); await f.make().issueRelay.poll();
      assert.equal(f.state.sent.length, 1);
      assert.equal(f.state.sent[0].robotId, present ? "b" : "a");
      assert.equal(f.state.sent[0].content.includes("【成员 B · 队长代发】"), !present);
      assert.deepEqual(f.state.sent[0].options.atUsers || [], []);
      assert.equal(f.state.acked.size, 1);
    });
  }
});

test("in-group offline member and ambiguous member mapping pause, never delegate", async t => {
  for (const failure of ["offline", "ambiguous"]) {
    await t.test(failure, async t => {
      const f = fixture(t); f.state.members.push("202");
      if (failure === "offline") f.entries[1].client = null;
      else f.entries.push({ ...f.entries[1], robotId: "b2", appId: "222" });
      f.addDelivery(); await f.make().issueRelay.poll();
      assert.equal(f.state.sent.length, 0); assert.equal(f.state.acked.size, 0);
    });
  }
});

test("legacy leader leaving pauses subsequent parts and next inbound message", async t => {
  const f = fixture(t); const bridge = f.make(); f.addDelivery(B, "正文".repeat(1000));
  legacyCheckpoint(f, bridge);
  f.state.afterSend = () => { f.state.members = []; };
  await bridge.issueRelay.poll();
  assert.equal(f.state.sent.length, 1); assert.equal(f.state.acked.size, 0);
  assert.equal(f.state.binding.group.state, "pending");
  await f.input(bridge, "继续", "input-2");
  assert.equal(f.state.requests.filter(r => r.path === "issue-relay" && r.body.operation === "message").length, 0);
});

test("removing a squad member after poll prevents sending its stale delivery", async t => {
  const f = fixture(t); f.state.members.push("202"); f.addDelivery();
  const bridge = f.make();
  const request = bridge.issueRelay.request;
  bridge.issueRelay.request = async (path, body) => {
    const result = await request(path, body);
    if (path === "issue-deliveries") f.state.binding.group.agent_ids = [A];
    return result;
  };
  await bridge.issueRelay.poll();
  assert.equal(f.state.sent.length, 0);
  assert.equal(f.state.acked.size, 0);
});

test("legacy removal of a squad member between parts pauses its remaining output", async t => {
  const f = fixture(t); f.state.members.push("202"); f.addDelivery(B, "成员长回复".repeat(500));
  const bridge = f.make(); legacyCheckpoint(f, bridge);
  f.state.afterSend = () => { f.state.binding.group.agent_ids = [A]; };
  await bridge.issueRelay.poll();
  assert.equal(f.state.sent.length, 1);
  assert.equal(f.state.acked.size, 0);
});

test("fallback label with 120 Unicode characters fits the single UTF-8 summary and precise footer", async t => {
  const f = fixture(t); f.addDelivery(B, "性能提升🚀".repeat(2000));
  f.state.deliveries[0].author_name = "🚀".repeat(160);
  f.state.deliveries[0].comment_id = "33333333-3333-4333-8333-333333333333";
  await f.make().issueRelay.poll();
  assert.equal(f.state.sent.length, 1);
  for (const sent of f.state.sent) {
    assert.match(sent.content, /^\*\*\[任务模式\] TEST-1\*\*\n\n【/);
    assert.match(sent.content, /队长代发】/);
    assert.match(sent.content, /完整对话链接\]\(https:\/\/team.example.invalid\/team\/issues\/TEST-1#comment-33333333-3333-4333-8333-333333333333\)\*\*$/);
    assert.ok(sent.content.includes(`【${"🚀".repeat(120)} · 队长代发】`));
    assert.match(sent.content, /回复较长/);
    assert.ok(sent.content.length <= 8192);
    assert.equal(sent.content.includes("\ufffd"), false);
  }
});

test("legacy shared checkpoint pins fallback sender through restart and later member arrival", async t => {
  const f = fixture(t); f.addDelivery(B, "长正文".repeat(500)); f.state.failPart = 2;
  const first = f.make(); const parts = legacyCheckpoint(f, first);
  await first.issueRelay.poll(); first.stop();
  assert.equal(f.state.sent.length, 1);
  assert.equal(f.groups.deliveries["delivery-1"].route.robot_id, "a");
  f.state.members.push("202"); f.state.failPart = 0; f.restoreGroups();
  await f.make().issueRelay.poll();
  assert.equal(f.state.sent.length, parts.length); assert.ok(f.state.sent.every(message => message.robotId === "a"));
  assert.equal(f.groups.deliveries["delivery-1"].format, undefined);
  const ack = f.state.requests.find(r => r.path.endsWith("/ack")).body;
  assert.deepEqual(ack.message_ids, parts.map((_, index) => String(1859897297556590001n + BigInt(index))));
  assert.equal(f.state.acked.size, 1);
});

test("fresh failed summary pins the absent member's leader route across restart and member arrival", async t => {
  const f = fixture(t); f.addDelivery(B, "中文🙂".repeat(800)); f.state.failPart = 1;
  const first = f.make(); await first.issueRelay.poll(); first.stop();
  assert.equal(f.state.sent.length, 0);
  assert.equal(f.state.acked.size, 0);
  const checkpoint = f.groups.deliveries["delivery-1"];
  assert.equal(checkpoint.format, "combined-v2"); assert.equal(checkpoint.next, 0);
  assert.equal(checkpoint.route.robot_id, "a");
  f.state.members.push("202"); f.state.failPart = 0; f.restoreGroups();
  await f.make().issueRelay.poll();
  assert.equal(f.state.sent.length, 1);
  assert.equal(f.state.sent[0].robotId, "a");
  assert.match(f.state.sent[0].content, /队长代发/);
  assert.ok(Buffer.byteLength(f.state.sent[0].content) <= 8192);
  assert.deepEqual(f.state.sent[0].options.atUsers || [], []);
  assert.equal(f.state.acked.size, 1);
});

test("legacy partial leader handoff retains checkpoint and pauses instead of replaying via new leader", async t => {
  const f = fixture(t); f.addDelivery(B, "长正文".repeat(500)); f.state.failPart = 2;
  const first = f.make(); legacyCheckpoint(f, first); await first.issueRelay.poll();
  f.state.binding.group.leader_agent_id = B; f.state.binding.group.leader_robot_id = "b"; f.state.members.push("202");
  f.state.failPart = 0;
  await f.make("b").issueRelay.poll();
  assert.equal(f.state.sent.length, 1); assert.equal(f.state.acked.size, 0);
  assert.equal(f.groups.deliveries["delivery-1"].next, 1);
  assert.equal(f.groups.deliveries["delivery-1"].route.robot_id, "a");
});

test("all parts sent before handoff permits new leader to ack without resending", async t => {
  const f = fixture(t); f.addDelivery(); f.state.failAck = true;
  await f.make().issueRelay.poll(); assert.equal(f.state.sent.length, 1);
  f.state.binding.group.leader_agent_id = B; f.state.binding.group.leader_robot_id = "b"; f.state.members = ["202"];
  f.state.failAck = false; await f.make("b").issueRelay.poll();
  assert.equal(f.state.sent.length, 1); assert.equal(f.state.acked.size, 1);
});

test("shared mutex plus completed tombstone prevents concurrent snapshot replay", async t => {
  const f = fixture(t); f.addDelivery();
  const a1 = f.make(), a2 = f.make();
  await Promise.all([a1.issueRelay.poll(), a2.issueRelay.poll()]);
  assert.equal(f.state.sent.length, 1); assert.equal(f.groups.deliveries["delivery-1"].acked, true);
});

test("legacy per-robot group checkpoint is not ignored and replayed", async t => {
  const f = fixture(t); f.addDelivery(); const bridge = f.make();
  bridge.issueRelay.sentParts["delivery-1"] = { next: 1, digest: "legacy", messageIds: [GROUP] };
  bridge.issueRelay.save();
  await bridge.issueRelay.poll();
  assert.equal(f.state.sent.length, 0); assert.equal(f.state.acked.size, 0);
});

test("private deliveries bypass group sender routing and retain ordinary format", async t => {
  const f = fixture(t); f.addDelivery();
  f.state.deliveries[0].conversation = { kind: "private", id: "alice" };
  await f.make("b").issueRelay.poll();
  assert.equal(f.state.sent.length, 1); assert.equal(f.state.sent[0].robotId, "b");
  assert.doesNotMatch(f.state.sent[0].content, /代发/);
  assert.match(f.state.sent[0].content, /^\*\*\[任务模式\]/);
});

test("robot-origin metadata and cached IM sender IDs are ignored without content heuristics", async t => {
  const f = fixture(t);
  assert.equal(botOrigin({ message: { header: { role: "robot" } } }), true);
  assert.equal(botOrigin({ fromagentid: "101" }), true);
  assert.equal(botOrigin({ FromUserType: "BOT" }), true);
  assert.equal(botOrigin({ message: { header: { role: "user" }, body: [{ content: "[任务模式]" }] } }), false);
  await f.make().issueRelay.poll();
  assert.equal(f.groups.isBot({}, "9101"), true);
  assert.equal(f.groups.isBot({}, "alice"), false);
});

test("shared state contains only routing/checkpoints, no credentials or raw issue text", async t => {
  const f = fixture(t); f.addDelivery(B, "private fixture content marker");
  await f.make().issueRelay.poll();
  const saved = readFileSync(f.groups.deliveryPath, "utf8");
  assert.doesNotMatch(saved, /private fixture content marker|Bearer|appSecret|appKey/);
  assert.match(saved, /messageIds/);
});
