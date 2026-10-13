import test from "node:test";
import assert from "node:assert/strict";
import { registerHandlers } from "./src/robots.js";

function fixture() {
  const handlers = new Map(), received = [];
  registerHandlers({
    wsClient: { on: (event, fn) => handlers.set(event, fn) },
    client: {}, robot: { name: "fixture", appId: "12" },
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
