import { installChatInputFixture } from "./test/chat-input-fixture.js";
import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { RobotRuntime } from "./src/robots.js";
import { Bridge } from "./src/bridge.js";

const quiet = { log() {}, warn() {}, error() {} };

test("runtime no longer scans status history or drains retired pending notifications", async t => {
  const dir = mkdtempSync(join(tmpdir(), "retired-status-notifications-"));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const statePath = join(dir, "state.json");
  const retiredPath = statePath + ".review-notifications.json";
  // Retired state remains an audit artifact, not startup configuration.
  const retired = '{"bindings":{"fixture":{"pending":{"old-review":{},"old-blocked":{}}}}}';
  writeFileSync(retiredPath, retired);
  t.mock.method(globalThis, "setInterval", () => { throw new Error("Unexpected background status scanner"); });
  const runtime = new RobotRuntime({ statePath, robots: [], multica: {} }, quiet);
  assert.equal(Object.hasOwn(runtime, "reviewNotifications"), false);
  await runtime.sync(); await runtime.stopAll();
  assert.equal(readFileSync(retiredPath, "utf8"), retired);
});

for (const kind of ["private", "group"]) test(`${kind} task completion replies remain in the originating conversation`, async t => {
  const dir = mkdtempSync(join(tmpdir(), "conversation-completion-"));
  const sent = [], conversation = { kind, id: kind === "group" ? "42" : "alice" };
  const config = { statePath: join(dir, "state.json"), robotId: "robot", reviewNotifications: true,
    allowUsers: ["alice"], allowAllInGroups: true, pollIntervalMs: 1, pollTimeoutMs: 1000,
    multica: { cli: join(dir, "missing-cli"), defaultAgent: "fixture-agent", workspaceId: "ws",
      workspaceSlug: "team", webUrl: "https://team.example.test", serverUrl: "https://api.example.test" } };
  const bridge = new Bridge(config, async (destination, content) => { sent.push({ destination, content }); }, quiet);
  t.after(() => { bridge.stop(); rmSync(dir, { recursive: true, force: true }); });
  Object.assign(bridge.multica, {
    listAgents: async () => [{ id: "fixture-agent-id", name: "fixture-agent" }],
    createIssue: async () => ({ id: "issue", identifier: "TEST-1", status: "in_review" }),
    runs: async () => [{ id: "run", status: "completed" }],
    comments: async () => [{ id: "comment", author_type: "agent", source_task_id: "run", content: "Completion answer" }],
    getIssue: async () => { throw new Error("Retired notification handoff must not suppress replies"); },
    request: async () => { throw new Error("No proactive status scan is allowed"); },
  });
  installChatInputFixture(bridge, { bypassRelay: true });
  await bridge.handleMessage({ conversation, user: "alice", text: "/issue Do a fixture task", messageId: "123" });
  assert.equal(sent.length, 2); // A group answer also has a native AT reminder.
  assert(sent.every(message => message.destination === conversation));
  assert.match(sent[1].content, /Completion answer/);
  assert.match(sent[1].content, /issues\/TEST-1#comment-comment/);
});
