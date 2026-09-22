// Keep 64-bit message identifiers as strings; never round them through Number.
export function receiptOptions(raw, kind, robotId) {
  const header = raw.message?.header || {};
  const baseMsgId = kind === "private" ? raw.MsgId ?? raw.msgId : header.messageid ?? raw.messageid;
  const msgId2 = kind === "private"
    ? raw.MsgId2 ?? raw.msgId2 ?? raw.msgid2
    : header.msgid2 ?? header.msgId2 ?? raw.msgid2 ?? raw.msgId2;
  const fromUid = kind === "private"
    ? raw.FromUserId ?? raw.fromUserId
    : header.fromuserid ?? raw.fromuserid;
  if ([baseMsgId, msgId2, fromUid].some((v) => v === undefined || v === null || v === "")) return null;
  if ([baseMsgId, msgId2].some((v) => typeof v === "number" && !Number.isSafeInteger(v))) return null;
  const chatId = kind === "private" ? robotId : raw.groupid ?? raw.groupId;
  if (!chatId) return null;
  return {
    fromUid: String(fromUid), chatType: kind === "private" ? 7 : 2,
    chatId,
    baseMsgId: String(baseMsgId), msgId2: String(msgId2), replyContent: "d101", replyDesc: "/收到",
  };
}

export async function acknowledge(client, raw, kind, robotId, logger = console) {
  const options = receiptOptions(raw, kind, robotId);
  if (!options) {
    logger.warn("receipt missing identifiers: " + JSON.stringify({ kind, keys: Object.keys(raw), headerKeys: Object.keys(raw.message?.header || {}) }));
    return false;
  }
  try {
    const result = await client.im.message.addEmojiReply(options);
    return result?.success === true;
  } catch (error) {
    logger.warn("receipt reaction failed: " + error.message);
    return false;
  }
}
