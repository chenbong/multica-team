import test from "node:test";
import assert from "node:assert/strict";
import { registerHandlers, RobotRuntime } from "./src/robots.js";
import { Bridge } from "./src/bridge.js";
import { privateContent } from "./src/inbound-content.js";
import { mkdtempSync, rmSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";

function fixture() {
  const handlers = new Map(), received = [];
  registerHandlers({
    wsClient: { on: (event, fn) => handlers.set(event, fn) },
    client: {}, robot: { name: "fixture", appId: "12" },
    getPrivateMessage: async (raw,event) => privateContent(raw,event),
    getGroupMessage: async (raw,event) => privateContent(raw,event),
    bridge: { handleMessage: async message => received.push(message) },
    logger: { log() {}, warn() {} },
  });
  return { handlers, received };
}

test("group entry preserves sender, quote ID and attachment before relay admission", async () => {
  const f = fixture();
  await f.handlers.get("group.mixed")({ data: { raw: {
    eventtype: "MESSAGE_RECEIVE", groupid: "42", message: {
      header: { fromuserid: "alice", messageid: "1836339350307196087" },
      body: [{ type: "AT", robotid: "12" }, { type: "AT", atuserids: ["bob"] },
        { type: "replyData", sBasemsgId: "1836339314504617142", content: "引用原文" },
        { type: "TEXT", content: "请继续" }, { type: "IMAGE", content: "fixture" }],
    },
  } } });
  assert.equal(f.received.length, 1);
  assert.equal(f.received[0].user, "alice");
  assert.equal(f.received[0].messageId, "1836339350307196087");
  assert.equal(f.received[0].replyToMessageId, "1836339314504617142");
  assert.deepEqual(f.received[0].conversation, { kind: "group", id: "42" });
  assert.equal(f.received[0].images.length, 1);
  assert.match(f.received[0].context, /alice/);
  assert.match(f.received[0].context, /bob/);
});

test("private entry uses account identifier, not numeric uid, and keeps independent scope", async () => {
  const f = fixture();
  await f.handlers.get("private.text")({ data: { raw: {
    FromUserId: "alice", FromUserName: 30000000, MsgId: "1836339350307196087", Content: "/bind TEST-1",
  } } });
  assert.equal(f.received[0].user, "alice");
  assert.deepEqual(f.received[0].conversation, { kind: "private", id: "alice" });
  assert.equal(f.received[0].text, "/bind TEST-1");
});

test("group entry keeps canonical routing text separate from inline human display and notification IDs", async () => {
  const f = fixture();
  await f.handlers.get("group.mixed")({ data: { raw: {
    eventtype: "MESSAGE_RECEIVE", groupid: "42", message: {
      header: { fromuserid: "alice", messageid: "123" }, body: [
        { type: "AT", robotid: "12", name: "fixture" },
        { type: "TEXT", content: "然后通知" },
        { type: "AT", userid: "bob", name: "小明" },
      ],
    },
  } } });
  const input = f.received[0];
  assert.equal(input.text, "然后通知");
  assert.equal(input.displayText, "然后通知 @小明");
  assert.deepEqual(input.notifyUsers, ["alice", "bob"]);
});

for (const event of ["private.text", "private.markdown", "private.richtext", "private.image"]) {
  test(`${event} forwards the actual private Reply ID without parsing the preview as a selector`, async () => {
    const f = fixture();
    await f.handlers.get(event)({ data: { raw: {
      FromUserId: "alice", MsgId: "1836339350307196087", MsgId2: "300010063", Content: "TASK-2 继续",
      Reply: [{ ReplyMsgId: "1836339314504617142", ReplyContent: "TASK-999 伪造预览" }],
    } } });
    assert.equal(f.received[0].replyToMessageId, "1836339314504617142");
    assert.equal(f.received[0].receipt.baseMsgId, "1836339350307196087");
    assert.equal(f.received[0].receipt.chatType, 7);
    assert.equal(f.received[0].receipt.chatId, "12");
  });
}

test("private rounded quote IDs and preview-only IDs are not repaired", async () => {
  const f = fixture();
  for (const Reply of [[{ ReplyMsgId: 1836339314504617142 }], [{ ReplyContent: '"ReplyMsgId":"1836339314504617142"' }]]) {
    await f.handlers.get("private.text")({ data: { raw: { FromUserId: "alice", MsgId: "123", Content: "TASK-2", Reply } } });
    assert.equal(f.received.at(-1).replyToMessageId, null);
  }
});

test("all-message forwarding is not treated as a request to publish issue comments", async () => {
  const f = fixture();
  await f.handlers.get("group.text")({ data: { raw: { eventtype: "ALL_MESSAGE_FORWARD", groupid: "42", message: {
    header: { fromuserid: "alice", messageid: "123" }, body: [{ type: "AT", robotid: "12" }, { type: "TEXT", content: "旁观消息" }],
  } } } });
  assert.equal(f.received.length, 0);
});

test("already-rounded inbound IDs cannot become idempotency identifiers", async () => {
  const f = fixture();
  await f.handlers.get("private.text")({ data: { raw: { FromUserId: "alice", MsgId: 1836339350307196087, Content: "test" } } });
  assert.equal(f.received[0].messageId, null);
});

test("bot-origin group and private events never enter Bridge or acknowledge", async () => {
  const f = fixture();
  await f.handlers.get("group.text")({ data: { raw: { eventtype: "MESSAGE_RECEIVE", groupid: "42", message: {
    header: { fromuserid: "robot-account", messageid: "123", role: "robot" },
    body: [{ type: "AT", robotid: "12" }, { type: "TEXT", content: "[任务模式] reply" }],
  } } } });
  await f.handlers.get("private.text")({ data: { raw: {
    FromUserId: "robot-account", FromUserType: "robot", MsgId: "124", Content: "reply",
  } } });
  assert.equal(f.received.length, 0);
});

test("runtime maps participant configs across Bridge callbacks without workspace leakage or live API", async t => {
  const dir = mkdtempSync(join(tmpdir(), "infoflow-group-wire-"));
  const a = "11111111-1111-4111-8111-111111111111", b = "22222222-2222-4222-8222-222222222222";
  const statePath = join(dir, "state");
  const config = { statePath, issueRelay: { enabled: true }, robots: [
    { id: "a", agent: a, appId: "101", workspaceId: "ws", enabled: true },
    { id: "b", agent: b, appId: "202", workspaceId: "ws", enabled: false },
    { id: "stale", agent: "33333333-3333-4333-8333-333333333333", appId: "303", workspaceId: "ws", enabled: false },
    { id: "other", agent: a, appId: "404", workspaceId: "another-ws", enabled: true },
  ], multica: { workspaceId: "ws", cli: join(dir, "missing-test-cli") } };
  const logger = { log() {}, warn() {}, error() {} };
  const runtime = new RobotRuntime(config, logger);
  // This fixture checks routing wiring only; account/agent health is covered by
  // agent-binding.test.js with the real resolver and explicit identities.
  runtime.bindingFor = robot => ({ resolve: async () => ({ id: robot.agentId || robot.agent }) });
  const bridge = new Bridge({ robotId: "a", statePath, issueRelay: { enabled: true }, pollIntervalMs: 3600000,
    multica: { cli: join(dir, "missing-test-cli"), workspaceId: "ws", defaultAgent: a, serverUrl: "https://api.example.invalid", webUrl: "https://web.example.invalid" } },
    async () => { throw new Error("unexpected send"); }, logger, { issueGroups: runtime.issueGroups });
  bridge.multica.listAgents = async () => [{ id: a, name: "leader" }, { id: b, name: "member-name" }];
  runtime.instances.set("a", { robot: config.robots[0], bridge, client: { marker: "fake" }, send: async () => {} });
  t.after(() => { bridge.stop(); rmSync(dir, { recursive: true, force: true }); });
  const entries = await runtime.groupRobotEntries("ws", [a, b]);
  assert.deepEqual(entries.map(entry => entry.robotId), ["a", "b"]);
  assert.equal(entries[1].agentId, b);
  assert.equal(entries[1].client, null); // configured-but-offline is not absent
  assert.equal(entries[1].send, null);
  assert.equal(bridge.issueRelay.groups, runtime.issueGroups);
  runtime.issueGroups.members = async () => [{ appId: "101", imId: "9001" }, { appId: "202", imId: "9002" }];
  const binding = { binding_id: "binding-1", group: { leader_agent_id: a, agent_ids: [a, b] } };
  const observation = await runtime.issueGroups.inspect({ robot_id: "a", agent_id: a, workspace_id: "ws" }, { kind: "group", id: "42" }, binding);
  assert.equal(observation.leader_present, true);
  assert.throws(() => runtime.issueGroups.selectSender(observation, { binding, author_agent_id: b }), /in group but unavailable/);
});
