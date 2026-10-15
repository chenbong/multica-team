import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, rmSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { IssueRelay } from "./src/issue-relay.js";

const agent = "11111111-1111-4111-8111-111111111111";
const scope = { robot_id: "robot", agent_id: agent, workspace_id: "workspace" };

function fixture(t, kind = "private", noticeKind = "bound") {
  const directory = mkdtempSync(join(tmpdir(), "infoflow-notices-"));
  const config = { robotId: scope.robot_id, statePath: join(directory, "state"),
    issueRelay: { enabled: true }, pollIntervalMs: 3600000,
    multica: { defaultAgent: agent, workspaceId: scope.workspace_id, workspaceSlug: "team",
      webUrl: "https://web.example.invalid", issueWebUrl: "http://127.0.0.1:8004" } };
  const state = { sent: [], requests: [], warnings: [], failAck: false, failSend: false, allowed: true,
    bots: [{ appId: "101", imId: "999" }], rosterCalls: 0 };
  const notice = { id: "notice-1", kind: noticeKind, issue_id: "issue-1", identifier: "TEST-1",
    workspace_slug: "team", conversation: { kind, id: kind === "private" ? "alice" : "42" },
    robot_id: scope.robot_id, agent_id: agent };
  const groups = {
    entries: async (...args) => {
      assert.deepEqual(args, [scope.workspace_id, [agent], { includeRobotId: scope.robot_id }]);
      return [{ robotId: scope.robot_id, agentId: agent, appId: "101", client: {} }];
    },
    roster: async (_client, id) => { assert.equal(id, "42"); state.rosterCalls++; return { bots: state.bots, members: [] }; },
    invite: () => assert.fail("lifecycle notice must never invite"),
    inspect: () => assert.fail("lifecycle notice must not reconcile deleted bindings"),
    selectSender: () => assert.fail("lifecycle notice must never select a member robot"),
  };
  const relays = [];
  const make = () => {
    const relay = new IssueRelay(config, {}, async (conversation, content, options) => {
      if (state.failSend) throw new Error("fake-sdk-secret");
      state.sent.push({ conversation, content, options });
      return kind === "private" ? { msgkey: "1836339350307196087" } : { messageid: "1836339350307196087" };
    }, { warn: message => state.warnings.push(message) }, { groups });
    relay.request = async (path, body) => {
      state.requests.push({ path, body: structuredClone(body) });
      if (path === "conversation-notices") return { notices: [structuredClone(notice)] };
      if (path.endsWith("/check")) {
        assert.deepEqual(body, scope);
        assert.equal(state.rosterCalls > 0, kind === "group");
        return { allowed: state.allowed };
      }
      if (path.endsWith("/ack")) {
        assert.equal(JSON.parse(readFileSync(relay.noticesPath, "utf8"))[notice.id].next, 1);
        if (state.failAck) throw new Error("fake-service-secret");
        return { handled: true, silent: true };
      }
      throw new Error("unexpected fake endpoint");
    };
    relays.push(relay);
    return relay;
  };
  t.after(() => { relays.forEach(relay => relay.stop()); rmSync(directory, { recursive: true, force: true }); });
  return { config, state, notice, make, groups };
}

for (const kind of ["private", "group"]) for (const noticeKind of ["bound", "unbound"]) {
  test(`${kind} ${noticeKind} has one bold lifecycle header and issue-only IP hyperlink`, async t => {
    const f = fixture(t, kind, noticeKind), relay = f.make();
    await relay.pollConversationNotices();
    await relay.pollConversationNotices();
    assert.equal(f.state.sent.length, 1);
    assert.deepEqual(f.state.sent[0], { conversation: f.notice.conversation,
      content: `**[任务模式] TEST-1 已${noticeKind === "bound" ? "绑定" : "解绑"}本对话**\n\n[任务链接](http://127.0.0.1:8004/team/issues/TEST-1)`, options: {} });
    assert.deepEqual(f.state.requests.find(item => item.path.endsWith("/ack")).body,
      { ...scope, message_id: "1836339350307196087", message_ids: ["1836339350307196087"] });
    assert.equal(f.state.rosterCalls, kind === "group" ? 1 : 0);
    assert.equal(relay.sentNotices[f.notice.id].acked, true);
  });
  test(`${kind} ${noticeKind} ACK failure and restart retry only ACK, retaining tombstones`, async t => {
    const f = fixture(t, kind, noticeKind), first = f.make();
    f.state.failAck = true;
    await first.pollConversationNotices(); first.stop();
    const restored = f.make();
    f.state.failAck = false;
    f.state.allowed = false;
    // Already sent unbind notices do not require the deleted group binding,
    // or even a fresh roster, to retry their durable acknowledgement.
    f.groups.roster = async () => assert.fail("already sent notice must only ACK");
    await restored.pollConversationNotices(); await restored.pollConversationNotices();
    assert.equal(f.state.sent.length, 1);
    assert.equal(restored.sentNotices[f.notice.id].acked, true);
    assert.equal(f.state.requests.filter(item => item.path.endsWith("/check")).length, 1);
    assert.doesNotMatch(f.state.warnings.join(" "), /secret/);
  });
  test(`${kind} ${noticeKind} failed send persists pending state and restarts with one send`, async t => {
    const f = fixture(t, kind, noticeKind), first = f.make();
    f.state.failSend = true;
    await first.pollConversationNotices(); first.stop();
    assert.equal(first.sentNotices[f.notice.id].next, 0);
    assert.equal(f.state.requests.some(item => item.path.endsWith("/ack")), false);
    f.state.failSend = false;
    await f.make().pollConversationNotices();
    assert.equal(f.state.sent.length, 1);
    assert.equal(f.state.rosterCalls, kind === "group" ? 2 : 0);
  });
}

test("group notices need fresh own-app membership, never IM IDs or another configured robot", async t => {
  const f = fixture(t, "group"), relay = f.make();
  f.state.bots = [{ appId: "202", imId: "101" }];
  await relay.pollConversationNotices();
  assert.equal(f.state.sent.length, 0);
  assert.equal(f.state.requests.some(item => item.path.endsWith("/ack")), false);
  f.state.bots = [{ appId: "101", imId: "999" }];
  await relay.pollConversationNotices();
  assert.equal(f.state.sent.length, 1);
});

test("a changed notice payload is neither resent nor ACKed after an initial send", async t => {
  const f = fixture(t), relay = f.make(); f.state.failAck = true;
  await relay.pollConversationNotices();
  f.notice.conversation.id = "bob";
  f.state.failAck = false;
  await relay.pollConversationNotices();
  assert.equal(f.state.sent.length, 1);
  assert.equal(f.state.requests.filter(item => item.path.endsWith("/ack")).length, 1);
});

for (const field of ["robot_id", "agent_id", "identifier", "conversation"]) {
  test(`malformed/cross-scope notice ${field} cannot select a destination`, async t => {
    const f = fixture(t), relay = f.make();
    f.notice[field] = field === "conversation" ? { kind: "private", id: "alice@evil" } : "invalid";
    await relay.pollConversationNotices();
    assert.equal(f.state.sent.length, 0);
    assert.equal(f.state.requests.some(item => item.path.endsWith("/ack")), false);
  });
}

test("notice polls coalesce and a stop during membership lookup prevents sending", async t => {
  const f = fixture(t, "group"), relay = f.make();
  let release, entered;
  const started = new Promise(resolve => { entered = resolve; });
  f.groups.roster = async () => { entered(); await new Promise(resolve => { release = resolve; }); return { bots: f.state.bots, members: [] }; };
  const pending = relay.pollConversationNotices();
  await started;
  await relay.pollConversationNotices();
  assert.equal(f.state.requests.length, 1);
  relay.stop(); release(); await pending;
  assert.equal(f.state.sent.length, 0);
  assert.equal(relay.noticeTimer, null);
});

test("notice transport unavailable does not block independent issue polling; old API 404 is silent", async t => {
  const f = fixture(t), relay = f.make();
  let release;
  relay.groups = null;
  relay.request = async path => {
    if (path === "conversation-notices") return new Promise(resolve => { release = () => resolve({ notices: [] }); });
    assert.equal(path, "issue-deliveries"); return { deliveries: [] };
  };
  const pending = relay.pollConversationNotices();
  await Promise.resolve();
  await relay.poll();
  assert.equal(relay.polling, false);
  assert.equal(relay.noticePolling, true);
  release(); await pending;
  relay.request = async () => { throw Object.assign(new Error("fake-secret"), { status: 404 }); };
  await relay.pollConversationNotices();
  assert.deepEqual(f.state.warnings, []);
});

test("malformed ACK retains successful send and is retried without duplication", async t => {
  const f = fixture(t), relay = f.make(), request = relay.request;
  relay.request = async (path, body) => path.endsWith("/ack") ? {} : request(path, body);
  await relay.pollConversationNotices();
  assert.equal(relay.sentNotices[f.notice.id].acked, undefined);
  relay.request = request;
  await relay.pollConversationNotices();
  assert.equal(f.state.sent.length, 1);
  assert.equal(relay.sentNotices[f.notice.id].acked, true);
});

test("send checkpoint persistence failure cannot ACK or duplicate the in-memory successful send", async t => {
  const f = fixture(t), relay = f.make(), save = relay.saveNotices.bind(relay);
  relay.saveNotices = () => {
    if (relay.sentNotices[f.notice.id]?.next === 1) throw new Error("fake disk failure");
    save();
  };
  await relay.pollConversationNotices();
  assert.equal(f.state.sent.length, 1);
  assert.equal(f.state.requests.some(item => item.path.endsWith("/ack")), false);
  relay.saveNotices = save;
  await relay.pollConversationNotices();
  assert.equal(f.state.sent.length, 1);
  assert.equal(relay.sentNotices[f.notice.id].acked, true);
});

for (const kind of ["private", "group"]) for (const noticeKind of ["bound", "unbound"]) {
  test(`${kind} ${noticeKind} revoked after poll skips SDK send and ACK`, async t => {
    const f = fixture(t, kind, noticeKind), relay = f.make(), request = relay.request;
    relay.request = async (path, body) => {
      const response = await request(path, body);
      if (path === "conversation-notices") f.state.allowed = false;
      return response;
    };
    await relay.pollConversationNotices();
    assert.equal(f.state.sent.length, 0);
    assert.equal(f.state.requests.filter(item => item.path.endsWith("/check")).length, 1);
    assert.equal(f.state.requests.some(item => item.path.endsWith("/ack")), false);
    assert.equal(relay.sentNotices[f.notice.id], undefined);
  });
}

test("group authority check occurs after the fresh roster, including a revoked next-zero retry", async t => {
  const f = fixture(t, "group"), relay = f.make(), roster = f.groups.roster;
  f.state.failSend = true;
  await relay.pollConversationNotices();
  assert.equal(relay.sentNotices[f.notice.id].next, 0);
  f.state.failSend = false;
  f.groups.roster = async (...args) => {
    const response = await roster(...args);
    f.state.allowed = false;
    return response;
  };
  await relay.pollConversationNotices();
  assert.equal(f.state.sent.length, 0);
  assert.equal(relay.sentNotices[f.notice.id].next, 0);
  assert.equal(f.state.requests.some(item => item.path.endsWith("/ack")), false);
});

for (const response of [{}, { allowed: "true" }, { allowed: null }]) {
  test("malformed authority check fails closed without SDK sending or ACK", async t => {
    const f = fixture(t), relay = f.make(), request = relay.request;
    relay.request = (path, body) => path.endsWith("/check") ? response : request(path, body);
    await relay.pollConversationNotices();
    assert.equal(f.state.sent.length, 0);
    assert.equal(f.state.requests.some(item => item.path.endsWith("/ack")), false);
  });
}

test("stop during API authority check prevents a late authorized send", async t => {
  const f = fixture(t), relay = f.make(), request = relay.request;
  let entered, release;
  const started = new Promise(resolve => { entered = resolve; });
  relay.request = (path, body) => {
    if (path.endsWith("/check")) { entered(); return new Promise(resolve => { release = resolve; }); }
    return request(path, body);
  };
  const pending = relay.pollConversationNotices(); await started;
  relay.stop(); release({ allowed: true }); await pending;
  assert.equal(f.state.sent.length, 0);
  assert.equal(f.state.requests.some(item => item.path.endsWith("/ack")), false);
});
