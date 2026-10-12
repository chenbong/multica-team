import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Bridge } from "./src/bridge.js";
import { GroupDirectory, atUsers, formatGroupContext, parseGroupBlocks, replyMentions } from "./src/group-context.js";

test("AT blocks: robot mention dropped, person mentions collected", () => {
  const parsed = parseGroupBlocks([
    { type: "AT", robotid: 4100350459, name: "机器人" },
    { type: "TEXT", content: "请" },
    { type: "AT", atuserids: ["alice", "bob"] },
    { type: "AT", userid: "carol", name: "Carol" },
    { type: "TEXT", content: "看一下" },
  ]);
  assert.equal(parsed.text, "请 看一下");
  assert.deepEqual(parsed.mentions, ["alice", "bob", "carol"]);
});

test("reply @userid becomes a mention only for group members", () => {
  const members = ["alice", "Bob_rd", "v_carol"];
  const content = "@alice 请看 @bob_rd 的方案；npm i @types/node，邮件 x@alice.com，@nobody 不在群里。\n@v_carol.";
  assert.deepEqual(replyMentions(content, members), ["alice", "Bob_rd", "v_carol"]);
  assert.deepEqual(replyMentions(content, null), []);
  assert.deepEqual(atUsers("asker", "@alice @asker", members), ["asker", "alice"]);
  assert.deepEqual(atUsers("asker", "@alice", null), ["asker"]);
  const many = Array.from({ length: 80 }, (_, i) => "u" + i);
  assert.equal(atUsers("asker", many.map((id) => "@" + id).join(" "), many).length, 50);
});

test("context header names group, sender, mentions and members", () => {
  const text = formatGroupContext({
    groupId: 1679939,
    info: { name: "研发群", masterUserId: "owner", memberCount: 3 },
    sender: "alice",
    mentions: ["bob"],
    members: ["alice", "bob", "owner"],
  });
  assert.match(text, /群聊：研发群（ID 1679939）/);
  assert.match(text, /发送人：alice/);
  assert.match(text, /消息中@了：bob/);
  assert.match(text, /群主：owner/);
  assert.match(text, /群成员（userid）：alice、bob、owner/);
  const hinted = formatGroupContext({ groupId: 7, info: null, sender: "a", members: ["a"], sendCommand: "node send.js --robot r" });
  assert.match(hinted, /执行：node send\.js --robot r --group 7 --at/);
  assert.doesNotMatch(formatGroupContext({ groupId: 7, info: null, sender: "a", sendCommand: "node send.js" }), /send\.js/);
  const bare = formatGroupContext({ groupId: 7, info: null, sender: "alice" });
  assert.match(bare, /群聊：ID 7/);
  assert.doesNotMatch(bare, /群成员/);
  const big = formatGroupContext({ groupId: 7, info: { memberCount: 300 }, sender: "a", members: Array.from({ length: 300 }, (_, i) => "u" + i) });
  assert.match(big, /群成员共 300 人/);
});

test("directory caches lookups and backs off after a failure", async () => {
  let now = 0, calls = 0, fail = true;
  const directory = new GroupDirectory({
    getGroupInfo: async (groupId) => { calls += 1; assert.equal(groupId, "42"); return { name: "群" }; },
    getMembers: async (groupId) => {
      calls += 1;
      assert.equal(groupId, "42");
      if (fail) throw new Error("权限未开通");
      return [{ userId: "alice", type: "user" }, { userId: "bob", type: "user" }, { agentId: 1, name: "bot", type: "agent" }];
    },
  }, { warn() {} }, () => now);
  assert.equal(await directory.members("42"), null);
  assert.equal(await directory.members("42"), null);
  assert.equal(calls, 1);
  fail = false;
  now += 61_000;
  assert.deepEqual(await directory.members("42"), ["alice", "bob"]);
  assert.deepEqual(await directory.members("42"), ["alice", "bob"]);
  assert.equal((await directory.info("42")).name, "群");
  assert.equal(calls, 3);
});

test("group chat forwards context; member list repeats only when it changes; commands skip it", async (t) => {
  const dir = mkdtempSync(join(tmpdir(), "bridge-group-test-"));
  const messages = [];
  const config = { robotId: "r", statePath: join(dir, "state.json"), allowUsers: [], allowAllInGroups: true,
    multica: { cli: "unused", defaultAgent: "agent", profile: "owner", workspaceId: "ws", webUrl: "http://localhost", workspaceSlug: "ws" },
    pollIntervalMs: 100000, pollTimeoutMs: 1000 };
  const bridge = new Bridge(config, async () => {});
  const sessions = [];
  Object.assign(bridge.multica, {
    listAgents: async () => [{ id: "a", name: "agent" }],
    createChatSession: async () => { const s = { id: "s" + sessions.length, status: "active" }; sessions.push(s); return s; },
    chatSession: async (id) => sessions.find((s) => s.id === id),
    sendChatMessage: async (sessionId, content) => { messages.push(content); return { task_id: "t" + messages.length }; },
  });
  t.after(() => { bridge.stop(); rmSync(dir, { recursive: true, force: true }); });

  let members = ["alice", "bob"];
  let lookups = 0;
  const groupContext = async () => { lookups += 1; return { groupId: 9, info: { name: "研发群" }, members, sender: "alice", mentions: ["bob"] }; };
  const send = (text, id) => bridge.handleMessage({ text, user: "alice", conversation: { kind: "group", id: 9 }, messageId: id, groupContext });

  await send("第一条", "m1");
  await send("第二条", "m2");
  members = ["alice", "bob", "carol"];
  await send("第三条", "m3");
  await send("/help", "m4");

  assert.equal(messages.length, 3);
  assert.match(messages[0], /^\[如流群聊上下文\]\n群聊：研发群（ID 9）\n发送人：alice\n消息中@了：bob\n群成员（userid）：alice、bob\n/);
  assert.match(messages[0], /\n\n第一条$/);
  assert.doesNotMatch(messages[1], /群成员/);
  assert.match(messages[1], /发送人：alice/);
  assert.match(messages[2], /群成员（userid）：alice、bob、carol/);
  assert.equal(lookups, 3);
});

test("private chat and failing context lookups still forward the bare message", async (t) => {
  const dir = mkdtempSync(join(tmpdir(), "bridge-group-test-"));
  const messages = [];
  const config = { robotId: "r", statePath: join(dir, "state.json"), allowUsers: ["alice"], allowAllInGroups: true,
    multica: { cli: "unused", defaultAgent: "agent", profile: "owner", workspaceId: "ws", webUrl: "http://localhost", workspaceSlug: "ws" },
    pollIntervalMs: 100000, pollTimeoutMs: 1000 };
  const bridge = new Bridge(config, async () => {}, { log() {}, warn() {}, error() {} });
  Object.assign(bridge.multica, {
    listAgents: async () => [{ id: "a", name: "agent" }],
    createChatSession: async () => ({ id: "s", status: "active" }),
    chatSession: async () => ({ id: "s", status: "active" }),
    sendChatMessage: async (sessionId, content) => { messages.push(content); return { task_id: "t" + messages.length }; },
  });
  t.after(() => { bridge.stop(); rmSync(dir, { recursive: true, force: true }); });
  await bridge.handleMessage({ text: "私聊", user: "alice", conversation: { kind: "private", id: "alice" }, messageId: "p" });
  await bridge.handleMessage({ text: "群聊", user: "alice", conversation: { kind: "group", id: 9 }, messageId: "g",
    groupContext: async () => { throw new Error("boom"); } });
  assert.deepEqual(messages, ["私聊", "群聊"]);
});
