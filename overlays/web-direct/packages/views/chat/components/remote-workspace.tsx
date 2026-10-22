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

export function RemoteWorkspace({ sessionId, children }: { sessionId: string | null; children: ReactNode }) {
  const account = useAuthStore((s) => s.user?.id ?? "");
  const workspace = useWorkspaceId();
  const scope = JSON.stringify([account, workspace, sessionId]);
  // Keyed boundary prevents requests, selection and open files leaking across chats.
  return <WorkspaceSession key={scope} sessionId={sessionId} scope={scope}>{children}</WorkspaceSession>;
}
function WorkspaceSession({ sessionId, scope, children }: { sessionId: string | null; scope: string; children: ReactNode }) {
  const [open, setOpen] = useState(false);
  const [selected, setSelected] = useState("");
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
    locate: (ref) => { setOpen(true); if (ref.kind === "file") setSelected(ref.path); },
  };
  const refresh = () => { void qc.invalidateQueries({ queryKey: queryPrefix }); };
  const openedRef: WorkspaceReference = { path: selected, kind: "file", revision: file.data?.revision };
  return <WorkspaceUIContext.Provider value={context}>
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      <div className="flex shrink-0 items-center gap-2 border-b px-3 py-1">
        <Button type="button" size="sm" variant="ghost" onClick={() => setOpen(!open)} aria-expanded={open}><FolderOpen size={14}/> 文件 / Files</Button>
        {open && <><span className="truncate text-xs text-muted-foreground">{root.data?.root_name ?? "远程工作区 / Remote workspace"}</span><Button type="button" size="sm" variant="ghost" onClick={refresh} aria-label="刷新文件 / Refresh files"><RefreshCw size={14}/></Button></>}
      </div>
      <div className="flex min-h-0 flex-1 flex-col lg:flex-row">
        {open && <section aria-label="远程文件 / Remote files" className="flex max-h-[55vh] min-h-48 w-full shrink-0 flex-col overflow-auto border-b bg-surface lg:max-h-none lg:w-[48%] lg:min-w-72 lg:max-w-[75%] lg:resize-x lg:border-r lg:border-b-0">
          {!sessionId ? <p className="p-4 text-sm">先创建聊天并运行一轮，即可浏览 Agent 实际工作目录。 / Start a conversation and run a turn first.</p>
          : root.isPending ? <p role="status" className="p-3">加载工作区… / Loading workspace…</p>
          : root.error ? <p role="alert" className="p-3 text-sm">{workspaceError(root.error)}</p>
          : root.data && <>
            <form className="flex gap-1 border-b p-2" onSubmit={(event) => { event.preventDefault(); if (pathInput.trim()) setSelected(pathInput.trim()); }}>
              <input className="min-w-0 flex-1 rounded border bg-transparent px-2 text-xs" aria-label="相对文件路径 / Relative file path" placeholder="src/index.ts" value={pathInput} onChange={(event) => setPathInput(event.target.value)}/>
              <Button type="submit" size="sm" variant="ghost">打开 / Open</Button>
            </form>
            {draft && draft.references.length > 0 && draft.binding !== binding && <p role="alert" className="p-2 text-xs text-destructive">工作目录已切换，发送前请移除旧引用并重新添加。 / Workspace changed; reattach references.</p>}
            <div className="max-h-60 shrink-0 overflow-auto border-b p-1" aria-label="目录树 / File tree">
              <Directory sessionId={sessionId} prefix={queryPrefix} binding={root.data.binding} path="." initial={root.data} onOpen={setSelected} onAdd={add}/>
            </div>
            {selected ? <>
              <div className="flex flex-wrap items-center gap-1 border-b px-2 py-1 text-xs">
                <span className="min-w-0 flex-1 truncate" title={selected}>{selected}</span>
                <Button type="button" size="sm" variant="ghost" disabled={!file.data || !!file.error} onClick={() => add(openedRef)}><Plus size={12}/> 文件 / File</Button>
                <Button type="button" size="sm" variant="ghost" disabled={!selection || !file.data || !!file.error} onClick={() => { if (selection) add({ ...openedRef, ...selection }); }}>选区 / Selection</Button>
              </div>
              {file.isPending ? <p role="status" className="p-3">读取文件… / Reading file…</p> : file.error ? <p role="alert" className="p-3 text-sm">{workspaceError(file.error)}</p> : file.data && <ContextMenu>
                <ContextMenuTrigger className="min-h-0 flex-1 overflow-auto select-text!">
                  <div className="flex font-mono text-xs leading-5" onMouseUp={() => setSelection(codeRef.current ? selectionLines(codeRef.current, window.getSelection()) : null)} onKeyUp={() => setSelection(codeRef.current ? selectionLines(codeRef.current, window.getSelection()) : null)}>
                    <pre aria-hidden className="sticky left-0 select-none border-r bg-surface px-2 text-right text-muted-foreground">{(file.data.content ?? "").split("\n").map((_, i) => i + 1).join("\n")}</pre>
                    <div ref={codeRef} className="min-w-0 px-3" tabIndex={0} aria-label="只读文件预览 / Read-only file preview"><pre><StaticCodeBody body={file.data.content ?? ""} language={languageFor(selected)}/></pre></div>
                  </div>
                </ContextMenuTrigger>
                <ContextMenuContent><ContextMenuItem onClick={() => add(openedRef)}>添加文件到聊天 / Add file to chat</ContextMenuItem><ContextMenuItem disabled={!selection} onClick={() => { if (selection) add({ ...openedRef, ...selection }); }}>添加选区 / Add selection</ContextMenuItem></ContextMenuContent>
              </ContextMenu>}
            </> : <p className="p-4 text-xs text-muted-foreground">选择文件预览；右键文件或文件夹添加到聊天。敏感路径、符号链接和 node_modules 默认不可浏览。 / Select a file; right-click to attach.</p>}
          </>}
        </section>}
        <div className="flex min-h-0 min-w-0 flex-1 flex-col">{children}</div>
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
    </ContextMenuTrigger><ContextMenuContent><ContextMenuItem onClick={() => props.onAdd({ path: entry.path, kind: entry.kind })}>添加到聊天 / Add to chat</ContextMenuItem><ContextMenuItem onClick={() => void navigator.clipboard?.writeText(entry.path)}>复制相对路径 / Copy relative path</ContextMenuItem></ContextMenuContent></ContextMenu>
    {expanded && <div className="ml-3 border-l pl-1"><Directory {...props} path={entry.path}/></div>}
  </div>;
}
function languageFor(path: string): string | undefined {
  const ext = path.split(".").pop() ?? "";
  return ({ ts: "typescript", tsx: "typescript", js: "javascript", jsx: "javascript", py: "python", go: "go", rs: "rust", sh: "bash", json: "json", md: "markdown", css: "css", html: "xml", yml: "yaml", yaml: "yaml" } as Record<string, string>)[ext];
}
