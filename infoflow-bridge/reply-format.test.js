import test from "node:test";
import assert from "node:assert/strict";
import { formatReply, replyUrl, withReplyMode } from "./src/reply-format.js";

const config = { webUrl: "https://web.example.test/base/", issueWebUrl: "https://entry.example.test/", workspaceSlug: "team" };
const binding = { issue_id: "11111111-1111-4111-8111-111111111111", identifier: "TASK-7", workspace_slug: "other" };

test("native comment and session routes use the configured entry and authoritative metadata", () => {
  assert.equal(replyUrl(config, { binding, commentId: "comment_1" }), "https://entry.example.test/other/issues/TASK-7#comment-comment_1");
  assert.equal(replyUrl(config, { binding: { ...binding, identifier: null }, commentId: "c2" }),
    "https://entry.example.test/other/issues/11111111-1111-4111-8111-111111111111#comment-c2");
  assert.equal(replyUrl(config, { sessionId: "session /🙂" }), "https://entry.example.test/team/chat?session=session%20%2F%F0%9F%99%82");
  assert.equal(replyUrl({ ...config, issueWebUrl: "" }, { sessionId: "s1" }), "https://web.example.test/base/team/chat?session=s1");
  assert.equal(replyUrl(config, { binding }), "https://entry.example.test/other/issues/TASK-7");
  assert.equal(replyUrl(config), "https://entry.example.test/team/chat");
});

test("invalid or missing precision metadata fails instead of inventing a link", () => {
  assert.throws(() => replyUrl({ ...config, workspaceSlug: "" }, { sessionId: "s1" }), /metadata/);
  assert.throws(() => replyUrl(config, { binding: {} }), /metadata/);
  assert.throws(() => replyUrl(config, { binding, commentId: "unsafe/value" }), /comment/);
  for (const issueWebUrl of ["file:///tmp/entry", "https://user:password@entry.example.test"]) {
    assert.throws(() => replyUrl({ ...config, issueWebUrl }), /invalid.*URL/);
  }
});

test("short reply preserves the exact body including whitespace and Markdown", () => {
  const content = "\n**结果**：中文🙂\n";
  const url = replyUrl(config, { sessionId: "s1" });
  assert.equal(formatReply(content, { mode: "chat", url }), withReplyMode(content, "chat") + `\n\n**[完整对话链接](${url})**`);
});

test("one UTF-8 prefix budgets header, footer, notice and 120 Unicode author characters", () => {
  const content = "中文🙂𝌆🚀\n".repeat(1200);
  const label = `【${"🚀".repeat(120)} · 队长代发】\n`;
  const url = replyUrl(config, { binding, commentId: "33333333-3333-4333-8333-333333333333" });
  const output = formatReply(content, { mode: "issue", identifier: binding.identifier, label, url });
  const prefix = withReplyMode(label, "issue", binding.identifier);
  const suffix = `\n\n...（回复较长，全文请在下方链接查看。）\n\n**[完整对话链接](${url})**`;
  assert.ok(output.startsWith(prefix));
  assert.ok(output.endsWith(suffix));
  const summary = output.slice(prefix.length, -suffix.length);
  assert.ok(summary.length > 0 && content.startsWith(summary) && summary.length < content.length);
  assert.equal(Buffer.from(output).toString("utf8"), output);
  assert.equal(output.includes("\ufffd"), false);
  assert.ok(Buffer.byteLength(output) <= 1800);
  assert.throws(() => formatReply(content, { mode: "issue", label, url, maxBytes: 100 }), /budget/);
});

test("the complete message byte boundary determines whether a notice is needed", () => {
  const url = replyUrl(config, { sessionId: "s1" });
  const overhead = Buffer.byteLength(withReplyMode("", "chat") + `\n\n**[完整对话链接](${url})**`);
  const body = "x".repeat(1800 - overhead);
  assert.equal(Buffer.byteLength(formatReply(body, { mode: "chat", url })), 1800);
  assert.equal(formatReply(body, { mode: "chat", url }).includes("回复较长"), false);
  assert.match(formatReply(body + "🙂", { mode: "chat", url }), /回复较长/);
});

test("truncating within a code fence keeps the notice and clickable footer outside it", () => {
  const url = replyUrl(config, { sessionId: "s1" });
  for (const marker of ["```", "~~~~"]) {
    const content = marker + "text\n" + "中文🙂".repeat(800) + "\n" + marker;
    const output = formatReply(content, { mode: "chat", url });
    assert.ok(output.includes(`\n${marker}\n\n...（回复较长`));
    assert.ok(output.endsWith(`**[完整对话链接](${url})**`));
    assert.ok(Buffer.byteLength(output) <= 1800);
  }
});

test("closing a truncated list fence retains its indentation instead of opening a new block", () => {
  const url = replyUrl(config, { sessionId: "s1" });
  const output = formatReply("- 示例：\n\n  ```text\n  " + "中文🙂".repeat(800) + "\n  ```", { mode: "chat", url });
  assert.ok(output.includes("\n  ```\n\n...（回复较长"));
  assert.ok(output.endsWith(`**[完整对话链接](${url})**`));
  assert.ok(Buffer.byteLength(output) <= 1800);
});
