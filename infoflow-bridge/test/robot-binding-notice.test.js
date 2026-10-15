import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import vm from "node:vm";
import { saveRobotBinding } from "../src/robot-binding-notice.js";
import { inlineMarkdown } from "../src/inline-markdown.js";

const robot = { id: "robot", purpose: "task", enabled: true, appId: "101", profile: "alice", workspaceId: "ws", agent: "智能体 A", name: "机器人 A", allowUsers: ["someone-else"], appSecret: "fixture-only-secret" };

function fixture(previous = {}) {
  const calls = [], warnings = [];
  const runtime = { config: { robots: [] },
    async sync(config) { calls.push("sync"); this.config = config; },
    async sendDirect(id, recipient, content) { calls.push({ id, recipient, content }); },
  };
  const options = { runtime, previous, robot, robots: [robot], email: "alice@example.test",
    persist: rows => { assert.deepEqual(rows, [robot]); calls.push("persist"); },
    readConfig: () => { calls.push("reload"); return { robots: [structuredClone(robot)] }; },
    logger: { warn: value => warnings.push(value) },
  };
  return { runtime, calls, warnings, options };
}

test("new robot binding sends one confirmation to the authenticated owner after persistence and synchronization", async () => {
  const f = fixture();
  assert.deepEqual(await saveRobotBinding(f.options), { status: "sent" });
  assert.deepEqual(f.calls.slice(0, 3), ["persist", "reload", "sync"]);
  assert.equal(f.calls.length, 4);
  assert.deepEqual(f.calls[3], { id: "robot", recipient: "alice", content: "**[聊天模式]**\n\n智能体 **智能体 A** 绑定如流机器人 **机器人 A** 成功" });
  assert.doesNotMatch(f.calls[3].content, /fixture-only-secret|完整对话链接/);
});

test("unchanged saves and credential-only edits do not repeat the binding confirmation", async () => {
  const f = fixture({ ...robot, appSecret: "older-secret", appKey: "old-key" });
  assert.deepEqual(await saveRobotBinding(f.options), { status: "skipped" });
  assert.deepEqual(f.calls, ["persist", "reload", "sync"]);
});

for (const key of ["appId", "profile", "workspaceId", "agent"]) test(`changing ${key} sends a new binding confirmation`, async () => {
  const f = fixture({ ...robot, [key]: "old" });
  assert.deepEqual(await saveRobotBinding(f.options), { status: "sent" });
  assert.equal(f.calls.filter(value => typeof value === "object").length, 1);
});

test("re-enabling a saved binding confirms it, while disabled robots send nothing", async () => {
  const f = fixture({ ...robot, enabled: false });
  assert.deepEqual(await saveRobotBinding(f.options), { status: "sent" });
  const disabled = fixture();
  disabled.options.robot = { ...robot, enabled: false };
  disabled.options.robots = [disabled.options.robot];
  disabled.options.persist = () => {};
  assert.deepEqual(await saveRobotBinding(disabled.options), { status: "skipped" });
  assert.equal(disabled.calls.filter(value => typeof value === "object").length, 0);
});

test("failed transport never rolls back a saved binding or discloses a sensitive SDK error", async () => {
  const f = fixture();
  f.runtime.sendDirect = async () => { throw Error("secret-sdk-url-with-credentials"); };
  assert.deepEqual(await saveRobotBinding(f.options), { status: "failed" });
  assert.deepEqual(f.runtime.config.robots, [robot]);
  assert.equal(f.warnings.length, 1);
  assert.doesNotMatch(f.warnings[0], /secret-sdk-url|credentials|fixture-only-secret/);
});

for (const step of ["persist", "sync"]) test(`${step} failure never sends a premature success`, async () => {
  const f = fixture();
  if (step === "persist") f.options.persist = () => { throw Error("save failed"); };
  else f.runtime.sync = async () => { throw Error("sync failed"); };
  await assert.rejects(saveRobotBinding(f.options), /failed/);
  assert.equal(f.calls.filter(value => typeof value === "object").length, 0);
});

test("a changed configuration cannot send a stale success from a different binding", async () => {
  const f = fixture();
  f.options.readConfig = () => ({ robots: [{ ...robot, agent: "different" }] });
  assert.deepEqual(await saveRobotBinding(f.options), { status: "failed" });
  assert.equal(f.calls.filter(value => typeof value === "object").length, 0);
});

test("inline presentation escapes Markdown and HTML and clips whole Unicode units", () => {
  assert.equal(inlineMarkdown(" A\n[link](url) <tag> **x** "), "A \\[link\\]\\(url\\) &lt;tag&gt; \\*\\*x\\*\\*");
  const short = inlineMarkdown("🙂[*]".repeat(100), 50);
  assert.ok(Buffer.byteLength(short) <= 50);
  assert.ok(short.endsWith("..."));
  assert.doesNotMatch(short, /\uFFFD/);
});

test("admin route verifies ownership and credentials before the save/notice workflow", () => {
  const src = readFileSync(new URL("../src/admin.js", import.meta.url), "utf8");
  const route = src.slice(src.indexOf('url.pathname === "/api/robot"'), src.indexOf('url.pathname === "/api/robot/delete"'));
  assert.ok(route.indexOf("resolveOwnedAgent(") < route.indexOf("fetchRobotName("));
  assert.ok(route.indexOf("fetchRobotName(") < route.indexOf("await saveRobotBinding("));
  assert.match(route, /email: session\.email/);
  assert.match(route, /id: input\.id \|\| previous\.id \|\| randomUUID\(\)/);
  assert.match(route, /bindingNotice \}/);
});

test("admin save clearly distinguishes a saved binding from a failed confirmation", () => {
  const html = readFileSync(new URL("../public/index.html", import.meta.url), "utf8");
  const fn = html.match(/function bindingSaveMessage\(notice\) \{[\s\S]*?\n\}/)?.[0];
  assert.ok(fn);
  const context = vm.createContext({});
  vm.runInContext(fn, context);
  assert.match(context.bindingSaveMessage({ status: "failed" }), /绑定已保存.*发送失败/);
  assert.match(context.bindingSaveMessage({ status: "sent" }), /绑定成功.*私聊/);
  assert.match(context.bindingSaveMessage(), /已保存/);
});
