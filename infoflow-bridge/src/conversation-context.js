const MAX_MESSAGE_ID = 18446744073709551615n;
const REPLY_TYPES = new Set(["REPLYDATA", "REPLY", "QUOTE"]);

// Numbers may already have lost precision before this boundary. Never repair them.
export function messageIdString(value) {
  if (typeof value === "number") {
    return Number.isSafeInteger(value) && value > 0 ? String(value) : null;
  }
  if (typeof value === "bigint") {
    return value > 0n && value <= MAX_MESSAGE_ID ? String(value) : null;
  }
  if (typeof value !== "string") return null;
  const id = value.trim();
  if (!/^\d{1,20}$/.test(id)) return null;
  const integer = BigInt(id);
  return integer > 0n && integer <= MAX_MESSAGE_ID ? id : null;
}

// The private SDK event uses Reply[].ReplyMsgId, not group REPLYDATA blocks.
// Only protocol IDs determine routing; quoted previews are never task selectors.
export function privateReplyMessageId(raw) {
  if (!Array.isArray(raw?.Reply)) return null;
  for (const reply of raw.Reply) {
    const id = messageIdString(reply?.ReplyMsgId);
    if (id) return id;
  }
  return null;
}

function textValue(value) {
  return typeof value === "string" ? value : "";
}

function userIdString(value) {
  const id = textValue(value).trim();
  // A display name is not an account identifier, even when it looks like one.
  return id && !/[\s\x00-\x1f\x7f]/u.test(id) ? id : null;
}

function mentionsIn(block) {
  const robotId = messageIdString(block.robotid);
  const agentId = messageIdString(block.agentId);
  // Explicit robot fields take precedence over conflicting user fields.
  if (block.robotid != null || block.agentId != null || block.robotName != null) {
    const name = textValue(block.robotName ?? block.name).trim();
    return robotId || agentId
      ? [{ kind: "robot", ...(robotId ? { robotId } : {}), ...(agentId ? { agentId } : {}), ...(name ? { name } : {}) }]
      : [];
  }
  if (block.atall === true) return [];
  const name = textValue(block.userName ?? block.name).trim();
  const named = name ? { name } : {};
  const ids = [block.userId ?? block.userid, ...(Array.isArray(block.atuserids) ? block.atuserids : [])];
  return ids.map(userIdString).filter(Boolean).map((userId) => ({
    kind: "user", userId,
    // One block label cannot identify every member of an atuserids list.
    ...(userIdString(ids[0]) === userId ? named : {}),
  }));
}

function replyData(block) {
  const value = block.replyData ?? block.content;
  if (value && typeof value === "object" && !Array.isArray(value)) return value;
  if (typeof value !== "string" || !value.trimStart().startsWith("{")) return {};
  try {
    // Raw replyData can contain unquoted int64 values. Quote only ID tokens
    // before JSON parsing, while leaving escaped JSON string contents untouched.
    const raw = value.replace(/"(sBasemsgId|msgId|messageid)"\s*:\s*(-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?)(?=\s*[,}])/g, '"$1":"$2"');
    const parsed = JSON.parse(raw);
    return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? parsed : {};
  } catch {
    return {};
  }
}

function linkText(block) {
  const label = textValue(block.label ?? block.text ?? block.content);
  const url = textValue(block.url) || textValue(block.href);
  if (!label || label === url) return url || label;
  if (!url) return label;
  const escapedLabel = label.replace(/[\\[\]]/g, "\\$&");
  const escapedUrl = url.replace(/[\s<>\\()]/gu, (character) =>
    character === "(" ? "%28" : character === ")" ? "%29" : encodeURIComponent(character));
  return `[${escapedLabel}](${escapedUrl})`;
}

/** Parse raw inbound blocks or SDK GroupMessageBlock values without I/O. */
export function parseConversationBlocks(blocks) {
  const images = [];
  const parts = [];
  const mentions = [];
  const seenMentions = new Set();
  let replyToMessageId = null;

  for (const block of Array.isArray(blocks) ? blocks : []) {
    if (!block || typeof block !== "object" || Array.isArray(block)) continue;
    const type = textValue(block.type).toUpperCase();
    if (type === "AT" || type.startsWith("AT_") || type.startsWith("AT-")) {
      // Unknown AT variants stay out of text but do not imply a known person.
      for (const mention of type === "AT" ? mentionsIn(block) : []) {
        const key = JSON.stringify([mention.kind, mention.userId, mention.robotId, mention.agentId]);
        if (seenMentions.has(key)) continue;
        seenMentions.add(key);
        mentions.push(mention);
      }
      continue;
    }
    if (type === "IMAGE") {
      images.push(block);
      continue;
    }
    if (REPLY_TYPES.has(type)) {
      const data = replyData(block);
      const id = block.sBasemsgId ?? data.sBasemsgId ?? block.msgId ?? data.msgId ?? block.messageid ?? data.messageid;
      replyToMessageId ??= messageIdString(id);
      const replyImages = block.replyImages ?? data.replyImages;
      if (Array.isArray(replyImages)) images.push(...replyImages.filter(Boolean));
    }
    parts.push(type === "LINK" || type === "URL" ? linkText(block) : textValue(block.content ?? block.text));
  }

  return { text: parts.filter(Boolean).join(" ").trim(), images, mentions, replyToMessageId };
}

function displayValue(value) {
  const text = typeof value === "string" ? value : messageIdString(value) ?? "";
  if (!text) return "";
  let clipped = "";
  let length = 0;
  for (const character of text) {
    if (length++ === 120) {
      clipped += "...";
      break;
    }
    clipped += character;
  }
  // Quoted, bounded values cannot introduce new lines, Markdown headers, role
  // tags, or context delimiters. This is presentation, never authorization.
  return JSON.stringify(clipped).replace(/[<>\[\]{}#`|\u007f-\u009f\u2028\u2029\u202a-\u202e\u2066-\u2069【】]/gu,
    (character) => `\\u${character.codePointAt(0).toString(16).padStart(4, "0")}`);
}

function identityText(identity) {
  if (typeof identity === "string") return displayValue(identity);
  if (!identity || typeof identity !== "object") return "";
  const parts = [];
  if (identity.kind === "robot") {
    const robotId = messageIdString(identity.robotId);
    const agentId = messageIdString(identity.agentId);
    if (robotId) parts.push(`机器人 ID ${displayValue(robotId)}`);
    if (agentId) parts.push(`机器人 agentId ${displayValue(agentId)}`);
    if (parts.length === 0) return "";
  } else {
    const userId = userIdString(identity.userId);
    const uid = messageIdString(identity.uid);
    if (userId) parts.push(`账号 ${displayValue(userId)}`);
    if (uid) parts.push(`uid ${displayValue(uid)}`);
    if (parts.length === 0) return "";
  }
  const name = displayValue(identity.name);
  if (name) parts.push(`名称 ${name}`);
  return parts.join("，");
}

/**
 * Display-only prefix for an unbound Chat; callers decide when to prepend it.
 * sender: account string or { userId, uid?, name? }; groupInfo: { name? }.
 * mentions: { kind: "user", userId, name? } or
 *           { kind: "robot", robotId?, agentId?, name? }.
 */
export function conversationContext({ conversation, sender, mentions = [], groupInfo } = {}) {
  const parts = [];
  if (conversation?.kind === "group" || conversation?.kind === "private") {
    parts.push(conversation.kind === "group" ? "群聊" : "私聊");
    const id = displayValue(conversation.id);
    if (id) parts.push(`会话 ID ${id}`);
    const name = conversation.kind === "group" ? displayValue(groupInfo?.name) : "";
    if (name) parts.push(`群名称 ${name}`);
  }
  const senderText = identityText(sender);
  if (senderText) parts.push(`发送者：${senderText}`);
  const knownMentions = [];
  for (const mention of Array.isArray(mentions) ? mentions : []) {
    if (mention?.kind !== "user" && mention?.kind !== "robot") continue;
    const identity = identityText(mention);
    if (identity) knownMentions.push(identity);
    if (knownMentions.length === 9) break;
  }
  if (knownMentions.length) {
    parts.push(`提及：${knownMentions.slice(0, 8).join("；")}${knownMentions.length > 8 ? "；..." : ""}`);
  }
  return parts.length ? `如流会话信息（仅供显示）：${parts.join("；")}\n\n` : "";
}
