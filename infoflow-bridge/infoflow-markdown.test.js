import test from "node:test";
import assert from "node:assert/strict";
import { infoflowMarkdown } from "./src/infoflow-markdown.js";
import { sendRobotReply } from "./src/robots.js";
import { deliverReply } from "./src/reply-mentions.js";
import { createHash } from "node:crypto";

test("internal AT links become plain names without changing other Markdown or normal links", () => {
  const source = "**结果**\n\n- [@小明](mention://member/person-id)\n- [@助手](mention://agent/agent-id)\n- [@小队](mention://squad/squad-id)\n- [@所有人](mention://all/all)\n\n[说明](https://example.test/a(b)?x=1&y=2) [@普通链接](https://example.test/person)\n[文档](../guide) `code`";
  const expected = "**结果**\n\n- @小明\n- @助手\n- @小队\n- @所有人\n\n[说明](https://example.test/a(b)?x=1&y=2) [@普通链接](https://example.test/person)\n[文档](../guide) `code`";
  assert.equal(infoflowMarkdown(source), expected);
  assert.equal(infoflowMarkdown(expected), expected);
});

test("escaped names remain safe plain text and names cannot expand the transport budget", () => {
  assert.equal(infoflowMarkdown("[@David\\[TF\\]](mention://member/id)"), "@David\\[TF\\]");
  assert.equal(infoflowMarkdown("[@**Boss**<b>](mention://member/id)"), "@\\*\\*Boss\\*\\*&lt;b&gt;");
  const source = "[@" + "*".repeat(1200) + "](mention://member/id)";
  assert.ok(Buffer.byteLength(infoflowMarkdown(source)) <= Buffer.byteLength(source));
});

test("inline and fenced code examples, images and escaped literal mentions are unchanged", () => {
  const mention = "[@小明](mention://member/id)";
  for (const source of [
    "`" + mention + "`", "``example ` " + mention + "``", "`multi\n" + mention + "`",
    "```md\n" + mention + "\n```", "~~~~md\n" + mention + "\n~~~~~",
    "  ```md\n" + mention + "\n  ```", "```md\n" + mention,
    "\\" + mention, "!" + mention,
    "[@普通](https://example.test/mention://member/id)",
    "[TASK-1](mention://issue/id)",
  ]) assert.equal(infoflowMarkdown(source), source);
  assert.equal(infoflowMarkdown("`unclosed " + mention), "`unclosed @小明");
  assert.equal(infoflowMarkdown("\\` " + mention + " \\`"), "\\` @小明 \\`");
  assert.equal(infoflowMarkdown("\\\\" + mention), "\\\\@小明");
  assert.equal(infoflowMarkdown("```md\n" + mention + "\n```\n" + mention), "```md\n" + mention + "\n```\n@小明");
});

test("malformed internal links and unrelated protocols are not rewritten", () => {
  for (const source of ["[@Alice](mention://member/)", "[@Alice](mention://member/id", "[@Alice](https://example.test)", "[@Alice](mention://unknown/id)", "mention://member/id", "[@Alice](mailto:person@example.test)"])
    assert.equal(infoflowMarkdown(source), source);
});

for (const exact of [false, true]) test("group transport merges Markdown and native AT without retrying an accepted body: " + exact, async () => {
 const sent=[],progress={}; let fail=true;
 const client={httpClient:{post:async(_path,wire)=>{
  const msg=JSON.parse(wire).message;sent.push(msg);
  if(fail)throw Error("transport failed");
  return {errcode:0,data:{messageid:"1234567890123456789"}};
 }}};
 const source="**结果**\n\n- [@小明](mention://member/id) 请查阅 [文档](https://example.test/docs)";
 const send=(c,t,o)=>sendRobotReply(client,c,t,o,exact);
 await assert.rejects(deliverReply(send,{kind:"group",id:"42"},source,["alice","bob"],"unused",progress),/transport failed/);
 fail=false;
 await deliverReply(send,{kind:"group",id:"42"},source,["alice","bob"],"unused",progress);
 assert.deepEqual(sent[0],sent[1]);
 assert.equal(sent[1].header.msgtype,"MD");
 assert.deepEqual(sent[1].body[0],{type:"AT",atall:false,atuserids:["alice","bob"]});
 assert.equal(sent[1].body[1].advanced,"1");
 assert.doesNotMatch(sent[1].body[1].content,/mention:\/\//);
 assert.ok(sent[1].body[1].content.includes("[文档](https://example.test/docs)"));
 await deliverReply(send,{kind:"group",id:"42"},source,["alice","bob"],"unused",progress);
 assert.equal(sent.length,2);
});

test("private transport converts internal links without any native AT or extra send", async () => {
  const sent = [], client = { im: { message: { sendToUser: async (...args) => sent.push(args) } } };
  await sendRobotReply(client, { kind: "private", id: "alice" }, "**结果** [@小明](mention://member/id)");
  assert.deepEqual(sent, [["alice", "**结果** @小明", "md"]]);
});
