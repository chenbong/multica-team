import { createHash } from "node:crypto";
import { messageIdString } from "./conversation-context.js";

const ACCOUNT = /^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$/;
export const MAX_NOTIFY_USERS = 50;

export function notifyUsers(conversation, sender = null, users = []) {
  if (conversation?.kind !== "group") return [];
  if (!Array.isArray(users) || users.length > 10000) throw new Error("invalid notification users");
  const result = [];
  for (const user of [sender, ...users]) {
    if (user == null || user === "") continue;
    if (typeof user !== "string" || !ACCOUNT.test(user)) throw new Error("invalid notification account");
    const account = user.toLowerCase();
    if (!/[a-z]/.test(account) || /^[0-9a-f]{8}-[0-9a-f-]{27}$/.test(account) ||
        ["all", "atall", "robot", "bot"].includes(account)) throw new Error("notification target is not a human account");
    if (!result.includes(account)) result.push(account);
  }
  if (result.length > 5000) throw new Error("notification audience exceeds group size limit");
  return result;
}

export function mentionedReplyUsers(conversation, sender, mentions = []) {
  const users = notifyUsers(conversation, sender, mentions.filter(item => item?.kind === "user").map(item => item.userId));
  return users;
}

// Legacy conversion remains available for stored notices and old consumers.
// New automatic replies use combined Markdown and AT, never this TEXT format.
export function plainGroupReply(content) {
  return String(content)
    .replace(/\[([^\]\n]+)\]\((https?:\/\/[^\s)]+)\)/g, "$1：$2")
    .replace(/\*\*([^*]+)\*\*/g, "$1")
    .replace(/^ {0,3}(?:`{3,}|~{3,})[^\n]*$/gm, "")
    .replace(/`([^`\n]+)`/g, "$1")
    .replace(/^ {0,3}#{1,6}\s+/gm, "")
    .trim();
}
export function notificationText(url = null, identifier = null) {
  return `${identifier ? `[任务模式] ${identifier}\n` : ""}已回复，请查看上一条消息。`;
}

export function nativeAtBody(content, recipients) {
  const text = plainGroupReply(content);
  const link = text.match(/\n完整对话链接：(https?:\/\/\S+)$/);
  const useLink = link && Buffer.byteLength(link[1]) <= 1000;
  return [
    { type: "AT", atall: false, atuserids: recipients },
    { type: "TEXT", content: useLink ? text.slice(0, link.index) + "\n" : text },
    ...(useLink ? [{ type: "LINK", href: link[1], label: "完整对话链接" }] : []),
  ];
}

// Already accepted legacy bodies are terminal: do not replay them or send a
// delayed standalone AT. New sends persist one combined-message checkpoint.
export async function deliverReply(send, conversation, content, recipients, notice, progress = {}, save = () => {}, legacyNotice = null) {
  if (progress.bodySent) {
    if (!progress.notified) { progress.notified = true; save(); }
    return;
  }
  const users = notifyUsers(conversation, null, recipients);
  const digest = createHash("sha256").update(JSON.stringify([content, users])).digest("hex");
  if (progress.format === "combined-v2" && progress.digest !== digest) throw new Error("reply changed during retry");
  if (progress.format !== "combined-v2") {
    progress.format = "combined-v2";
    progress.digest = digest;
    progress.transport = {};
    save();
  }
  const options = conversation.kind === "group"
    ? { atUsers: users, deliveryState: progress.transport, saveDelivery: save } : {};
  const receipt = await send(conversation, content, options);
  const id = messageIdString(receipt?.messageid ?? receipt?.messageId ?? receipt?.msgkey);
  if (id) progress.messageId = id;
  progress.bodySent = true;
  progress.notified = true;
  save();
}
