// IM-only presentation. Never apply this to prompts, comments, or stored task
// content. The caller supplies the authoritative route, not model-written text.
export function withReplyMode(content, mode) {
  if (mode !== "chat" && mode !== "issue") throw new Error("unknown reply mode");
  return `${mode === "issue" ? "[任务模式]" : "[聊天模式]"}\n\n${content}`;
}

export function noticeReplyMode(result) {
  if (result.conversation_mode === "chat" || result.conversation_mode === "issue") {
    return result.conversation_mode;
  }
  // Older service responses have no presentation metadata. A retained issue
  // link alone does not determine mode after unbind on updated servers.
  return result.binding ? "issue" : "chat";
}
