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
  return `${mode === "issue" ? "[任务模式]" : "[聊天模式]"}${ref ? ` ${ref}` : ""}\n\n${content}`;
}

export function noticeReplyMode(result) {
  if (result.conversation_mode === "chat" || result.conversation_mode === "issue") {
    return result.conversation_mode;
  }
  // Older service responses have no presentation metadata. A retained issue
  // link alone does not determine mode after unbind on updated servers.
  return result.binding ? "issue" : "chat";
}
