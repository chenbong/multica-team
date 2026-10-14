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
  assert.match(html, /warmChatLinks\(\);\s+resetForm\(\);\s+toast\("已保存/);
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
    agents: [{ name: "Own", mine: true, workspaceId: "a", workspaceName: "A" },
      { name: "Foreign", mine: false, ownerEmail: "bob@example.test" }],
    me: { email: "alice@example.test" }, workspaces: [],
  };
  vm.runInNewContext(renderAgents + "\nrenderAgents();", {
    data, agentSelect: select,
    document: { createElement: () => ({ dataset: {} }), getElementById: () => hint },
  });
  assert.deepEqual(options.map((option) => option.value), ["", "Own"]);
  assert.doesNotMatch(hint.textContent, /别人|bob/);
  data.agents = [];
  vm.runInNewContext(renderAgents + "\nrenderAgents();", {
    data, agentSelect: select,
    document: { createElement: () => ({ dataset: {} }), getElementById: () => hint },
  });
  assert.equal(options[0].textContent, "当前用户暂未创建智能体");
});

test("editing an old binding never silently selects a same-name agent in another workspace", () => {
  assert.match(html, /if \(match\) match\.selected = true;\s+else form\.agent\.value = ""/);
  assert.match(html, /!keepWorkspace \? \[\.\.\.agentSelect\.options\]/);
});
