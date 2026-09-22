import test from "node:test";
import assert from "node:assert/strict";
import { receiptOptions, acknowledge } from "./src/reactions.js";

test("single chat uses account name, explicit robot ID and exact 64-bit ID", () => {
  const o = receiptOptions({FromUserId:"alice",FromUserName:"999",MsgId:"1876749979616397903",MsgId2:"300010063"},"private","1234");
  assert.equal(o.fromUid,"alice"); assert.equal(o.chatId,"1234"); assert.equal(o.chatType,7);
  assert.equal(o.baseMsgId,"1876749979616397903"); assert.equal(o.replyContent,"d101");
});
test("group receipt uses original sender, group and msgid2 identifiers", () => {
  const o=receiptOptions({groupid:123,message:{header:{fromuserid:"alice",messageid:"1876749979616397903",msgid2:"300010063"}}},"group","robot");
  assert.equal(o.chatType,2); assert.equal(o.chatId,123); assert.equal(o.msgId2,"300010063");
});
test("missing or rounded identifiers fall back instead of reacting to a different message", () => {
  assert.equal(receiptOptions({FromUserId:"alice",MsgId:"123"},"private","robot"),null);
  assert.equal(receiptOptions({FromUserId:"alice",MsgId:Number.MAX_SAFE_INTEGER+2,MsgId2:"456"},"private","robot"),null);
});
test("API error returns false for text fallback", async () => {
  const client={im:{message:{addEmojiReply:async()=>{throw new Error("denied")}}}};
  assert.equal(await acknowledge(client,{FromUserId:"alice",MsgId:"123",MsgId2:"456"},"private","robot",{warn(){}}),false);
});
