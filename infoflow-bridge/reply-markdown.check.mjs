// Explicit offline rendering check. Supply an already installed marked module;
// this check does not install dependencies or contact an SDK/service.
import test from "node:test";
import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";
import { formatReply, replyUrl } from "./src/reply-format.js";

if (!process.argv[2]) throw new Error("Pass the path to a locally installed marked module");
const { marked } = await import(pathToFileURL(process.argv[2]).href);
const text = "中文🙂🚀".repeat(800);
const cases = {
  "backtick fence": "```text\n" + text + "\n```",
  "tilde fence": "~~~~text\n" + text + "\n~~~~",
  "indented fence": "  ```text\n" + text + "\n  ```",
  "list fence": "- 示例：\n\n  ```text\n  " + text + "\n  ```",
  "quote fence": "> ```text\n> " + text + "\n> ```",
  "link label": "[" + text + "](https://doc.example.test)",
  "link destination": "[示例](https://doc.example.test/" + "path".repeat(900) + ")",
  "link title": "[示例](https://doc.example.test \"" + text + "\")",
  "inline code": "`" + text + "`",
  "autolink": "<https://doc.example.test/" + "path".repeat(900) + ">",
};

for (const mode of ["chat", "issue"]) {
  const binding = mode === "issue" ? { issue_id: "11111111-1111-4111-8111-111111111111", identifier: "TASK-7" } : null;
  const url = replyUrl({ webUrl: "https://entry.example.test", workspaceSlug: "team" },
    { binding, commentId: binding ? "comment-7" : null, sessionId: binding ? null : "session-7" });
  for (const maxBytes of [320, 1800]) {
    for (const [name, content] of Object.entries(cases)) {
      test(`${mode} ${maxBytes} bytes: ${name} truncation renders the bold footer as an anchor`, () => {
        const output = formatReply(content, { mode, identifier: binding?.identifier,
          label: mode === "issue" ? "【成员 · 队长代发】\n" : "", url, maxBytes });
        const html = marked.parse(output);
        assert.ok(html.includes(`<strong><a href="${url}">完整对话链接</a></strong>`), html);
        assert.ok(html.includes(`<strong>[${mode === "issue" ? "任务模式] TASK-7" : "聊天模式]"}</strong>`), html);
        assert.ok(Buffer.byteLength(output) <= maxBytes);
        assert.equal(Buffer.from(output).toString("utf8"), output);
      });
    }
  }
}
