import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { IssueRelay } from "./src/issue-relay.js";

function fixture(t, metadata, options = {}) {
  const dir = mkdtempSync(join(tmpdir(), "infoflow-bind-metadata-"));
  const calls = [], sent = [], warnings = [];
  let reads = 0;
  const relay = new IssueRelay({ robotId: "test-robot", statePath: join(dir, "state"),
    issueRelay: { enabled: true }, pollIntervalMs: 3600000,
    multica: { defaultAgent: "11111111-1111-4111-8111-111111111111", workspaceId: "test-workspace",
      webUrl: "https://team.example.invalid" } }, {},
  async (...args) => sent.push(args), { warn: value => warnings.push(value) }, {
    getPrivateRobotInfo: async () => { reads++; return typeof metadata === "function" ? metadata() : metadata; },
    privateRobotInfoTimeoutMs: 20, ...options,
  });
  relay.request = async (path, body) => {
    calls.push({ path, body: structuredClone(body) });
    return { handled: true, silent: false, can_comment: true, conversation_mode: "issue",
      binding: { binding_id: "binding-1", generation: "generation-1", issue_id: "issue-1",
        identifier: "TEST-1", workspace_slug: "team", private: { state: "active" } } };
  };
  const input = (text = "/bind TEST-1", extra = {}) => ({ conversation: { kind: "private", id: "alice" },
    user: "alice", text, messageId: "123", ...extra });
  t.after(() => { relay.stop(); rmSync(dir, { recursive: true, force: true }); });
  return { relay, calls, sent, warnings, input, reads: () => reads };
}

for (const text of ["/bind TEST-1", "/issue Test private creation"]) {
  test(`private ${text.split(" ")[0]} carries only validated public robot metadata`, async t => {
    const f = fixture(t, { name: "  Public robot  ", puid: "9223372036854775806", openChatDouble: 1,
      rawResponse: { appSecret: "test-secret-must-never-be-forwarded" } });
    await f.relay.handle(f.input(text));
    const body = f.calls.at(-1).body;
    assert.equal(body.robot_name, "Public robot");
    assert.equal(body.robot_chat_id, "9223372036854775806");
    assert.doesNotMatch(JSON.stringify(body), /appSecret|rawResponse|test-secret/);
    assert.equal(f.reads(), 1);
  });
}

test("metadata and binding request remain pinned across an uncertain mutation retry", async t => {
  let name = "First name";
  const f = fixture(t, () => ({ name, puid: "123456", openChatDouble: true }));
  const send = f.relay.request;
  let first = true;
  f.relay.request = async (...args) => {
    const result = await send(...args);
    if (first) { first = false; throw new Error("test transport timeout"); }
    return result;
  };
  await f.relay.handle(f.input());
  name = "Changed name";
  await f.relay.handle(f.input());
  assert.equal(f.reads(), 1);
  assert.deepEqual(f.calls[0].body, f.calls[1].body);
  assert.equal(f.calls[1].body.robot_name, "First name");
});

for (const metadata of [null, { name: "Robot", puid: 9007199254740992, openChatDouble: 1 },
  { name: "Robot", puid: "12", openChatDouble: 0 },
  () => { throw new Error("test-secret-must-not-appear"); },
  () => new Promise(() => {})]) {
  test("unavailable, disabled or malformed public metadata never blocks a private bind or leaks SDK details", async t => {
    const f = fixture(t, metadata);
    await f.relay.handle(f.input());
    assert.equal(f.calls.length, 1);
    assert.equal(f.calls[0].body.operation, "bind");
    assert.equal(f.calls[0].body.robot_chat_id, undefined);
    assert.equal(f.calls[0].body.robot_name, undefined);
    assert.doesNotMatch(JSON.stringify([f.calls, f.sent, f.warnings]), /test-secret|rawResponse/);
  });
}

test("group binds and read-only private lookups never request private robot metadata", async t => {
  const f = fixture(t, () => assert.fail("private metadata read outside private bind/create"));
  await f.relay.handle(f.input("/bind TEST-1", { conversation: { kind: "group", id: "123" } }));
  await f.relay.handle(f.input("/binding TEST-1", { messageId: "124" }));
  assert.equal(f.reads(), 0);
});

test("a stopped relay cannot submit a late metadata-enriched bind", { timeout: 2000 }, async t => {
  let resolveInfo;
  let entered;
  const started = new Promise(resolve => { entered = resolve; });
  const f = fixture(t, () => { entered(); return new Promise(resolve => { resolveInfo = resolve; }); },
    { privateRobotInfoTimeoutMs: 1000 });
  const pending = f.relay.handle(f.input());
  await started;
  f.relay.stop();
  resolveInfo({ name: "Public robot", puid: "123", openChatDouble: 1 });
  await pending;
  assert.equal(f.calls.length, 0);
  assert.equal(f.sent.length, 0);
});
