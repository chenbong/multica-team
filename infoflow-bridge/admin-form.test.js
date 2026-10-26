import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import vm from "node:vm";

const html = readFileSync(new URL("./public/index.html", import.meta.url), "utf8");
const reset = html.match(/function resetForm\(\) \{[\s\S]*?\n\}/)?.[0];

test("admin reset uses the native prototype when a named control shadows reset", () => {
  assert.ok(reset, "reset handler must be present");
  class Form {
    reset() { this.nativeResetCalls = (this.nativeResetCalls || 0) + 1; }
  }
  const form = Object.assign(new Form(), {
    reset: { tagName: "BUTTON", id: "reset" },
    id: { value: "edited-robot" },
    allowAllInGroups: { checked: false },
  });
  const labels = new Map([['form-title', {textContent: '编辑临时机器人'}], ['secret-hint', {textContent: 'old'}]]);
  vm.runInNewContext(reset + '\nresetForm();', { form, HTMLFormElement: Form,
    renderAgents() {},
    document: { getElementById: id => labels.get(id) } });
  assert.equal(form.nativeResetCalls, 1);
  assert.equal(form.id.value, "");
  assert.equal(form.allowAllInGroups.checked, true);
  assert.equal(labels.get('form-title').textContent, '新增机器人');
  assert.match(labels.get('secret-hint').textContent, /如流开放平台/);
});

test("clear and successful save both use the same reset handler", () => {
  assert.match(html, /id="reset"/);
  assert.match(html, /getElementById\("reset"\)\.addEventListener\("click", resetForm\)/);
  assert.match(html, /warmChatLinks\(\);\s+resetForm\(\);\s+toast\(bindingSaveMessage\(data\.bindingNotice\)\)/);
});

test("robot setup guide includes the requested enterprise group visibility step", () => {
  const guide = html.match(/还没有机器人？三步创建[\s\S]*?<\/ol>/)?.[0];
  assert.ok(guide, "three-step guide must be present");
  assert.equal((guide.match(/<li>/g) || []).length, 3);
  assert.match(guide, /改权限[\s\S]*?可见范围 → 可见群聊 → 群聊范围改成 <code>全部企业群<\/code>/);
  assert.doesNotMatch(html, /默认权限就够用，不用改/);
});

test("admin agent picker has no opt-out and never renders another creator's agents", () => {
  assert.doesNotMatch(html, /mine-only|mineOnly|只看这个账号创建的/);
  const renderAgents = html.match(/function renderAgents\(\) \{[\s\S]*?\n\}/)?.[0];
  assert.ok(renderAgents);
  const options = [];
  const select = {
    value: "", selectedOptions: [], options,
    set innerHTML(_value) { options.length = 0; },
    append(option) { options.push(option); },
  };
  const hint = {};
  const data = {
    agents: [{ id: "own-id", name: "Own", mine: true, workspaceId: "a", workspaceName: "A" },
      { name: "Foreign", mine: false, ownerEmail: "bob@example.test" }],
    me: { email: "alice@example.test" }, workspaces: [],
  };
  vm.runInNewContext(renderAgents + "\nrenderAgents();", {
    data, agentSelect: select,
    form: { id: { value: "" } },
    document: { createElement: () => ({ dataset: {} }), getElementById: () => hint },
  });
  assert.deepEqual(options.map((option) => option.value), ["", "own-id"]);
  assert.doesNotMatch(hint.textContent, /别人|bob/);
  data.agents = [];
  vm.runInNewContext(renderAgents + "\nrenderAgents();", {
    data, agentSelect: select,
    form: { id: { value: "" } },
    document: { createElement: () => ({ dataset: {} }), getElementById: () => hint },
  });
  assert.equal(options[0].textContent, "当前用户暂未创建智能体");
});

test("new bindings hide bound agents; editing retains the current one and reset hides it again", () => {
  const renderAgents = html.match(/function renderAgents\(\) \{[\s\S]*?\n\}/)?.[0];
  const options = [];
  const select = { value: "", selectedOptions: [], options,
    set innerHTML(_value) { options.length = 0; }, append(option) { options.push(option); } };
  const form = { id: { value: "" } };
  const data = { me: { email: "alice@example.test" }, workspaces: [], agents: [
    { id: "bound-a", name: "Bound", mine: true, workspaceId: "a", bindingRobotIds: ["robot-a"] },
    { id: "bound-b", name: "Bound", mine: true, workspaceId: "b", bindingRobotIds: [] },
    { id: "free-a", name: "Free", mine: true, workspaceId: "a", bindingRobotIds: [] },
    { name: "Foreign", mine: false, bindingRobotIds: [] },
  ] };
  const context = vm.createContext({ data, agentSelect: select, form,
    document: { createElement: () => ({ dataset: {} }), getElementById: () => ({}) } });
  vm.runInContext(renderAgents, context);
  const render = () => vm.runInContext("renderAgents()", context);
  render();
  assert.deepEqual(options.slice(1).map(o => [o.value, o.dataset.workspaceId]), [["bound-b", "b"], ["free-a", "a"]]);
  form.id.value = "robot-a"; render();
  assert.equal(options.length, 4);
  form.id.value = "different-robot"; render();
  assert.equal(options.length, 3);
  form.id.value = "";
  data.agents = [data.agents[0]]; render();
  assert.equal(options.length, 1);
  assert.match(options[0].textContent, /均已绑定.*先解绑/);
  data.agents[0].bindingRobotIds = []; render();
  assert.equal(options.length, 2);
});

test("editing an old binding never silently selects a same-name agent in another workspace", () => {
  assert.match(html, /if \(match\) match\.selected = true;\s+else form\.agent\.value = ""/);
  assert.match(html, /!keepWorkspace \? \[\.\.\.agentSelect\.options\]/);
});

function submitFixture({ agentId = "11111111-1111-4111-8111-111111111111", editingId = "", reject = false } = {}) {
  const start = html.indexOf('form.addEventListener("submit", async (event) => {');
  const end = html.indexOf('\nfunction bindingSaveMessage(', start);
  assert(start >= 0 && end > start, "actual form submit handler must be present");
  const calls = [], toasts = [], metrics = { resets: 0, renders: 0, prevented: 0 };
  let submit;
  const select = { value: agentId, selectedOptions: agentId ? [{ dataset: { workspaceId: "22222222-2222-4222-8222-222222222222" } }] : [] };
  const form = { id: { value: editingId }, agent: select, appId: { value: "12345" },
    appKey: { value: "fixture-key" }, appSecret: { value: "fixture-secret" },
    allowAllInGroups: { checked: true }, addEventListener: (event, callback) => { assert.equal(event, "submit"); submit = callback; } };
  const context = vm.createContext({ form, agentSelect: select, activeProfile: "fixture-profile", data: {},
    chatLinks: new Map(), chatLinkLoads: new Map(),
    post: async (url, payload) => {
      calls.push({ url, payload: JSON.parse(JSON.stringify(payload)) });
      if (reject) throw new Error("fixture backend rejected credentials");
      return { activeProfile: "fixture-profile", bindingNotice: { status: "sent" } };
    },
    render: () => { metrics.renders++; }, warmChatLinks() {},
    resetForm: () => { metrics.resets++; }, toast: text => toasts.push(text),
    bindingSaveMessage: () => "fixture save accepted",
  });
  vm.runInContext(html.slice(start, end), context);
  return { calls, toasts, metrics, form, submit: () => submit({ preventDefault: () => { metrics.prevented++; } }) };
}

test("selected fixed agent ID reaches the save endpoint for new and edited robots", async t => {
  for (const editingId of ["", "fixture-existing-robot"]) {
    await t.test(editingId || "new robot", async () => {
      const fixture = submitFixture({ editingId });
      await fixture.submit();
      assert.equal(fixture.calls.length, 1, "a selected ID must not be rejected by the old name-field guard");
      const { url, payload } = fixture.calls[0];
      assert.equal(url, "/api/robot");
      assert.equal(payload.agentId, "11111111-1111-4111-8111-111111111111");
      assert.equal(payload.workspaceId, "22222222-2222-4222-8222-222222222222");
      assert.equal(payload.id, editingId);
      assert.equal(Object.hasOwn(payload, "agent"), false);
      assert.equal(fixture.metrics.resets, 1);
      assert(!fixture.toasts.includes("先选一个 Team Mesh 智能体"));
    });
  }
});

test("empty agent selection blocks submission and preserves the entered credentials", async () => {
  const fixture = submitFixture({ agentId: "" });
  await fixture.submit();
  assert.equal(fixture.calls.length, 0);
  assert.deepEqual(fixture.toasts, ["先选一个 Team Mesh 智能体"]);
  assert.equal(fixture.metrics.resets, 0);
  assert.equal(fixture.form.appSecret.value, "fixture-secret");
});

test("a backend save error retains the form for correction and reports the actual error", async () => {
  const fixture = submitFixture({ reject: true });
  await fixture.submit();
  assert.equal(fixture.calls.length, 1);
  assert.deepEqual(fixture.toasts, ["fixture backend rejected credentials"]);
  assert.equal(fixture.metrics.resets, 0);
  assert.equal(fixture.form.appKey.value, "fixture-key");
  assert.equal(fixture.form.appSecret.value, "fixture-secret");
});
