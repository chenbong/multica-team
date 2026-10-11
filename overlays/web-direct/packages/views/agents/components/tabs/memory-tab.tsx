"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ChangeEvent } from "react";
import {
  Eye,
  FilePlus2,
  FileText,
  HardDrive,
  Loader2,
  Pencil,
  Save,
  Trash2,
  Upload,
} from "lucide-react";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import type { Agent, AgentRuntime } from "@multica/core/types";
import type { AgentMemoryLimits } from "@multica/core/types/agent-memory";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Switch } from "@multica/ui/components/ui/switch";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../../i18n";
import { FileViewer, type FileMode } from "../../../skills/components/file-viewer";
import {
  DEFAULT_MEMORY_LIMITS,
  MEMORY_UPLOAD_ACCEPT,
  byteLength,
  draftFromServer,
  draftSignature,
  draftToRequest,
  formatBytes,
  mergeMemoryFiles,
  newLocalId,
  readMemoryUploads,
  uniqueMemoryPath,
  validateMemoryDraft,
  type DraftMemoryFile,
  type MemoryDraftIssue,
} from "./agent-memory";
import { MemoryRuntimeImportDialog } from "./memory-runtime-import-dialog";

type LoadState = { phase: "loading" } | { phase: "error"; message: string } | { phase: "ready" };

/**
 * Agent memory (multica-team overlay): Markdown/text files delivered with
 * every run of this agent. Files can be typed by hand, uploaded from this
 * browser, or imported from one of the caller's runtime machines; all three
 * only edit the local draft until Save replaces the stored set. Each file can
 * be switched off: it stays in the set but is not delivered to runs.
 */
export function MemoryTab({
  agent,
  runtime,
  onDirtyChange,
}: {
  agent: Agent;
  runtime: AgentRuntime | null;
  onDirtyChange?: (dirty: boolean) => void;
}) {
  const { t } = useT("agents");
  const [load, setLoad] = useState<LoadState>({ phase: "loading" });
  const [limits, setLimits] = useState<AgentMemoryLimits>(DEFAULT_MEMORY_LIMITS);
  const [baseline, setBaseline] = useState<DraftMemoryFile[]>([]);
  const [draft, setDraft] = useState<DraftMemoryFile[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [mode, setMode] = useState<FileMode>("raw");
  const [focusRequest, setFocusRequest] = useState(false);
  const [saving, setSaving] = useState(false);
  const [importOpen, setImportOpen] = useState(false);
  const uploadRef = useRef<HTMLInputElement>(null);

  const fetchMemory = useCallback(async () => {
    setLoad({ phase: "loading" });
    try {
      const resp = await api.getAgentMemory(agent.id);
      const files = draftFromServer(resp.files);
      setLimits(resp.limits ?? DEFAULT_MEMORY_LIMITS);
      setBaseline(files);
      setDraft(files);
      setSelectedId(files[0]?.localId ?? null);
      setLoad({ phase: "ready" });
    } catch (err) {
      setLoad({
        phase: "error",
        message: err instanceof Error && err.message ? err.message : t(($) => $.tab_body.memory.load_failed),
      });
    }
  }, [agent.id, t]);

  useEffect(() => {
    void fetchMemory();
  }, [fetchMemory]);

  const dirty = useMemo(() => draftSignature(draft) !== draftSignature(baseline), [draft, baseline]);
  useEffect(() => {
    onDirtyChange?.(dirty);
  }, [dirty, onDirtyChange]);

  const issues = useMemo(() => validateMemoryDraft(draft, limits), [draft, limits]);
  const totalBytes = useMemo(() => draft.reduce((sum, f) => sum + byteLength(f.content), 0), [draft]);
  const enabledCount = draft.filter((f) => f.enabled).length;
  const selected = draft.find((f) => f.localId === selectedId) ?? null;
  const selectedIssues = issues.filter((i) => "localId" in i && i.localId === selected?.localId);

  const describeIssue = (issue: MemoryDraftIssue): string => {
    switch (issue.kind) {
      case "path":
        return t(($) => $.tab_body.memory.path_error[issue.error]);
      case "duplicate":
        return t(($) => $.tab_body.memory.issue_duplicate, { path: issue.path });
      case "folder_conflict":
        return t(($) => $.tab_body.memory.issue_folder_conflict, { path: issue.path });
      case "file_too_large":
        return t(($) => $.tab_body.memory.issue_file_too_large, {
          path: issue.path,
          size: formatBytes(limits.max_file_bytes),
        });
      case "too_many_files":
        return t(($) => $.tab_body.memory.issue_too_many_files, { max: limits.max_files });
      case "total_too_large":
        return t(($) => $.tab_body.memory.issue_total_too_large, {
          size: formatBytes(limits.max_total_bytes),
        });
    }
  };

  const updateSelected = (patch: Partial<DraftMemoryFile>) => {
    if (!selected) return;
    setDraft((prev) => prev.map((f) => (f.localId === selected.localId ? { ...f, ...patch } : f)));
  };

  const setEnabled = (localId: string, enabled: boolean) => {
    setDraft((prev) => prev.map((f) => (f.localId === localId ? { ...f, enabled } : f)));
  };

  const setAllEnabled = (enabled: boolean) => {
    setDraft((prev) => prev.map((f) => (f.enabled === enabled ? f : { ...f, enabled })));
  };

  const addManualFile = () => {
    const taken = new Set(draft.map((f) => f.path.toLowerCase()));
    const file: DraftMemoryFile = {
      localId: newLocalId(),
      path: uniqueMemoryPath(draft.length === 0 ? "MEMORY.md" : "notes.md", taken),
      content: "",
      source: "manual",
      enabled: true,
    };
    setDraft((prev) => [...prev, file]);
    setSelectedId(file.localId);
    setMode("raw");
    setFocusRequest(true);
  };

  const deleteSelected = () => {
    if (!selected) return;
    const index = draft.findIndex((f) => f.localId === selected.localId);
    const next = draft.filter((f) => f.localId !== selected.localId);
    setDraft(next);
    setSelectedId(next[Math.min(index, next.length - 1)]?.localId ?? null);
  };

  const addIncoming = (incoming: Omit<DraftMemoryFile, "localId">[], toastKey: "upload_added" | "import_added") => {
    if (incoming.length === 0) return;
    const merged = mergeMemoryFiles(draft, incoming);
    setDraft(merged.files);
    setSelectedId(merged.touched[0] ?? selectedId);
    toast.success(
      toastKey === "upload_added"
        ? t(($) => $.tab_body.memory.upload_added_toast, { n: incoming.length })
        : t(($) => $.tab_body.memory.import_added_toast, { n: incoming.length }),
    );
  };

  const handleUpload = async (event: ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(event.target.files ?? []);
    event.target.value = "";
    if (files.length === 0) return;
    const { accepted, rejected } = await readMemoryUploads(files, limits);
    for (const r of rejected) {
      toast.error(
        r.reason === "too_large"
          ? t(($) => $.tab_body.memory.upload_rejected_too_large, {
              name: r.name,
              size: formatBytes(limits.max_file_bytes),
            })
          : r.reason === "binary"
            ? t(($) => $.tab_body.memory.upload_rejected_binary, { name: r.name })
            : t(($) => $.tab_body.memory.upload_rejected_unreadable, { name: r.name }),
      );
    }
    addIncoming(accepted, "upload_added");
  };

  const handleSave = async () => {
    if (issues.length > 0) return;
    setSaving(true);
    const keepPath = selected?.path;
    try {
      const resp = await api.updateAgentMemory(agent.id, { files: draftToRequest(draft) });
      const files = draftFromServer(resp.files);
      setLimits(resp.limits ?? limits);
      setBaseline(files);
      setDraft(files);
      setSelectedId(files.find((f) => f.path === keepPath)?.localId ?? files[0]?.localId ?? null);
      toast.success(t(($) => $.tab_body.memory.saved_toast));
    } catch (err) {
      toast.error(
        err instanceof Error && err.message ? err.message : t(($) => $.tab_body.memory.save_failed_toast),
      );
    } finally {
      setSaving(false);
    }
  };

  const sourceLabel = (f: DraftMemoryFile) =>
    f.source === "upload"
      ? t(($) => $.tab_body.memory.source_upload)
      : f.source === "runtime"
        ? t(($) => $.tab_body.memory.source_runtime)
        : t(($) => $.tab_body.memory.source_manual);

  if (load.phase === "loading") {
    return (
      <p className="flex items-center gap-2 text-caption text-muted-foreground">
        <Loader2 className="h-3.5 w-3.5 animate-spin motion-reduce:animate-none" aria-hidden="true" />
        {t(($) => $.tab_body.memory.loading)}
      </p>
    );
  }
  if (load.phase === "error") {
    return (
      <div className="space-y-3">
        <p role="alert" className="text-caption text-destructive">
          {load.message}
        </p>
        <Button size="sm" variant="outline" onClick={() => void fetchMemory()}>
          {t(($) => $.tab_body.memory.retry)}
        </Button>
      </div>
    );
  }

  const globalIssues = issues.filter((i) => !("localId" in i));

  return (
    <div className="space-y-4">
      <p className="max-w-2xl text-pretty text-body leading-6 text-muted-foreground">
        {t(($) => $.tab_body.memory.intro)}
      </p>

      <div className="flex flex-wrap items-center gap-2">
        <Button type="button" size="sm" variant="outline" onClick={addManualFile}>
          <FilePlus2 className="h-3.5 w-3.5" aria-hidden="true" />
          {t(($) => $.tab_body.memory.new_file)}
        </Button>
        <Button type="button" size="sm" variant="outline" onClick={() => uploadRef.current?.click()}>
          <Upload className="h-3.5 w-3.5" aria-hidden="true" />
          {t(($) => $.tab_body.memory.upload)}
        </Button>
        <Button type="button" size="sm" variant="outline" onClick={() => setImportOpen(true)}>
          <HardDrive className="h-3.5 w-3.5" aria-hidden="true" />
          {t(($) => $.tab_body.memory.import_runtime)}
        </Button>
        <span className="ml-auto text-caption tabular-nums text-muted-foreground">
          {draft.length > 0 &&
            t(($) => $.tab_body.memory.enabled_usage, { enabled: enabledCount, n: draft.length }) + " · "}
          {t(($) => $.tab_body.memory.usage, {
            n: draft.length,
            max: limits.max_files,
            size: formatBytes(totalBytes),
            maxSize: formatBytes(limits.max_total_bytes),
          })}
        </span>
        <input
          ref={uploadRef}
          type="file"
          multiple
          accept={MEMORY_UPLOAD_ACCEPT}
          className="sr-only"
          tabIndex={-1}
          aria-hidden="true"
          data-testid="memory-upload-input"
          onChange={(e) => void handleUpload(e)}
        />
      </div>

      {globalIssues.length > 0 && (
        <ul role="alert" className="space-y-0.5 text-caption text-destructive">
          {globalIssues.map((issue) => (
            <li key={issue.kind}>{describeIssue(issue)}</li>
          ))}
        </ul>
      )}

      {draft.length === 0 ? (
        <div className="rounded-lg border border-dashed px-4 py-10 text-center">
          <p className="text-body font-medium">{t(($) => $.tab_body.memory.empty_title)}</p>
          <p className="mx-auto mt-1 max-w-md text-caption text-muted-foreground">
            {t(($) => $.tab_body.memory.empty_hint)}
          </p>
        </div>
      ) : (
        <div className="grid min-h-[28rem] overflow-hidden rounded-lg border md:grid-cols-[14rem_minmax(0,1fr)]">
          <div className="flex min-h-0 flex-col border-b md:border-b-0 md:border-r">
          <div className="flex items-center gap-1 border-b px-2 py-1 text-micro text-muted-foreground">
            <span className="flex-1">{t(($) => $.tab_body.memory.files_label)}</span>
            <Button
              type="button"
              size="xs"
              variant="ghost"
              disabled={enabledCount === draft.length}
              onClick={() => setAllEnabled(true)}
            >
              {t(($) => $.tab_body.memory.enable_all)}
            </Button>
            <Button
              type="button"
              size="xs"
              variant="ghost"
              disabled={enabledCount === 0}
              onClick={() => setAllEnabled(false)}
            >
              {t(($) => $.tab_body.memory.disable_all)}
            </Button>
          </div>
          <ul
            role="listbox"
            aria-label={t(($) => $.tab_body.memory.files_label)}
            className="max-h-72 space-y-0.5 overflow-y-auto p-1 md:max-h-none"
          >
            {draft.map((f) => {
              const active = f.localId === selected?.localId;
              const hasIssue = issues.some((i) => "localId" in i && i.localId === f.localId);
              return (
                <li key={f.localId} className="flex items-center gap-1.5 pl-1.5">
                  <Switch
                    size="sm"
                    checked={f.enabled}
                    onCheckedChange={(checked) => setEnabled(f.localId, checked)}
                    aria-label={t(($) => $.tab_body.memory.enable_aria, { path: f.path || "—" })}
                  />
                  <button
                    type="button"
                    role="option"
                    aria-selected={active}
                    onClick={() => setSelectedId(f.localId)}
                    className={cn(
                      "flex min-w-0 flex-1 items-center gap-2 rounded-md px-2 py-1.5 text-left text-caption transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                      active
                        ? "bg-surface-selected font-medium text-surface-selected-foreground"
                        : "text-muted-foreground hover:bg-surface-hover hover:text-foreground",
                      !f.enabled && "opacity-60",
                    )}
                  >
                    <FileText
                      className={cn("h-3.5 w-3.5 shrink-0", hasIssue && "text-destructive")}
                      aria-hidden="true"
                    />
                    <span className={cn("min-w-0 flex-1 truncate font-mono", !f.enabled && "line-through")}>
                      {f.path || "—"}
                    </span>
                    <Badge variant="outline" className="shrink-0 px-1 text-micro">
                      {sourceLabel(f)}
                    </Badge>
                  </button>
                </li>
              );
            })}
          </ul>
          </div>

          {selected ? (
            <div className="flex min-w-0 flex-col">
              <div className="flex items-center gap-2 border-b p-2">
                <Input
                  aria-label={t(($) => $.tab_body.memory.path_label)}
                  value={selected.path}
                  placeholder={t(($) => $.tab_body.memory.path_placeholder)}
                  onChange={(e) => updateSelected({ path: e.target.value })}
                  aria-invalid={selectedIssues.length > 0}
                  className="h-8 font-mono text-caption"
                  spellCheck={false}
                />
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  onClick={() => setMode((m) => (m === "preview" ? "raw" : "preview"))}
                >
                  {mode === "preview" ? (
                    <Pencil className="h-3.5 w-3.5" aria-hidden="true" />
                  ) : (
                    <Eye className="h-3.5 w-3.5" aria-hidden="true" />
                  )}
                  {mode === "preview" ? t(($) => $.tab_body.memory.edit) : t(($) => $.tab_body.memory.preview)}
                </Button>
                <Button
                  type="button"
                  size="icon-sm"
                  variant="ghost"
                  className="text-muted-foreground hover:text-destructive"
                  aria-label={t(($) => $.tab_body.memory.delete_aria, { path: selected.path })}
                  onClick={deleteSelected}
                >
                  <Trash2 className="h-3.5 w-3.5" aria-hidden="true" />
                </Button>
              </div>
              {(selected.sourcePath || selectedIssues.length > 0 || !selected.enabled) && (
                <div className="space-y-0.5 border-b px-3 py-1.5 text-micro">
                  {!selected.enabled && (
                    <p className="text-muted-foreground">{t(($) => $.tab_body.memory.disabled_hint)}</p>
                  )}
                  {selected.sourcePath && selected.source !== "manual" && (
                    <p className="truncate text-muted-foreground">
                      {selected.source === "runtime"
                        ? t(($) => $.tab_body.memory.source_detail_runtime, { path: selected.sourcePath })
                        : t(($) => $.tab_body.memory.source_detail_upload, { path: selected.sourcePath })}
                    </p>
                  )}
                  {selectedIssues.map((issue, i) => (
                    <p key={i} className="text-destructive">
                      {describeIssue(issue)}
                    </p>
                  ))}
                </div>
              )}
              <div className="min-h-[22rem] flex-1">
                <FileViewer
                  key={selected.localId}
                  path={selected.path}
                  content={selected.content}
                  mode={mode}
                  readOnly={false}
                  autoFocus={focusRequest}
                  onFocusHandled={() => setFocusRequest(false)}
                  onChange={(content) => updateSelected({ content })}
                />
              </div>
            </div>
          ) : null}
        </div>
      )}

      <div className="flex items-center justify-end gap-3">
        {dirty && (
          <span className="text-caption text-muted-foreground">
            {t(($) => $.tab_body.common.unsaved_changes)}
          </span>
        )}
        <Button size="sm" onClick={() => void handleSave()} disabled={!dirty || saving || issues.length > 0}>
          {saving ? (
            <Loader2 className="h-3.5 w-3.5 animate-spin motion-reduce:animate-none" aria-hidden="true" />
          ) : (
            <Save className="h-3.5 w-3.5" aria-hidden="true" />
          )}
          {t(($) => $.tab_body.common.save)}
        </Button>
      </div>

      {importOpen && (
        <MemoryRuntimeImportDialog
          agentRuntime={runtime}
          onClose={() => setImportOpen(false)}
          onImport={(files) => addIncoming(files, "import_added")}
        />
      )}
    </div>
  );
}
