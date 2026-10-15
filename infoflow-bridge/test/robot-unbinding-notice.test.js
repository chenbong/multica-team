import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import vm from "node:vm";
import { deleteRobotBinding } from "../src/robot-binding-notice.js";

const robot = { id: "robot", name: "PDC-CPU", agent: "PDC-CPU", enabled: true, appId: "fixture", allowUsers: ["not-the-owner"] };
function fixture() {
  const other = { id: "other" }, calls = [], warnings = [];
  let saved = [robot, other];
  const runtime = { config: { robots: saved }, async sync(config) { calls.push("sync"); this.config = config; } };
  const options = { runtime, robot, email: "alice@example.test",
    persist(rows) { calls.push("persist"); saved = rows; },
    readConfig() { calls.push("reload"); return { robots: saved }; },
    async sendDirect(snapshot, user, content) {
      assert.deepEqual(runtime.config.robots, [other]);
      calls.push({ snapshot, user, content });
    }, logger: { warn: value => warnings.push(value) } };
  return { options, runtime, calls, warnings, other };
}
test("unbind persists and disconnects before sending exactly once with the old identity to the signed-in user", async () => {
  const f = fixture();
  assert.deepEqual(await deleteRobotBinding(f.options), { status: "sent" });
  assert.deepEqual(f.calls.slice(0, 3), ["persist", "reload", "sync"]);
  assert.equal(f.calls.length, 4);
  assert.notEqual(f.calls[3].snapshot, robot);
  assert.deepEqual(f.calls[3].snapshot, robot);
  assert.equal(f.calls[3].user, "alice");
  assert.equal(f.calls[3].content, "**[聊天模式]**\n\n智能体 **PDC-CPU** 解绑如流机器人 **PDC-CPU** 成功");
  assert.deepEqual(await deleteRobotBinding({ ...f.options, robot: undefined }), { status: "skipped" });
  assert.equal(f.calls.length, 4);
});
test("notification failure does not undo removal or expose transport errors", async () => {
  const f = fixture();
  f.options.sendDirect = async () => { throw Error("secret-sdk-error"); };
  assert.deepEqual(await deleteRobotBinding(f.options), { status: "failed" });
  assert.deepEqual(f.runtime.config.robots, [f.other]);
  assert.equal(f.warnings.length, 1);
  assert.doesNotMatch(f.warnings[0], /secret-sdk-error/);
});
for (const step of ["persist", "sync"]) test(`${step} failure never sends success`, async () => {
  const f = fixture();
  if (step === "persist") f.options.persist = () => { throw Error("disk failure"); };
  else f.runtime.sync = async () => { throw Error("sync failure"); };
  await assert.rejects(deleteRobotBinding(f.options), /failure/);
  assert.equal(f.calls.filter(x => typeof x === "object").length, 0);
});
test("still-present bindings and invalid recipients never receive success", async () => {
  for (const stale of [true, false]) {
    const f = fixture();
    if (stale) f.options.readConfig = () => ({ robots: [robot] });
    else f.options.email = "invalid user@example.test";
    assert.deepEqual(await deleteRobotBinding(f.options), { status: "failed" });
    assert.equal(f.calls.filter(x => typeof x === "object").length, 0);
  }
});
test("disabled bindings use the saved identity without recreating a runtime connection", async () => {
  const f = fixture();
  f.options.robot = { ...robot, enabled: false, agent: "A[*]", name: "<bot>" };
  f.runtime.config.robots = [f.options.robot, f.other];
  assert.deepEqual(await deleteRobotBinding(f.options), { status: "sent" });
  assert.match(f.calls[3].content, /A\\\[\\\*\\\]/);
  assert.match(f.calls[3].content, /&lt;bot&gt;/);
});
test("delete route preserves ownership and verification-robot guards before removal", () => {
  const src = readFileSync(new URL("../src/admin.js", import.meta.url), "utf8");
  const route = src.slice(src.indexOf('url.pathname === "/api/robot/delete"'), src.indexOf('url.pathname === "/api/robot/link"'));
  assert.ok(route.indexOf('purpose === "verification"') < route.indexOf("await deleteRobotBinding("));
  assert.ok(route.indexOf("await ownsRobot(") < route.indexOf("await deleteRobotBinding("));
  assert.match(route, /email: session\.email/);
  assert.match(route, /unbindingNotice \}/);
});
test("concurrent duplicate delete sends once and stale ownership checks cannot delete a replacement", async () => {
  const f = fixture();
  const results = await Promise.all([deleteRobotBinding(f.options), deleteRobotBinding(f.options)]);
  assert.deepEqual(results.map(r => r.status).sort(), ["sent", "skipped"]);
  assert.equal(f.calls.filter(x => typeof x === "object").length, 1);
  const stale = fixture();
  stale.runtime.config.robots = [{ ...robot, agent: "replacement" }, stale.other];
  await assert.rejects(deleteRobotBinding(stale.options), /绑定已变化/);
  assert.deepEqual(stale.calls, []);
});
test("page distinguishes successful removal from failed delivery", () => {
  const html = readFileSync(new URL("../public/index.html", import.meta.url), "utf8");
  const fn = html.match(/function bindingDeleteMessage\(notice\) \{[\s\S]*?\n\}/)?.[0];
  assert.ok(fn);
  const context = vm.createContext({}); vm.runInContext(fn, context);
  assert.match(context.bindingDeleteMessage({ status: "sent" }), /解绑成功.*私聊/);
  assert.match(context.bindingDeleteMessage({ status: "failed" }), /解绑成功.*发送失败/);
  assert.match(context.bindingDeleteMessage({ status: "skipped" }), /不存在/);
  assert.match(html, /toast\(bindingDeleteMessage\(data\.unbindingNotice\)\)/);
});
