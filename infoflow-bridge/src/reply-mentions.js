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
  if (users.length > MAX_NOTIFY_USERS) throw new Error("too many notification users in one input");
  return users;
}

// Native TEXT + AT renders real mentions; Markdown body blocks hide them in
// current InfoFlow clients. Keep readable text and bare clickable URLs.
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
  return `${identifier ? `[任务模式] ${identifier}` : "[回复提醒]"}\n回复已更新，请查看上一条消息。${url ? `\n完整对话链接：${url}` : ""}`;
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

// Keep the Markdown body and native AT reminder as separate, durable stages.
// In particular, a failed AT send must not replay an already accepted body.
export async function deliverReply(send, conversation, content, recipients, notice, progress = {}, save = () => {}) {
  const users = notifyUsers(conversation, null, recipients);
  const digest = createHash("sha256").update(JSON.stringify([content, users, notice])).digest("hex");
  if (progress.digest && progress.digest !== digest) throw new Error("reply changed during notification retry");
  if (!progress.digest) { progress.digest = digest; save(); }
  if (!progress.bodySent) {
    await send(conversation, content, {});
    progress.bodySent = true; save();
  }
  if (users.length && !progress.notified) {
    const start = progress.notificationNext || 0;
    if (!Number.isInteger(start) || start < 0 || start > users.length) throw new Error("invalid notification progress");
    for (let next = start; next < users.length; next += MAX_NOTIFY_USERS) {
      const receipt = await send(conversation, notice, { atUsers: users.slice(next, next + MAX_NOTIFY_USERS), notificationOnly: true });
      progress.notificationNext = Math.min(next + MAX_NOTIFY_USERS, users.length);
      const id = messageIdString(receipt?.messageid ?? receipt?.messageId);
      if (id) progress.notificationMessageIds = [...(progress.notificationMessageIds || []), id];
      save();
    }
    progress.notified = true; save();
  }
}
