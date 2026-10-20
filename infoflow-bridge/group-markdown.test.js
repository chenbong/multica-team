import test from "node:test";
import assert from "node:assert/strict";
import { combinedMarkdown, literalBodyMentions, invalidHumanAudience } from "./src/group-markdown.js";
import { sendGroupExact } from "./src/issue-groups.js";
import { formatReply } from "./src/reply-format.js";

const group = { kind: "group", id: "13579" };
test("subscription delivery checks each group's current members before applying the 50-person cap", async () => {
  const users=Array.from({length:60},(_,i)=>"person"+i),wires=[];
  const client={httpClient:{post:async(path,wire)=>{
    if(path.endsWith("/memberList"))return {errcode:0,data:{agentInfoList:[],userInfoList:users.slice(10).map(userId=>({userId}))}};
    wires.push(JSON.parse(wire).message);return {errcode:0,data:{messageid:"123"}};
  }}};
  await sendGroupExact(client,group,"reply",{atUsers:users,filterGroupMembers:true,deliveryState:{},beforeSend:async()=>{}});
  assert.deepEqual(wires[0].body[0].atuserids,users.slice(10));
});

test("unbind while checking membership prevents the external send", async () => {
  let active=true,sends=0;
  const client={httpClient:{post:async path=>{
    if(path.endsWith("/memberList")){active=false;return {errcode:0,data:{agentInfoList:[],userInfoList:[{userId:"alice"}]}}}
    sends++;return {errcode:0,data:{messageid:"123"}};
  }}};
  await assert.rejects(sendGroupExact(client,group,"reply",{
    atUsers:["alice"],filterGroupMembers:true,deliveryState:{},
    beforeSend:async()=>{if(!active)throw Error("binding removed")},
  }),/binding removed/);
  assert.equal(sends,0);
});
test("mentions reserve space above the untouched link and do not become native inside body prose", () => {
  const url = "https://example.test/task";
  const result = combinedMarkdown(formatReply("正文 @alice\n" + "中文🙂".repeat(4000), { url }), ["alice"]);
  assert.ok(result.length <= 8192);
  assert.ok(result.includes("@\u200balice"));
  assert.ok(result.endsWith(`@alice\n\n**[完整对话链接](${url})**`));
  assert.match(result, /回复较长/);
  assert.equal(Buffer.from(result).toString("utf8"), result);
});

test("body mentions and code avoid pre-Markdown AT replacement; URI semantics are preserved", () => {
  const input = "@alice and a@alice.example and https://example.test/@alice and `@alice`\n```js\nconst u='@alice';\n```";
  const result = literalBodyMentions(input, ["alice"]);
  assert.ok(result.includes("@\u200balice"));
  assert.ok(result.includes("a&#64;alice.example"));
  assert.ok(result.includes("https://example.test/%40alice"));
  assert.ok(result.includes("`@\u200balice`"));
  assert.ok(result.includes("const u='@\u200balice';"));
  assert.equal(literalBodyMentions("`@decorator`", ["alice"]), "`@decorator`");
  assert.equal(literalBodyMentions("`@ALICE`", ["alice"]), "`@\u200bALICE`");
});

test("native mention row remains outside a truncated code fence", () => {
  const result = combinedMarkdown("```js\n" + "中文🙂".repeat(3000), ["alice"]);
  assert.ok(result.length <= 8192);
  assert.match(result, /\n```\n\n\.\.\./);
  assert.ok(result.endsWith("@alice"));
});

test("only explicit human membership rejections can change recipients", () => {
  assert.equal(invalidHumanAudience(40000, "atuserids user is not a group member"), true);
  assert.equal(invalidHumanAudience(40000, "机器人未在群成员列表中"), false);
  assert.equal(invalidHumanAudience(40000, "invalid request"), false);
  assert.equal(invalidHumanAudience(500, "atuserids user is not a group member"), false);
});

test("confirmed stale members are removed once, then one combined message is retried and pinned", async () => {
  const wires = [], state = {}; let saves = 0, lists = 0;
  const client = { httpClient: { post: async (path, wire) => {
    if (path.endsWith("/memberList")) {
      lists++;
      return { errcode: 0, data: { agentInfoList: [], userInfoList: [{ userId: "alice" }] } };
    }
    const message = JSON.parse(wire).message; wires.push(message);
    if (wires.length === 1) return { errcode: 40000, errmsg: "atuserids user is not a group member" };
    return { errcode: 0, data: { messageid: "1234567890123456789" } };
  } } };
  await sendGroupExact(client, group, "**Result**", { atUsers: ["alice", "departed"], deliveryState: state, saveDelivery: () => saves++ });
  assert.equal(lists, 1); assert.equal(wires.length, 2); assert.equal(saves, 2);
  assert.deepEqual(wires[1].body[0].atuserids, ["alice"]);
  assert.equal(wires[1].header.msgtype, "MD");
  assert.doesNotMatch(wires[1].body[1].content, /@departed/);
  assert.deepEqual(state.users, ["alice"]);
});

test("ambiguous timeout and generic denial never query roster or send a fallback", async () => {
  for (const response of [null, { errcode: 40000, errmsg: "bad request" }]) {
    let calls = 0;
    const client = { httpClient: { post: async path => {
      calls++; assert.equal(path, "/robot/msg/groupmsgsend");
      if (response === null) throw Error("timeout");
      return response;
    } } };
    await assert.rejects(sendGroupExact(client, group, "body", { atUsers: ["alice"] }));
    assert.equal(calls, 1);
  }
});
