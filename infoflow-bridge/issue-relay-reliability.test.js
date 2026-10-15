import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, readdirSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createHash } from "node:crypto";
import { Bridge } from "./src/bridge.js";
import { IssueRelay } from "./src/issue-relay.js";

const AGENT_ID = "11111111-1111-4111-8111-111111111111";
const BINDING = { binding_id: "binding-1", issue_id: "issue-1", identifier: "RETRY-1", workspace_slug: "team", generation: "generation-1" };
const SUCCESS = { handled: true, silent: true };
const FIRST_MESSAGE_ID = "1859897297556589312";

function response(body, status = 200) {
  return { ok: status >= 200 && status < 300, status, json: async () => structuredClone(body) };
}

function incoming(messageId = FIRST_MESSAGE_ID, groupId = "42") {
  return {
    conversation: { kind: "group", id: groupId }, user: "alice",
    text: "请继续处理", messageId, replyToMessageId: "1859897297556589301", images: [],
  };
}

function delivery(id = "delivery-1", groupId = "42", content = "处理结果") {
  return { id, conversation: { kind: "group", id: groupId }, content, binding: { ...BINDING }, issue_id: BINDING.issue_id };
}

function deferred() {
  let resolve;
  const promise = new Promise((done) => { resolve = done; });
  return { promise, resolve };
}

function inboxSize(relay) {
  assert.ok(relay.inbox && typeof relay.inbox === "object", "IssueRelay must expose its durable inbox");
  return relay.inbox instanceof Map ? relay.inbox.size : Object.keys(relay.inbox).length;
}

function fixture(t) {
  const directory = mkdtempSync(join(tmpdir(), "infoflow-relay-reliability-"));
  const config = {
    robotId: "reliability-robot", issueRelay: { enabled: true }, statePath: join(directory, "state.json"),
    allowUsers: ["alice"], allowAllInGroups: true, pollIntervalMs: 3_600_000,
    multica: {
      cli: join(directory, "missing-agent-cli"), profile: "unused-test-profile", defaultAgent: AGENT_ID,
      workspaceId: "22222222-2222-4222-8222-222222222222", workspaceSlug: "team",
      serverUrl: "https://relay.example.invalid", webUrl: "https://web.example.invalid",
    },
  };
  const requests = [], sendAttempts = [], sent = [], ownerCalls = [], unexpectedRequests = [];
  const stoppedOnCleanup = [];
  const acknowledged = new Set();
  const server = {
    deliveries: [],
    input: async () => response(SUCCESS),
    preflight: async () => response({ handled: true, silent: false, can_comment: true, binding: BINDING }),
    ack: async (id) => { acknowledged.add(id); return response(SUCCESS); },
    send: async () => ({ messageid: String(1859897297556590000n + BigInt(sent.length)) }),
  };
  const logger = { log() {}, error() {}, warn() {} };
  const multica = {
    listAgents: async () => { ownerCalls.push("listAgents"); throw new Error("UUID config must not resolve an owner CLI"); },
  };
  const send = async (conversation, content, options) => {
    const call = { conversation: structuredClone(conversation), content, options: structuredClone(options) };
    sendAttempts.push(call);
    const result = await server.send(call);
    sent.push({ ...call, result: structuredClone(result) });
    return result;
  };
  const fetchImpl = async (url, options) => {
    const parsedUrl = new URL(url);
    const prefix = "/api/integrations/infoflow/";
    const body = JSON.parse(options.body);
    const path = parsedUrl.pathname.slice(prefix.length);
    requests.push({ path, body });
    if (parsedUrl.origin !== config.multica.serverUrl || !parsedUrl.pathname.startsWith(prefix)) {
      unexpectedRequests.push(url);
      throw new Error("unexpected request; real network is disabled");
    }
    if (path === "issue-relay") return body.operation === "binding" ? server.preflight(body) : server.input(body);
    if (path === "issue-deliveries") {
      return response({ deliveries: server.deliveries.filter((item) => !acknowledged.has(item.id)) });
    }
    const ack = path.match(/^issue-deliveries\/([^/]+)\/ack$/);
    if (ack) return server.ack(decodeURIComponent(ack[1]), body);
    unexpectedRequests.push(path);
    throw new Error("unexpected endpoint; real network is disabled");
  };
  const make = () => {
    const relay = new IssueRelay(config, multica, send, logger, {
      token: "local-test-service-token-0123456789", fetchImpl,
      stageImages: async () => { throw new Error("reliability fixtures must not download images"); },
    });
    stoppedOnCleanup.push(relay);
    return relay;
  };
  t.after(() => {
    for (const instance of stoppedOnCleanup) instance.stop();
    rmSync(directory, { recursive: true, force: true });
    assert.deepEqual(unexpectedRequests, [], "every request must stay inside the local fake service");
    assert.deepEqual(ownerCalls, [], "relay retries must never use owner credentials or a real CLI");
  });
  return {
    config, directory, logger, server, requests, sendAttempts, sent, ownerCalls, acknowledged, send, make,
    track: (instance) => stoppedOnCleanup.push(instance),
    inputRequests: () => requests.filter((request) => request.path === "issue-relay" && request.body.operation !== "binding"),
    ackRequests: () => requests.filter((request) => request.path.endsWith("/ack")),
  };
}

test("failed inbound is durable and restore plus poll retries the exact original request", async (t) => {
  for (const failure of ["http", "transport"]) {
    await t.test(failure, async (t) => {
      const f = fixture(t);
      f.server.input = async () => {
        if (failure === "transport") throw new Error("connection closed before response");
        return response({}, 503);
      };
      const first = f.make();
      const message = incoming();
      assert.equal(await first.handle(message), true, "a transient failure must not fall back to Chat");
      assert.ok(inboxSize(first) > 0, "the failed input must remain queued");
      const original = f.inputRequests()[0].body;
      assert.equal(original.message_id, FIRST_MESSAGE_ID);
      assert.equal(original.sender, "alice");
      assert.equal(original.reply_to_message_id, message.replyToMessageId);
      first.stop();

      f.server.input = async () => response(SUCCESS);
      const restored = f.make();
      assert.ok(inboxSize(restored) > 0, "a new instance must load the failed input from disk");
      await restored.poll();
      assert.deepEqual(f.inputRequests().map((request) => request.body), [original, original]);
      assert.equal(inboxSize(restored), 0);
      await restored.poll();
      assert.equal(f.inputRequests().length, 2, "a completed input must not be retried again");
    });
  }
});

test("durable inbox and outbound checkpoints survive independently in separate files", async (t) => {
  const f = fixture(t);
  const item = delivery("delivery-1", "43");
  f.server.deliveries = [item];
  f.server.input = async () => response({}, 503);
  f.server.ack = async () => response({}, 503);
  const first = f.make();
  await first.handle(incoming());
  await first.poll();
  assert.ok(inboxSize(first) > 0);
  assert.equal(f.sent.length, 1, "failed input must not prevent unrelated outbound draining");
  const snapshots = readdirSync(f.directory, { withFileTypes: true })
    .filter((entry) => entry.isFile() && !entry.name.endsWith(".tmp"))
    .map(({ name }) => ({ name, data: JSON.parse(readFileSync(join(f.directory, name), "utf8")) }));
  const inboxFiles = snapshots.filter(({ data }) => JSON.stringify(data).includes(FIRST_MESSAGE_ID));
  const outboxFiles = snapshots.filter(({ data }) => JSON.stringify(data).includes(item.id));
  assert.ok(inboxFiles.length > 0, "the input itself must be persisted");
  assert.ok(outboxFiles.length > 0, "the sent delivery checkpoint must be persisted");
  assert.ok(inboxFiles.some((input) => outboxFiles.some((output) => input.name !== output.name)),
    "inbox persistence must not overwrite the outbound checkpoint file");
  first.stop();

  f.server.input = async () => response(SUCCESS);
  f.server.ack = async (id) => { f.acknowledged.add(id); return response(SUCCESS); };
  const restored = f.make();
  assert.ok(inboxSize(restored) > 0);
  await restored.poll();
  assert.equal(inboxSize(restored), 0);
  assert.equal(f.sent.length, 1, "restoring the inbox must not erase sent-part progress");
  assert.ok(f.acknowledged.has(item.id));
});

test("same-conversation inputs cannot overtake a failed input, including after restart", async (t) => {
  const f = fixture(t);
  const firstMessage = incoming("1859897297556589312", "42");
  const secondMessage = incoming("1859897297556589313", "42");
  const independentMessage = incoming("1859897297556589314", "43");
  const accepted = [];
  let firstAvailable = false;
  f.server.input = async (body) => {
    if (body.message_id === firstMessage.messageId && !firstAvailable) return response({}, 503);
    accepted.push(body.message_id);
    return response(SUCCESS);
  };
  const first = f.make();
  assert.equal(await first.handle(firstMessage), true);
  assert.equal(await first.handle(secondMessage), true);
  assert.equal(await first.handle(independentMessage), true);
  assert.deepEqual(accepted, [independentMessage.messageId], "only the independent conversation may advance");
  assert.equal(inboxSize(first), 2);
  first.stop();

  firstAvailable = true;
  const restored = f.make();
  await restored.poll();
  assert.deepEqual(accepted, [independentMessage.messageId, firstMessage.messageId, secondMessage.messageId]);
  assert.equal(inboxSize(restored), 0);
});

test("failed or unauthorized relay inputs never reach Bridge owner-authority fallback", async (t) => {
  for (const failure of ["http-forbidden", "timeout", "denial-send-failure"]) {
    await t.test(failure, async (t) => {
      const f = fixture(t);
      const fallback = [], receipts = [];
      const relay = f.make();
      // Keep Bridge's actual routing while injecting the same isolated relay.
      const bridge = new Bridge({ ...f.config, issueRelay: { enabled: false } }, f.send, f.logger);
      bridge.issueRelay.stop();
      bridge.issueRelay = relay;
      f.track(bridge);
      bridge.chat.send = async (...args) => { fallback.push({ kind: "chat", args }); };
      bridge.multica.listAgents = async () => { fallback.push({ kind: "owner-agents" }); return []; };
      bridge.multica.createIssue = async () => { fallback.push({ kind: "owner-create" }); return {}; };
      f.server.input = async () => {
        if (failure === "timeout") throw new Error("request timed out");
        if (failure === "http-forbidden") return response({}, 403);
        return response({ handled: true, silent: false,
          notice: "没有工作区权限，请联系工作区负责人 workspace-owner。" });
      };
      if (failure === "denial-send-failure") f.server.send = async () => { throw new Error("notice transport unavailable"); };
      for (const [index, text] of ["普通消息", "/issue 不得使用绑定者身份创建"].entries()) {
        await bridge.handleMessage({ ...incoming(String(1859897297556589400n + BigInt(index))), text,
          acknowledge: async () => { receipts.push(text); return true; } });
      }
      await relay.poll();
      assert.ok(f.inputRequests().length > 0, "the test must exercise relay requests");
      assert.deepEqual(fallback, []);
      // Receipt confirms transport admission only; denied/offline requests must
      // still never borrow the owner's identity or create a model invocation.
      assert.deepEqual(receipts, ["普通消息", "/issue 不得使用绑定者身份创建"]);
      assert.equal(f.sent.length, 0);
    });
  }
});

test("one failed group delivery does not block another group or acknowledge the failed one", async (t) => {
  const f = fixture(t);
  const bad = delivery("bad-delivery", "42"), good = delivery("good-delivery", "43");
  f.server.deliveries = [bad, good];
  f.server.send = async ({ conversation }) => {
    if (conversation.id === "42") throw new Error("robot was removed from this group");
    return { messageid: "1859897297556590001" };
  };
  const relay = f.make();
  await relay.poll();
  assert.deepEqual(f.sent.map(({ conversation }) => conversation.id), ["43"]);
  assert.deepEqual([...f.acknowledged], [good.id]);
  await relay.poll();
  assert.equal(f.sent.length, 1, "successful destinations must not be resent while another fails");

  f.server.send = async () => ({ messageid: "1859897297556590002" });
  await relay.poll();
  assert.deepEqual(f.sent.map(({ conversation }) => conversation.id), ["43", "42"]);
  assert.ok(f.acknowledged.has(bad.id));
});

test("legacy stop during the first pending chunk preserves all remaining data for restart", async (t) => {
  const f = fixture(t);
  const content = Array.from({ length: 400 }, (_, index) => `第 ${index} 行，结果🙂\n`).join("");
  const item = delivery("partial-delivery", "42", content);
  f.server.deliveries = [item];
  const started = deferred(), finishFirst = deferred();
  const firstId = "1859897297556590100";
  f.server.send = async () => { started.resolve(); return finishFirst.promise; };
  const first = f.make();
  const parts = first.replyParts(content, item.binding);
  first.sentParts[item.id] = { next: 0, digest: createHash("sha256").update(parts.join("")).digest("hex"), messageIds: [] };
  first.save();
  const polling = first.poll();
  try {
    await Promise.race([started.promise, polling.then(() => assert.fail("poll finished without attempting the first chunk"))]);
    first.stop();
    finishFirst.resolve({ messageid: firstId });
    await polling;
    assert.equal(f.sendAttempts.length, 1, "stop must be checked before every next chunk");
    assert.equal(f.ackRequests().length, 0, "an incomplete delivery must not be acknowledged");
  } finally {
    first.stop();
    finishFirst.resolve({ messageid: firstId });
    await polling;
  }

  const firstContent = f.sent[0].content;
  f.server.send = async () => ({ messageid: String(1859897297556590100n + BigInt(f.sent.length)) });
  const restored = f.make();
  await restored.poll();
  assert.ok(f.sent.length > 1);
  assert.equal(f.sent.filter(({ content }) => content === firstContent).length, 1, "the completed first chunk must not be repeated");
  assert.equal(f.ackRequests().length, 1);
  const ack = f.ackRequests()[0].body;
  const expectedIds = f.sent.map(({ result }) => result.messageid);
  assert.deepEqual(ack.message_ids, expectedIds, "ack must include the persisted first chunk ID and resumed chunk IDs");
  assert.ok(expectedIds.includes(ack.message_id));
});

test("fresh summary ack retries retain its exact external message ID across restart without sending again", async (t) => {
  const f = fixture(t);
  const item = delivery("ack-retry-delivery", "42", "不同片段的回复🙂\n".repeat(700));
  f.server.deliveries = [item];
  f.server.ack = async () => response({}, 503);
  const first = f.make();
  await first.poll();
  assert.equal(f.sent.length, 1);
  const expectedIds = f.sent.map(({ result }) => result.messageid);
  assert.equal(new Set(expectedIds).size, expectedIds.length);
  assert.equal(f.ackRequests().length, 1);
  const firstAck = f.ackRequests()[0].body;
  assert.deepEqual(firstAck.message_ids, expectedIds);
  assert.equal(typeof firstAck.message_id, "string");
  assert.ok(expectedIds.includes(firstAck.message_id), "the scalar ID must be one actually returned by send");
  assert.ok(expectedIds.every((id) => BigInt(id) > BigInt(Number.MAX_SAFE_INTEGER)));
  first.stop();

  let unexpectedSends = 0;
  f.server.send = async () => { unexpectedSends++; throw new Error("already checkpointed delivery was resent"); };
  f.server.ack = async (id) => { f.acknowledged.add(id); return response(SUCCESS); };
  const restored = f.make();
  await restored.poll();
  assert.equal(unexpectedSends, 0);
  assert.equal(f.ackRequests().length, 2);
  assert.deepEqual(f.ackRequests()[1].body, firstAck, "ack IDs and scope must survive process restart exactly");
  assert.ok(f.acknowledged.has(item.id));
  await restored.poll();
  assert.equal(f.ackRequests().length, 2);
});
