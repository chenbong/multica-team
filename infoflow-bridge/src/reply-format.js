import { inlineMarkdown } from "./inline-markdown.js";
import { markdownImages, trimImageMarkdown } from "./markdown-images.js";

// IM-only presentation. Never apply this to prompts, comments, or stored task
// content. The caller supplies the authoritative route, not model-written text.
export function taskReplyReference(identifier, title = null, { markdown = true, maxTitleBytes = 180 } = {}) {
  if (identifier == null || identifier === "") return "";
  const ref = String(identifier);
  if (!/^[A-Za-z0-9]{1,10}-\d+$/.test(ref) || ref.length > 100) throw new Error("invalid reply issue identifier");
  const name = typeof title === "string"
    ? title.replace(/[\x00-\x1f\x7f-\x9f]/g, " ").replace(/[\u200b-\u200f\u202a-\u202e\u2060-\u206f]/g, "").replace(/\s+/g, " ").trim() : "";
  if (!name || maxTitleBytes < 4) return ref;
  const limit = Math.min(180, maxTitleBytes);
  let shown;
  if (markdown) shown = inlineMarkdown(name, limit);
  else {
    shown = name;
    if (Buffer.byteLength(name) > limit) {
      shown = "";
      for (const char of name) {
        if (Buffer.byteLength(shown + char) > limit - 3) break;
        shown += char;
      }
      shown += "...";
    }
  }
  return `${ref}:${shown}`;
}

export function withReplyMode(content, mode, identifier = null, title = null, maxBytes = 1900) {
  if (mode !== "chat" && mode !== "issue") throw new Error("unknown reply mode");
  // Only use a server-provided task identifier, never derive it from reply text.
  // Ambiguous routing notices deliberately have no selected task/header suffix.
  let ref = mode === "issue" ? taskReplyReference(identifier) : "";
  const bare = `**${mode === "issue" ? "[任务模式]" : "[聊天模式]"}${ref ? ` ${ref}` : ""}**\n\n${content}`;
  // Legacy delivery chunks/digests cannot be re-split during an upgrade.
  // Fit the display-only title into the spare bytes without touching the body.
  if (ref) ref = taskReplyReference(identifier, title, { maxTitleBytes: maxBytes - Buffer.byteLength(bare) - 1 });
  return `**${mode === "issue" ? "[任务模式]" : "[聊天模式]"}${ref ? ` ${ref}` : ""}**\n\n${content}`;
}

export function replyUrl(multica, { binding = null, commentId = null, sessionId = null } = {}, webUrl = multica.issueWebUrl || multica.webUrl) {
  const base = new URL(webUrl);
  if (!["http:", "https:"].includes(base.protocol) || base.username || base.password) throw new Error("invalid reply link URL");
  const slug = binding?.workspace_slug || multica.workspaceSlug;
  let path = slug ? `/${encodeURIComponent(slug)}/chat` : "";
  if (binding) {
    const ref = binding.identifier || binding.issue_id || binding.id;
    if (!ref || !slug) throw new Error("issue link metadata is missing");
    path = `/${encodeURIComponent(slug)}/issues/${encodeURIComponent(ref)}`;
  } else if (sessionId && !slug) throw new Error("chat link metadata is missing");
  if (commentId && !/^[A-Za-z0-9_-]+$/.test(commentId)) throw new Error("invalid reply comment ID");
  base.pathname = base.pathname.replace(/\/$/, "") + path;
  base.search = !binding && sessionId ? `?session=${encodeURIComponent(sessionId)}` : "";
  base.hash = binding && commentId ? `comment-${commentId}` : "";
  return base.href;
}

// Budget the complete IM message, including a pinned leader attribution. The
// original platform content is never modified or used as routing metadata.
// Application budget, not a claim about the provider's character limit.
// Keep legacy checkpoint splitting unchanged; only new summaries use this cap.
export const MAX_REPLY_LENGTH = 8192;

export function formatReply(content, { mode = "chat", identifier = null, title = null, label = "", url = null, maxLength = MAX_REPLY_LENGTH, maxBytes } = {}) {
  // Group API counts UTF-16 code units. Explicit byte budgets remain available
  // for legacy callers; for-of truncation never splits a surrogate pair.
  const limit = maxBytes ?? maxLength;
  const measure = maxBytes == null ? text => text.length : text => Buffer.byteLength(text);
  const target = url?.replace(/[()]/g, c => c === "(" ? "%28" : "%29");
  const footer = target ? `\n\n**[完整对话链接](${target})**` : "";
  const hint = target ? "\n\n...（回复较长，全文请在下方链接查看。）" : "\n\n...（内容较长，后续已省略。）";
  const prefix = withReplyMode(label, mode, identifier, title, limit - measure(hint + footer) - 4);
  const text = String(content);
  const closing = replyFenceSuffix(text);
  if (measure(prefix + text + closing + footer) <= limit) return prefix + text + closing + footer;
  const suffix = hint + footer;
  const budget = limit - measure(prefix + suffix);
  if (budget < 0) throw new Error("reply metadata exceeds message budget");
  if (markdownImages(text).length) {
    let available = budget, summary, fence;
    do {
      summary = trimImageMarkdown(text, available, measure);
      fence = replyFenceSuffix(summary);
      available -= Math.max(1, measure(summary + fence) - budget);
    } while (measure(summary + fence) > budget && available >= 0);
    return prefix + summary + fence + suffix;
  }
  const chars = [];
  let used = 0;
  for (const char of text) {
    const size = measure(char);
    if (used + size > budget) break;
    chars.push(char); used += size;
  }
  let summary = chars.join(""), fence = replyFenceSuffix(summary);
  while (used + measure(fence) > budget && chars.length) {
    used -= measure(chars.pop());
    summary = chars.join(""); fence = replyFenceSuffix(summary);
  }
  return prefix + summary + fence + suffix;
}

export function replyFenceSuffix(text) {
  let fence = "", indent = "";
  for (const [, spaces, marker, rest] of text.matchAll(/^( {0,3})(`{3,}|~{3,})([^\n]*)$/gm)) {
    if (!fence) {
      if (marker[0] !== "`" || !rest.includes("`")) { fence = marker; indent = spaces; }
    } else if (marker[0] === fence[0] && marker.length >= fence.length && !rest.trim()) fence = "";
  }
  return fence ? "\n" + indent + fence : "";
}

export function noticeReplyMode(result) {
  if (result.conversation_mode === "chat" || result.conversation_mode === "issue") {
    return result.conversation_mode;
  }
  // Older service responses have no presentation metadata. A retained issue
  // link alone does not determine mode after unbind on updated servers.
  return result.binding ? "issue" : "chat";
}
