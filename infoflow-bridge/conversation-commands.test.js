import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { IssueRelay, relayOperation } from "./src/issue-relay.js";
import { formatCommandNotice } from "./src/commands.js";

const agent = "11111111-1111-4111-8111-111111111111";
const choices = [3, 7].map(n => ({ issue_id: `issue-${n}`, identifier: `ICLR-${n}`, workspace_slug: "iclr", status: "in_progress", binding_kinds: "group" }));
const quiet = { log() {}, warn() {}, error() {} };

function fixture(t, kind, response) {
  const dir = mkdtempSync(join(tmpdir(), "conversation-command-"));
  const sent = [], requests = [];
  const config = { robotId: "robot", issueRelay: { enabled: true }, statePath: join(dir, "state"), pollIntervalMs: 3600000,
    multica: { defaultAgent: agent, workspaceId: "ws", workspaceSlug: "iclr", webUrl: "https://web.example.test", issueWebUrl: "http://issues.example.test" } };
  const relay = new IssueRelay(config, {}, async (_conversation, content) => sent.push(content), quiet, {
    groups: { inspect: async () => assert.fail("read-only commands must not reconcile robots") },
    stageImages: async () => assert.fail("read-only commands must not stage attachments"),
  });
  relay.request = async (path, body) => {
    requests.push({ path, body });
    assert.equal(path, "issue-relay");
    assert.equal(body.operation, "binding");
    return structuredClone(response);
  };
  relay.fallback = async () => assert.fail("handled command must not invoke legacy Chat");
  const input = text => ({ conversation: { kind, id: kind === "group" ? "13461574" : "alice" }, user: "alice", text, messageId: "123456789", replyToMessageId: "unrelated-quote" });
  t.after(() => { relay.stop(); rmSync(dir, { recursive: true, force: true }); });
  return { relay, sent, requests, input };
}

test("classify conversation commands separately from task-specific operations", () => {
  for (const command of ["help", "status"]) {
    assert.deepEqual(relayOperation(` /${command.toUpperCase()}  `), { operation: "binding", command, content: "" });
  }
  assert.deepEqual(relayOperation("/binding"), { operation: "binding", command: "status", content: "", explicitBinding: true });
  assert.deepEqual(relayOperation("/status 2"), { operation: "binding", command: "status", statusPage: 2, content: "" });
  assert.deepEqual(relayOperation("/status ICLR-3"), { operation: "binding", command: "task_status", issue_ref: "ICLR-3", content: "" });
  assert.equal(relayOperation("/unbind ICLR-3").operation, "unbind");
  assert.equal(relayOperation("继续优化").operation, "message");
});

for (const kind of ["private", "group"]) {
  for (const command of ["help", "status"]) {
    test(`${kind} /${command}: independent of task selection, with receipt and no generic chat footer`, async t => {
      const response = { handled: true, silent: false, conversation_mode: "issue", notice_kind: command,
        notice: command === "help" ? "基本操作命令" : "当前关联的任务：", task_choices: command === "help" ? [] : choices };
      const f = fixture(t, kind, response); let acknowledged = 0;
      const input = { ...f.input(`/${command}`), acknowledge: async () => { acknowledged++; return true; } };
      await f.relay.handle(input);
      await f.relay.handle(input);
      assert.equal(acknowledged, 1);
      assert.equal(f.requests.length, 1);
      assert.equal(f.requests[0].body.command, command);
      assert.equal(f.requests[0].body.expected_binding_id, undefined);
      assert.equal(f.sent.length, 1);
      assert.doesNotMatch(f.sent[0], /\/iclr\/chat|当前智能体关联多个任务|当前绑定任务/);
      if (command === "help") {
        assert.match(f.sent[0], /`\/help`/);
        assert.match(f.sent[0], /`\/status`/);
        assert.doesNotMatch(f.sent[0], /完整对话链接|ICLR-3|ICLR-7|`\/binding/);
      } else {
        for (const n of [3, 7]) assert.match(f.sent[0], new RegExp(`ICLR-${n}（in_progress；群聊）：\\[完整对话链接\\]\\(http://issues.example.test/iclr/issues/ICLR-${n}\\)`));
      }
      assert.ok(Buffer.byteLength(f.sent[0]) <= 1800);
    });
  }
}

test("ambiguity lists server-authorized task links and preserves them across a failed send", async t => {
  const f = fixture(t, "group", { handled: true, silent: false, conversation_mode: "issue", notice_kind: "task_choices", task_choices: choices,
    notice: "当前智能体关联多个任务，请回复对应任务消息或带上指定任务编号" });
  const send = f.relay.send;
  f.relay.send = async () => { throw new Error("transport unavailable"); };
  await f.relay.handle(f.input("继续优化"));
  assert.deepEqual(Object.values(f.relay.inbox)[0].routingNotice.task_choices, choices);
  f.relay.send = send;
  await f.relay.poll();
  assert.equal(f.requests.filter(r => r.path === "issue-relay").length, 1, "a failed clarification must not be rerouted");
  assert.equal(f.sent.length, 1);
  assert.match(f.sent[0], /^\*\*\[任务模式\]\*\*/);
  for (const n of [3, 7]) assert.ok(f.sent[0].includes(`- ICLR-${n}：[完整对话链接](http://issues.example.test/iclr/issues/ICLR-${n})`));
  assert.doesNotMatch(f.sent[0], /\/chat|\*\*\[完整对话链接\]/);
});

test("large status lists stay one message without cutting any task hyperlink", async t => {
  const many = Array.from({ length: 80 }, (_, i) => ({ ...choices[0], identifier: `ICLR-${i + 1}`, binding_kinds: i % 2 ? "private" : "" }));
  const f = fixture(t, "private", { handled: true, silent: false, conversation_mode: "issue", notice_kind: "status", notice: "当前智能体参与的任务：", task_choices: many });
  await f.relay.handle(f.input("/status"));
  assert.equal(f.sent.length, 1);
  assert.ok(Buffer.byteLength(f.sent[0]) <= 1800);
  assert.match(f.sent[0], /共 80 个任务，第 1\/\d+ 页/);
  assert.match(f.sent[0], /发送 \/status 2/);
  assert.match(f.sent[0], /未绑定会话/);
  assert.match(f.sent[0], /私聊/);
  for (const line of f.sent[0].split("\n").filter(line => line.startsWith("- ICLR-"))) assert.match(line, /\[完整对话链接\]\(http:\/\/[^\s]+\)$/);
  const totalPages = Number(f.sent[0].match(/第 1\/(\d+) 页/)[1]);
  const identifiers = [];
  for (let page = 1; page <= totalPages; page++) {
    const message = formatCommandNotice(f.relay.config.multica, { handled: true, notice_kind: "status", conversation_mode: "issue", notice: "当前智能体参与的任务：", task_choices: many, status_page: page });
    assert.ok(Buffer.byteLength(message) <= 1800);
    identifiers.push(...[...message.matchAll(/^- (ICLR-\d+)（/gm)].map(match => match[1]));
  }
  assert.deepEqual(identifiers, many.map(c => c.identifier), "all tasks remain reachable without omission or duplicate rows");
});

test("status page selection reaches the service without a task selector or reconciliation", async t => {
  const f = fixture(t, "group", { handled: true, silent: false, notice_kind: "status", status_page: 2, notice: "当前群绑定的任务：", task_choices: choices });
  await f.relay.handle(f.input("/status 2"));
  assert.equal(f.requests.length, 1);
  assert.equal(f.requests[0].body.command, "status");
  assert.equal(f.requests[0].body.status_page, 2);
  assert.equal(f.requests[0].body.issue_ref, undefined);
  assert.equal(f.requests[0].body.expected_binding_id, undefined);
  assert.match(f.sent[0], /页码超出范围/);
  assert.doesNotMatch(f.sent[0], /完整对话链接|\/chat/);
});

test("status includes escaped task titles and all responsible agents, including a squad's leader", () => {
  const message = formatCommandNotice({ webUrl: "https://web.example.test" }, { conversation_mode: "issue", notice_kind: "status", notice: "当前群绑定的任务：", task_choices: [
    { ...choices[0], title: "PiCoKV 复现", agent_names: ["智能体 A", "智能体 B"] },
    { ...choices[1], title: "[not a link](bad)\n# heading", agent_names: ["<fake>"] },
  ] });
  assert.match(message, /ICLR-3 PiCoKV 复现（智能体 A，智能体 B；in_progress；群聊）/);
  assert.ok(message.includes("ICLR-7 \\[not a link\\]\\(bad\\) # heading（&lt;fake&gt;"));
  assert.equal((message.match(/\[完整对话链接\]/g) || []).length, 2);
  assert.ok(Buffer.byteLength(message) <= 1800);
});

test("long titles and large squads remain one safe row with the full task link", () => {
  const message = formatCommandNotice({ webUrl: "https://web.example.test" }, { conversation_mode: "issue", notice_kind: "status", notice: "当前任务：", task_choices: [
    { ...choices[0], title: "长标题🙂".repeat(500), agent_names: Array.from({length: 100}, (_, i) => "智能体" + i + "🙂".repeat(30)) },
    { ...choices[1], title: "待分配", agent_names: [] },
  ] });
  assert.ok(Buffer.byteLength(message) <= 1800);
  assert.match(message, /另 \d+ 个智能体/);
  assert.match(message, /暂无智能体负责人/);
  assert.doesNotMatch(message, /\uFFFD/);
  for (const line of message.split("\n").filter(line => line.startsWith("- ICLR-"))) assert.match(line, /\[完整对话链接\]\(https:\/\/[^\s]+\)$/);
});
