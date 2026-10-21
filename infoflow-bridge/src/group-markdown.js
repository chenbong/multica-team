import { randomInt } from "node:crypto";
import { infoflowMarkdown } from "./infoflow-markdown.js";
import { MAX_REPLY_LENGTH, replyFenceSuffix } from "./reply-format.js";
import { notifyUsers, MAX_NOTIFY_USERS } from "./reply-mentions.js";
import { markdownImages, trimImageMarkdown } from "./markdown-images.js";

// InfoFlow substitutes AT before Markdown parsing, even inside code and URLs.
// Keep URI destinations equivalent via percent encoding. Within code, mask only
// selected accounts; canonical task text remains untouched on the web.
export function literalBodyMentions(text, recipients = []) {
  const protectSelected = token => recipients.reduce(
    (value, account) => value.replace(new RegExp("@" + account.replaceAll(".", "\\."), "gi"),
      match => "@\u200b" + match.slice(1)), token);
  return text.replace(/(`{3,}|~{3,})[\s\S]*?\1|(`+)[^\n]*?\2|https?:\/\/[^\s<>]+|[\w.+-]+@[\w.-]+\.[A-Za-z]{2,}|@[A-Za-z0-9._-]+/g,
    token => {
      if (/^https?:\/\//.test(token)) return token.replace(/^(https?:\/\/[^/?#]*)([\s\S]*)$/,
        (_match, authority, rest) => authority + rest.replaceAll("@", "%40"));
      if (token.startsWith("`") || token.startsWith("~")) return protectSelected(token);
      if (token.startsWith("@")) return "@\u200b" + token.slice(1);
      return token.replaceAll("@", "&#64;");
    });
}

export function combinedMarkdown(content, recipients, omitted = 0) {
  const text = infoflowMarkdown(String(content));
  const match = text.match(/\n\n\*\*\[完整对话链接\]\(https?:\/\/[^\n]+\)\*\*$/);
  const footer = match?.[0] || "";
  const rawBody = footer ? text.slice(0, -footer.length) : text;
  const body = recipients.length ? literalBodyMentions(rawBody, recipients) : rawBody;
  const mentions = recipients.length ? "\n\n" + recipients.map(user => "@" + user).join(" ") : "";
  const overflow = omitted ? `\n\n（提醒人数超过 50 人，另有 ${omitted} 人未被 @ 提醒。）` : "";
  const suffix = mentions + overflow + footer;
  const closing = replyFenceSuffix(body);
  if ((body + closing + suffix).length <= MAX_REPLY_LENGTH) return body + closing + suffix;
  const hint = footer ? "\n\n...（回复较长，全文请在下方链接查看。）" : "\n\n...（内容较长，后续已省略。）";
  const budget = MAX_REPLY_LENGTH - suffix.length - hint.length;
  if (budget < 0) throw new Error("notification metadata exceeds message budget");
  if (markdownImages(body).length) {
    let available = budget, summary, fence;
    do {
      summary = trimImageMarkdown(body, available);
      fence = replyFenceSuffix(summary);
      available -= Math.max(1, summary.length + fence.length - budget);
    } while (summary.length + fence.length > budget && available >= 0);
    return summary + fence + hint + suffix;
  }
  const chars = [];
  let used = 0;
  for (const char of body) {
    if (used + char.length > budget) break;
    chars.push(char); used += char.length;
  }
  let summary = chars.join(""), fence = replyFenceSuffix(summary);
  while (used + fence.length > budget && chars.length) {
    used -= chars.pop().length;
    summary = chars.join(""); fence = replyFenceSuffix(summary);
  }
  return summary + fence + hint + suffix;
}

export function prepareGroupDelivery(conversation, content, options) {
  if (options.notificationOnly) throw new Error("standalone AT reminders are retired");
  const state = options.deliveryState || {};
  if (!state.id) {
    const users = notifyUsers(conversation, options.atUser, options.atUsers || []);
    state.id = randomInt(1, 2 ** 48);
    state.time = Date.now();
    state.users = users.slice(0, MAX_NOTIFY_USERS);
    state.omitted = Math.max(0, users.length - state.users.length);
    state.original = String(content);
    state.content = combinedMarkdown(state.original, state.users, state.omitted);
    options.saveDelivery?.();
  }
  if (!Number.isSafeInteger(state.id) || state.id <= 0 ||
      !Number.isSafeInteger(state.time) || state.time <= 0 ||
      !Array.isArray(state.users) || state.users.length > MAX_NOTIFY_USERS ||
      typeof state.content !== "string" || state.content.length > MAX_REPLY_LENGTH) {
    throw new Error("invalid persisted group delivery");
  }
  notifyUsers(conversation, null, state.users);
  return state;
}

export function groupMarkdownBody(state) {
  return [
    ...(state.users.length ? [{ type: "AT", atall: false, atuserids: state.users }] : []),
    { type: "MD", advanced: "1", content: state.content },
  ];
}

// A generic 40000, timeout, or a missing robot is NOT evidence that a human
// recipient left the group. Unknown errors retain the original audience.
export function invalidHumanAudience(code, message) {
  if (![40000, 40040].includes(code) || /机器人|robot|agent/i.test(message)) return false;
  return /(?:atuserids?|at users?|被@|艾特|提及).*(?:not.{0,20}(?:group|member)|不在群|不是群成员|不存在)/i.test(message);
}
