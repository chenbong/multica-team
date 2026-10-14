// IM-only presentation. Never apply this to prompts, comments, or stored task
// content. The caller supplies the authoritative route, not model-written text.
export function withReplyMode(content, mode, identifier = null) {
  if (mode !== "chat" && mode !== "issue") throw new Error("unknown reply mode");
  // Only use a server-provided task identifier, never derive it from reply text.
  // Ambiguous routing notices deliberately have no selected task/header suffix.
  const ref = mode === "issue" && identifier != null ? String(identifier) : "";
  if (ref && (!/^[A-Za-z0-9]{1,10}-\d+$/.test(ref) || ref.length > 100)) {
    throw new Error("invalid reply issue identifier");
  }
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
export function formatReply(content, { mode = "chat", identifier = null, label = "", url, maxBytes = 1800 }) {
  const prefix = withReplyMode(label, mode, identifier);
  const target = url.replace(/[()]/g, c => c === "(" ? "%28" : "%29");
  const footer = `\n\n**[完整对话链接](${target})**`;
  const text = String(content);
  const closing = replyFenceSuffix(text);
  if (Buffer.byteLength(prefix + text + closing + footer) <= maxBytes) return prefix + text + closing + footer;
  const suffix = "\n\n...（回复较长，全文请在下方链接查看。）" + footer;
  const budget = maxBytes - Buffer.byteLength(prefix + suffix);
  if (budget < 0) throw new Error("reply metadata exceeds byte budget");
  const chars = [];
  let bytes = 0;
  for (const char of text) {
    const size = Buffer.byteLength(char);
    if (bytes + size > budget) break;
    chars.push(char); bytes += size;
  }
  let summary = chars.join(""), fence = replyFenceSuffix(summary);
  while (bytes + Buffer.byteLength(fence) > budget && chars.length) {
    bytes -= Buffer.byteLength(chars.pop());
    summary = chars.join(""); fence = replyFenceSuffix(summary);
  }
  return prefix + summary + fence + suffix;
}

function replyFenceSuffix(text) {
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
