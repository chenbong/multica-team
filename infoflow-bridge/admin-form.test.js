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
