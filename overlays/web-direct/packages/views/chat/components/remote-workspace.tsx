"use client";

import { createContext, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import type { WorkspaceContext, WorkspaceReference, WorkspaceResult, WorkspaceEntry } from "@multica/core/types/remote-workspace";
import { Button } from "@multica/ui/components/ui/button";
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuTrigger } from "@multica/ui/components/ui/context-menu";
import { ChevronRight, ChevronDown, FileCode, Folder, FolderOpen, RefreshCw, X, Plus } from "lucide-react";
import { toast } from "sonner";
import "./remote-workspace.css";
import { StaticCodeBody } from "../../rich-content/rich-code-block";
import { appendReference, referenceKey, selectionLines, useRemoteWorkspaceDrafts, workspaceError } from "./remote-workspace-state";

interface WorkspaceUI {
  draft?: WorkspaceContext;
  clearSent: (draft: WorkspaceContext) => void;
  remove: (ref: WorkspaceReference) => void;
  locate: (ref: WorkspaceReference) => void;
}
const WorkspaceUIContext = createContext<WorkspaceUI | null>(null);
export const useRemoteWorkspace = () => useContext(WorkspaceUIContext);

export function RemoteWorkspaceChips() {
  const ui = useRemoteWorkspace();
  if (!ui?.draft?.references.length) return null;
  return <div className="flex flex-wrap gap-1 px-3 pt-2" aria-label="文件上下文 / File context">
    {ui.draft.references.map((ref) => <span key={referenceKey(ref)} className="inline-flex max-w-full items-center rounded border bg-surface-raised text-xs">
      <button type="button" className="truncate px-2 py-1" onClick={() => ui.locate(ref)} title={ref.path}>
        {ref.kind === "directory" ? "📁 " : "📄 "}{ref.path}{ref.start_line ? `:${ref.start_line}–${ref.end_line}` : ""}
      </button>
      <button type="button" className="p-1" aria-label={`移除引用 / Remove ${ref.path}`} onClick={() => ui.remove(ref)}><X size={12}/></button>
    </span>)}
  </div>;
}

interface WorkspaceLayoutProps {
  onOpenChange?: (open: boolean) => void;
  onToggleSessions?: () => void;
  sessionsHidden?: boolean;
}
export function RemoteWorkspace({ sessionId, children, ...layout }: { sessionId: string | null; children: ReactNode } & WorkspaceLayoutProps) {
  const account = useAuthStore((s) => s.user?.id ?? "");
  const workspace = useWorkspaceId();
  const scope = JSON.stringify([account, workspace, sessionId]);
  // Keyed boundary prevents requests, selection and open files leaking across chats.
  return <WorkspaceSession key={scope} sessionId={sessionId} scope={scope} {...layout}>{children}</WorkspaceSession>;
}
function WorkspaceSession({ sessionId, scope, children, onOpenChange, onToggleSessions, sessionsHidden }: { sessionId: string | null; scope: string; children: ReactNode } & WorkspaceLayoutProps) {
  const [open, setOpen] = useState(false);
  useEffect(() => { onOpenChange?.(open); }, [open, onOpenChange]);
  const [selected, setSelected] = useState("");
  const [tabs, setTabs] = useState<string[]>([]);
  const [compactView, setCompactView] = useState<"code" | "chat">("code");
  const openFile = (path: string) => {
    setSelected(path); setTabs((items) => items.includes(path) ? items : [...items, path]);
    setCompactView("code");
  };
  const closeFile = (path: string) => {
    const remaining = tabs.filter((item) => item !== path); setTabs(remaining);
    if (selected === path) setSelected(remaining.at(-1) ?? "");
  };
  const [pathInput, setPathInput] = useState("");
  const [selection, setSelection] = useState<{start_line: number; end_line: number} | null>(null);
  const codeRef = useRef<HTMLDivElement>(null);
  const qc = useQueryClient();
  const queryPrefix = useMemo(() => ["remote-workspace", scope], [scope]);
  const draft = useRemoteWorkspaceDrafts((s) => s.drafts[scope]);
  const updateDraft = useRemoteWorkspaceDrafts((s) => s.set);
  const clearSent = useRemoteWorkspaceDrafts((s) => s.clearSent);
  const root = useQuery({
    queryKey: [...queryPrefix, "root"],
    queryFn: ({ signal }) => api.chatWorkspace(sessionId!, { operation: "list", path: "." }, signal),
    enabled: open && !!sessionId,
    retry: false, refetchInterval: open ? 5000 : false, gcTime: 0,
  });
  const binding = root.data?.binding;
  const file = useQuery({
    queryKey: [...queryPrefix, binding, "file", selected],
    queryFn: ({ signal }) => api.chatWorkspace(sessionId!, { operation: "read", binding, path: selected }, signal),
    enabled: open && !!sessionId && !!binding && !!selected && !root.error,
    retry: false, gcTime: 0,
    refetchInterval: open ? 5000 : false,
  });
  useEffect(() => { setSelection(null); }, [selected, file.data?.revision, binding]);
  // Never retain source code in the query cache after leaving this chat.
  useEffect(() => () => { void qc.cancelQueries({ queryKey: queryPrefix }); qc.removeQueries({ queryKey: queryPrefix }); }, [qc, queryPrefix]);
  const add = (ref: WorkspaceReference) => {
    if (!binding || root.error) return;
    try { updateDraft(scope, appendReference(useRemoteWorkspaceDrafts.getState().drafts[scope], binding, ref)); }
    catch (error) { toast.error(error instanceof Error ? error.message : String(error)); }
  };
  const context: WorkspaceUI = {
    draft,
    clearSent: (sent) => clearSent(scope, sent),
    remove: (ref) => {
      const current = useRemoteWorkspaceDrafts.getState().drafts[scope];
      if (current) updateDraft(scope, { ...current, references: current.references.filter((item) => referenceKey(item) !== referenceKey(ref)) });
    },
    locate: (ref) => { setOpen(true); if (ref.kind === "file") openFile(ref.path); else setCompactView("code"); },
  };
  const refresh = () => { void qc.invalidateQueries({ queryKey: queryPrefix }); };
  const openedRef: WorkspaceReference = { path: selected, kind: "file", revision: file.data?.revision };
  return <WorkspaceUIContext.Provider value={context}>
    <div className="remote-workspace flex min-h-0 min-w-0 flex-1 flex-col">
      <div className="flex shrink-0 items-center gap-2 border-b px-3 py-1">
        {open && onToggleSessions && <Button type="button" size="sm" variant="ghost" onClick={onToggleSessions} aria-expanded={!sessionsHidden}>{sessionsHidden ? "显示会话列表" : "收起会话列表"}</Button>}
        <Button type="button" size="sm" variant="ghost" onClick={() => setOpen(!open)} aria-expanded={open}><FolderOpen size={14}/> 文件 / Files</Button>
        {open && <><span className="truncate text-xs text-muted-foreground">{root.data?.root_name ?? "远程工作区 / Remote workspace"}</span><Button type="button" size="sm" variant="ghost" onClick={refresh} aria-label="刷新文件 / Refresh files"><RefreshCw size={14}/></Button></>}
      </div>
      {open && <div className="workspace-view-switch" role="group" aria-label="工作区视图 / Workspace view">
        <Button size="sm" variant={compactView === "code" ? "secondary" : "ghost"} aria-pressed={compactView === "code"} onClick={() => setCompactView("code")}>代码 / Code</Button>
        <Button size="sm" variant={compactView === "chat" ? "secondary" : "ghost"} aria-pressed={compactView === "chat"} onClick={() => setCompactView("chat")}>聊天 / Chat{draft?.references.length ? ` (${draft.references.length})` : ""}</Button>
      </div>}
      <div className={`workspace-panes ${open ? "workspace-panes-open" : ""}`} data-view={compactView}>
        {open && <>
          <aside aria-label="目录树 / File tree" className="workspace-explorer border-r bg-surface">
            <div className="flex h-9 shrink-0 items-center justify-between border-b px-3 text-xs font-medium"><span>资源管理器</span><span className="text-muted-foreground">只读</span></div>
            <form className="flex shrink-0 gap-1 border-b p-2" onSubmit={(event) => { event.preventDefault(); if (pathInput.trim()) openFile(pathInput.trim()); }}>
              <input className="min-w-0 flex-1 rounded border bg-transparent px-2 text-xs" aria-label="相对文件路径 / Relative file path" placeholder="src/index.ts" value={pathInput} onChange={(event) => setPathInput(event.target.value)}/>
              <button type="submit" className="text-xs" aria-label="打开 / Open"><ChevronRight size={14}/></button>
            </form>
            <div className="min-h-0 flex-1 overflow-auto p-1">
              {!sessionId ? <p className="p-3 text-xs">先创建聊天并运行一轮，即可浏览 Agent 实际工作目录。 / Start a conversation and run a turn first.</p>
              : root.isPending ? <p role="status" className="p-3 text-xs">加载工作区… / Loading workspace…</p>
              : root.error ? <p role="alert" className="p-3 text-xs">{workspaceError(root.error)}</p>
              : root.data && <Directory sessionId={sessionId} prefix={queryPrefix} binding={root.data.binding} path="." initial={root.data} onOpen={openFile} onAdd={add}/>}
            </div>
          </aside>
          <section aria-label="代码编辑区 / Code editor" className="workspace-editor bg-surface">
            <div className="flex h-9 shrink-0 overflow-x-auto border-b" role="tablist" aria-label="打开的文件 / Open files">
              {tabs.map((path) => <div key={path} className={`flex shrink-0 items-center border-r ${selected === path ? "border-t-2 border-t-brand bg-background" : "bg-surface-raised"}`}>
                <button role="tab" aria-selected={selected === path} className="flex items-center gap-2 px-3 text-xs" title={path} onClick={() => openFile(path)}><FileCode size={13}/>{path.split("/").at(-1)}</button>
                <button className="px-2" aria-label={`关闭文件 / Close ${path}`} onClick={() => closeFile(path)}><X size={12}/></button>
              </div>)}
            </div>
            {selected ? <>
              <div className="flex flex-wrap items-center gap-1 border-b px-2 py-1 text-xs">
                <span className="min-w-0 flex-1 truncate" title={file.data?.absolute_path ?? selected}>{selected.split("/").join(" › ")}</span>
                <Button type="button" size="sm" variant="ghost" disabled={!file.data || !!file.error || !!root.error} onClick={() => add(openedRef)}><Plus size={12}/> 文件 / File</Button>
                <Button type="button" size="sm" variant="ghost" disabled={!selection || !file.data || !!file.error || !!root.error} onClick={() => { if (selection) add({ ...openedRef, ...selection }); }}>选区 / Selection</Button>
              </div>
              {root.error ? <p role="alert" className="p-3 text-sm">{workspaceError(root.error)}</p> : file.isPending ? <p role="status" className="p-3">读取文件… / Reading file…</p> : file.error ? <p role="alert" className="p-3 text-sm">{workspaceError(file.error)}</p> : file.data && <ContextMenu>
                <ContextMenuTrigger className="min-h-0 flex-1 overflow-auto select-text!">
                  <div className="flex font-mono text-xs leading-5" onMouseUp={() => setSelection(codeRef.current ? selectionLines(codeRef.current, window.getSelection()) : null)} onKeyUp={() => setSelection(codeRef.current ? selectionLines(codeRef.current, window.getSelection()) : null)}>
                    <pre aria-hidden className="sticky left-0 select-none border-r bg-surface px-2 text-right text-muted-foreground">{(file.data.content ?? "").split("\n").map((_, i) => i + 1).join("\n")}</pre>
                    <div ref={codeRef} className="min-w-0 px-3" tabIndex={0} aria-label="只读文件预览 / Read-only file preview"><pre><StaticCodeBody body={file.data.content ?? ""} language={languageFor(selected)}/></pre></div>
                  </div>
                </ContextMenuTrigger>
                <ContextMenuContent>
                  <ContextMenuItem onClick={() => add(openedRef)}>添加文件到聊天 / Add file to chat</ContextMenuItem>
                  <ContextMenuItem disabled={!selection} onClick={() => { if (selection) add({ ...openedRef, ...selection }); }}>添加选区 / Add selection</ContextMenuItem>
                  <PathMenu absolutePath={file.data.absolute_path} relativePath={selected}/>
                </ContextMenuContent>
              </ContextMenu>}
            </> : <div className="flex flex-1 flex-col items-center justify-center gap-3 p-6 text-center text-sm text-muted-foreground"><FileCode size={40}/><p>从左侧打开文件</p><p className="text-xs">右键文件、文件夹或代码选区，添加到右侧 Agent 聊天。</p></div>}
          </section>
        </>}
        <section aria-label="Agent 聊天 / Agent chat" className="workspace-chat border-l">
          {open && <div className="flex h-9 shrink-0 items-center border-b px-3 text-xs font-medium">Agent · 当前会话</div>}
          {draft && draft.references.length > 0 && binding && draft.binding !== binding && <p role="alert" className="p-2 text-xs text-destructive">工作目录已切换，发送前请移除旧引用并重新添加。 / Workspace changed; reattach references.</p>}
          {children}
        </section>
      </div>
    </div>
  </WorkspaceUIContext.Provider>;
}
function Directory({ sessionId, prefix, binding, path, initial, onOpen, onAdd }: { sessionId: string; prefix: string[]; binding: string; path: string; initial?: WorkspaceResult; onOpen: (path: string) => void; onAdd: (ref: WorkspaceReference) => void }) {
  const query = useInfiniteQuery({
    queryKey: [...prefix, binding, "directory", path],
    queryFn: ({ pageParam, signal }) => api.chatWorkspace(sessionId, { operation: "list", binding, path, offset: pageParam }, signal),
    initialPageParam: 0,
    getNextPageParam: (page) => page.next_offset,
    initialData: initial ? { pages: [initial], pageParams: [0] } : undefined,
    retry: false, gcTime: 0, refetchInterval: 5000,
  });
  if (query.error) return <p role="alert" className="p-2 text-xs">{workspaceError(query.error)}</p>;
  if (query.isPending) return <p role="status" className="p-2 text-xs">加载… / Loading…</p>;
  const entries = query.data.pages.flatMap((page) => page.entries ?? []);
  return <div role="group">
    {entries.length === 0 && <p className="p-2 text-xs text-muted-foreground">空目录 / Empty directory</p>}
    {entries.map((entry) => <TreeEntry key={entry.path} entry={entry} sessionId={sessionId} prefix={prefix} binding={binding} onOpen={onOpen} onAdd={onAdd}/>)}
    {query.hasNextPage && <button type="button" className="p-2 text-xs" disabled={query.isFetchingNextPage} onClick={() => void query.fetchNextPage()}>加载更多 / Load more</button>}
  </div>;
}
function TreeEntry({ entry, ...props }: { entry: WorkspaceEntry; sessionId: string; prefix: string[]; binding: string; onOpen: (path: string) => void; onAdd: (ref: WorkspaceReference) => void }) {
  const [expanded, setExpanded] = useState(false);
  const directory = entry.kind === "directory";
  return <div>
    <ContextMenu><ContextMenuTrigger className="block">
      <div className="group flex items-center rounded hover:bg-surface-raised">
        <button type="button" className="flex min-w-0 flex-1 items-center gap-1 px-1 py-1 text-left text-xs" aria-expanded={directory ? expanded : undefined} onClick={() => directory ? setExpanded(!expanded) : props.onOpen(entry.path)}>
          {directory ? expanded ? <ChevronDown size={12}/> : <ChevronRight size={12}/> : <span className="w-3"/>}{directory ? <Folder size={13}/> : <FileCode size={13}/>}<span className="truncate">{entry.name}</span>
        </button>
        <button type="button" className="p-1" aria-label={`添加到聊天 / Add ${entry.path}`} onClick={() => props.onAdd({ path: entry.path, kind: entry.kind })}><Plus size={12}/></button>
      </div>
    </ContextMenuTrigger><ContextMenuContent><ContextMenuItem onClick={() => props.onAdd({ path: entry.path, kind: entry.kind })}>添加到聊天 / Add to chat</ContextMenuItem><PathMenu absolutePath={entry.absolute_path} relativePath={entry.path}/></ContextMenuContent></ContextMenu>
    {expanded && <div className="ml-3 border-l pl-1"><Directory {...props} path={entry.path}/></div>}
  </div>;
}
async function copyWorkspacePath(value: string) {
  try { await navigator.clipboard.writeText(value); toast.success("路径已复制"); }
  catch { toast.error("复制失败，请允许浏览器访问剪贴板后重试。"); }
}
function PathMenu({ absolutePath, relativePath }: { absolutePath?: string; relativePath: string }) {
  return <>
    <ContextMenuItem disabled={!absolutePath} onClick={() => { if (absolutePath) void copyWorkspacePath(absolutePath); }}>复制路径 / Copy path</ContextMenuItem>
    <ContextMenuItem onClick={() => void copyWorkspacePath(relativePath)}>复制相对路径 / Copy relative path</ContextMenuItem>
  </>;
}
function languageFor(path: string): string | undefined {
  const ext = path.split(".").pop() ?? "";
  return ({ ts: "typescript", tsx: "typescript", js: "javascript", jsx: "javascript", py: "python", go: "go", rs: "rust", sh: "bash", json: "json", md: "markdown", css: "css", html: "xml", yml: "yaml", yaml: "yaml" } as Record<string, string>)[ext];
}
