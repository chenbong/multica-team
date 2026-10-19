import test from "node:test";
import assert from "node:assert/strict";
import { formatReply, taskReplyReference, withReplyMode } from "./src/reply-format.js";

test("task headers show the server identifier and title without changing the body", () => {
  const body = "**结论**\n\n- 保留列表\n\n[正常链接](https://example.test/result)\n\n```js\nconst answer = 42;\n```";
  assert.equal(withReplyMode(body, "issue", "INFE-12", "分析任务"), `**[任务模式] INFE-12:分析任务**\n\n${body}`);
  assert.equal(withReplyMode(body, "chat", "INFE-12", "保密名称"), `**[聊天模式]**\n\n${body}`);
  assert.equal(withReplyMode(body, "issue", null, "不能推测的任务"), `**[任务模式]**\n\n${body}`);
  assert.equal(withReplyMode(body, "issue", "INFE-12", null), `**[任务模式] INFE-12**\n\n${body}`);
  assert.equal(withReplyMode(body, "issue", "INFE-12", {}), `**[任务模式] INFE-12**\n\n${body}`);
  assert.throws(() => taskReplyReference("INFE-12\nother", "title"), /invalid reply issue identifier/);
});

test("untrusted title formatting stays one-line literal Markdown text", () => {
  const title = "  **加粗**\n[成员](mention://member/not-a-link) `<tag>`\r\n\u202e第二行  ";
  const output = withReplyMode("原始正文", "issue", "INFE-12", title);
  const header = output.split("\n\n")[0];
  assert.equal(header.split("\n").length, 1);
  assert.ok(header.includes("\\*\\*加粗\\*\\*"));
  assert.ok(header.includes("\\[成员\\]\\(mention://member/not-a-link\\)"));
  assert.ok(header.includes("&lt;tag&gt;"));
  assert.doesNotMatch(header, /\u202e/);
  assert.equal(output.split("\n\n")[1], "原始正文");
});

test("long UTF-8 task names fit fresh and legacy message budgets", () => {
  const title = "任务🚀**[名称]**".repeat(300);
  const body = "中文🙂".repeat(500);
  for (const maxBytes of [320, 1800]) {
    const output = formatReply(body, { mode: "issue", identifier: "INFE-12", title,
      url: "https://example.test/team/issues/INFE-12#comment-c1", maxBytes });
    assert.ok(output.startsWith("**[任务模式] INFE-12:"));
    assert.ok(output.includes("..."));
    assert.ok(output.endsWith("**[完整对话链接](https://example.test/team/issues/INFE-12#comment-c1)**"));
    assert.ok(Buffer.byteLength(output) <= maxBytes);
    assert.equal(Buffer.from(output).toString("utf8"), output);
  }
  const legacy = "x".repeat(1830);
  const output = withReplyMode(legacy, "issue", "INFE-12", title);
  assert.ok(Buffer.byteLength(output) <= 1900);
  assert.ok(output.endsWith(legacy));
  const tight = withReplyMode("x".repeat(1868), "issue", "INFE-12", title);
  assert.ok(Buffer.byteLength(tight) <= 1900);
  assert.ok(tight.endsWith("x".repeat(1868)));
});
