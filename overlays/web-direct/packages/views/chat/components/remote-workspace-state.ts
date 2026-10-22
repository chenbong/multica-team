import { create } from "zustand";
import type { WorkspaceContext, WorkspaceReference } from "@multica/core/types/remote-workspace";

export function referenceKey(ref: WorkspaceReference): string {
  return JSON.stringify([ref.path, ref.kind, ref.start_line ?? 0, ref.end_line ?? 0]);
}
export function appendReference(current: WorkspaceContext | undefined, binding: string, ref: WorkspaceReference): WorkspaceContext {
  if (current?.references.length && current.binding !== binding) throw new Error("工作目录已切换，请先移除旧引用 / Remove stale references before attaching from the new workspace");
  const references = current?.references ?? [];
  const next = references.filter((item) => referenceKey(item) !== referenceKey(ref));
  if (next.length >= 12) throw new Error("最多添加 12 个引用 / Maximum 12 references");
  return { binding, references: [...next, ref] };
}
// Deliberately memory-only: paths and code are never written to browser storage.
// Account, workspace and session form the key. No file contents are retained here.
export const useRemoteWorkspaceDrafts = create<{
  drafts: Record<string, WorkspaceContext | undefined>;
  set: (key: string, value: WorkspaceContext | undefined) => void;
  clearSent: (key: string, sent: WorkspaceContext) => void;
}>((set) => ({
  drafts: {},
  set: (key, value) => set((state) => ({ drafts: { ...state.drafts, [key]: value } })),
  clearSent: (key, sent) => set((state) => {
    // Preserve additions/removals made while the message was being sent.
    if (state.drafts[key] !== sent) return state;
    const drafts = { ...state.drafts }; delete drafts[key]; return { drafts };
  }),
}));
export function selectionLines(container: HTMLElement, selection: Selection | null): { start_line: number; end_line: number } | null {
  if (!selection || selection.isCollapsed || selection.rangeCount === 0) return null;
  const range = selection.getRangeAt(0);
  if (!container.contains(range.startContainer) || !container.contains(range.endContainer)) return null;
  const before = range.cloneRange(); before.selectNodeContents(container); before.setEnd(range.startContainer, range.startOffset);
  const start = before.toString().split("\n").length;
  const text = range.toString().replace(/\n$/, "");
  return { start_line: start, end_line: start + text.split("\n").length - 1 };
}
export function workspaceError(error: unknown): string {
  const code = error instanceof Error ? error.message : String(error);
  const messages: Record<string, string> = {
    workspace_not_ready: "工作区尚未准备好。请先让此会话运行一次；daemon 重启后也需要重新运行。 / Run a turn to prepare this workspace.",
    workspace_unavailable_or_upgrade_required: "机器离线、连接位于其他服务节点，或 daemon 需要升级。 / Reconnect or upgrade the daemon.",
    workspace_requires_private_owned_agent: "仅支持本人创建的会话及本人拥有的私有 Agent，防止共享聊天泄露代码。 / A private, owned agent and conversation are required.",
    workspace_requires_machine_owner: "只有机器所有者可以浏览文件。 / Machine owner access required.",
    workspace_changed: "工作目录已切换，请刷新并重新添加引用。 / Workspace changed; refresh and reattach.",
    file_changed: "文件内容已变化，请重新打开文件并添加引用。 / File changed; reopen and reattach.",
    file_too_large: "仅预览 128 KiB 以内的文本文件。 / File exceeds the 128 KiB preview limit.",
    not_text_file: "暂不支持二进制文件预览。 / Only UTF-8 text files are supported.",
    path_denied: "此路径受保护或包含符号链接。 / Protected paths and symbolic links are not accessible.",
    file_unavailable: "文件已删除或无法读取。 / File missing or unreadable.",
    directory_too_large: "目录超过 10,000 项，请打开更具体的路径。 / Directory too large.",
    context_too_large: "引用内容超过 256 KiB，请选择更小的代码片段。 / Context exceeds 256 KiB.",
    workspace_busy: "文件请求过多，请稍后重试。 / Workspace busy; retry shortly.",
    workspace_timeout: "读取超时，请检查远程机器连接。 / Request timed out.",
    workspace_ambiguous_connection: "同一运行时存在多个连接，请重启或断开多余的 daemon。 / Multiple daemon connections; reconnect the intended machine.",
    workspace_disconnected: "远程机器连接已断开。 / Machine disconnected.",
  };
  return messages[code] ?? `无法读取远程文件 / Remote workspace unavailable: ${code}`;
}
