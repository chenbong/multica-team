"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { HardDrive, Loader2, RefreshCw } from "lucide-react";
import type { AgentRuntime } from "@multica/core/types";
import type { RuntimeLocalMemorySummary } from "@multica/core/types/agent-memory";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { runtimeDisplayLabel, runtimeListOptions } from "@multica/core/runtimes";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Label } from "@multica/ui/components/ui/label";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { useT } from "../../../i18n";
import { ProviderLogo } from "../../../runtimes/components/provider-logo";
import {
  buildRuntimeMachines,
  runtimeRowLabel,
} from "../../../runtimes/components/runtime-machines";
import {
  formatBytes,
  listRuntimeLocalMemory,
  readRuntimeLocalMemory,
  runtimeMemoryToDraft,
  type DraftMemoryFile,
} from "./agent-memory";

const TOOL_LABELS: Record<string, string> = {
  claude: "Claude Code",
  codebuddy: "CodeBuddy",
  codex: "Codex",
  gemini: "Gemini CLI",
  qwen: "Qwen Code",
  opencode: "OpenCode",
  kiro: "Kiro",
  hermes: "Hermes",
};

type ListState =
  | { phase: "idle" }
  | { phase: "loading" }
  | { phase: "ready"; entries: RuntimeLocalMemorySummary[] }
  | { phase: "error"; message: string };

export function MemoryRuntimeImportDialog({
  agentRuntime,
  onClose,
  onImport,
}: {
  agentRuntime: AgentRuntime | null;
  onClose: () => void;
  onImport: (files: Omit<DraftMemoryFile, "localId">[]) => void;
}) {
  const { t } = useT("agents");
  const wsId = useWorkspaceId();
  const userId = useAuthStore((s) => s.user?.id ?? null);
  const { data: runtimes = [] } = useQuery(runtimeListOptions(wsId));

  // Reading files off a machine is owner-only on the server, so only the
  // caller's own local runtimes are offered.
  const localRuntimes = useMemo(
    () =>
      runtimes.filter(
        (r) => r.runtime_mode === "local" && userId != null && r.owner_id === userId,
      ),
    [runtimes, userId],
  );
  const machines = useMemo(
    () => buildRuntimeMachines(localRuntimes, { now: Date.now(), currentUserId: userId }),
    [localRuntimes, userId],
  );

  const [runtimeId, setRuntimeId] = useState("");
  useEffect(() => {
    setRuntimeId((prev) => {
      if (prev && localRuntimes.some((r) => r.id === prev)) return prev;
      const preferred = localRuntimes.find((r) => r.id === agentRuntime?.id);
      return preferred?.id ?? localRuntimes.find((r) => r.status === "online")?.id ?? localRuntimes[0]?.id ?? "";
    });
  }, [agentRuntime?.id, localRuntimes]);
  const runtime = localRuntimes.find((r) => r.id === runtimeId) ?? null;
  const online = runtime?.status === "online";

  const [list, setList] = useState<ListState>({ phase: "idle" });
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [importing, setImporting] = useState(false);
  const [readErrors, setReadErrors] = useState<{ key: string; error: string }[]>([]);
  const [reloadToken, setReloadToken] = useState(0);
  const abortRef = useRef<AbortController | null>(null);

  useEffect(() => {
    abortRef.current?.abort();
    setSelected(new Set());
    setReadErrors([]);
    if (!runtimeId || !online) {
      setList({ phase: "idle" });
      return;
    }
    const controller = new AbortController();
    abortRef.current = controller;
    setList({ phase: "loading" });
    listRuntimeLocalMemory(runtimeId, controller.signal)
      .then((entries) => {
        if (!controller.signal.aborted) setList({ phase: "ready", entries });
      })
      .catch((err: unknown) => {
        if (controller.signal.aborted) return;
        setList({
          phase: "error",
          message: err instanceof Error && err.message ? err.message : String(err),
        });
      });
    return () => controller.abort();
  }, [runtimeId, online, reloadToken]);

  const entries = useMemo(() => (list.phase === "ready" ? list.entries : []), [list]);
  const groups = useMemo(() => {
    const byTool = new Map<string, RuntimeLocalMemorySummary[]>();
    for (const e of entries) {
      const bucket = byTool.get(e.tool) ?? [];
      bucket.push(e);
      byTool.set(e.tool, bucket);
    }
    return [...byTool.entries()];
  }, [entries]);
  const selectable = entries.filter((e) => !e.too_large);

  const toggle = (key: string, checked: boolean) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (checked) next.add(key);
      else next.delete(key);
      return next;
    });
  };

  const handleImport = async () => {
    if (!runtimeId || selected.size === 0) return;
    setImporting(true);
    setReadErrors([]);
    try {
      const result = await readRuntimeLocalMemory(runtimeId, [...selected]);
      if (result.files.length > 0) {
        onImport(runtimeMemoryToDraft(runtimeId, result.files));
      }
      if (result.errors.length > 0) {
        setReadErrors(result.errors);
        setSelected(new Set(result.errors.map((e) => e.key)));
      } else {
        onClose();
      }
    } catch (err) {
      setReadErrors([
        { key: "", error: err instanceof Error && err.message ? err.message : String(err) },
      ]);
    } finally {
      setImporting(false);
    }
  };

  const scopeLabel = (scope: string) =>
    scope === "project"
      ? t(($) => $.tab_body.memory.runtime_import.scope_project)
      : scope === "memory"
        ? t(($) => $.tab_body.memory.runtime_import.scope_memory)
        : t(($) => $.tab_body.memory.runtime_import.scope_global);

  return (
    <Dialog open onOpenChange={(open) => !open && !importing && onClose()}>
      <DialogContent className="flex max-h-[85vh] flex-col gap-4 sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t(($) => $.tab_body.memory.runtime_import.title)}</DialogTitle>
          <DialogDescription>
            {t(($) => $.tab_body.memory.runtime_import.description)}
          </DialogDescription>
        </DialogHeader>

        {localRuntimes.length === 0 ? (
          <p className="rounded-md border border-dashed px-3 py-6 text-center text-caption text-muted-foreground">
            {t(($) => $.tab_body.memory.runtime_import.no_runtimes)}
          </p>
        ) : (
          <div className="space-y-1.5">
            <Label>{t(($) => $.tab_body.memory.runtime_import.runtime_label)}</Label>
            <div className="flex items-center gap-2">
              <Select
                items={localRuntimes.map((r) => ({ value: r.id, label: runtimeDisplayLabel(r) }))}
                value={runtimeId}
                onValueChange={(v) => v && setRuntimeId(v)}
              >
                <SelectTrigger className="w-full" disabled={importing}>
                  <SelectValue placeholder={t(($) => $.tab_body.memory.runtime_import.runtime_placeholder)}>
                    {runtime ? (
                      <>
                        <ProviderLogo provider={runtime.provider} className="h-4 w-4 shrink-0" />
                        <span className="truncate">{runtimeDisplayLabel(runtime)}</span>
                      </>
                    ) : null}
                  </SelectValue>
                </SelectTrigger>
                <SelectContent>
                  {machines.map((machine) => (
                    <SelectGroup key={machine.id}>
                      <SelectLabel>{machine.title}</SelectLabel>
                      {machine.runtimes.map((r) => (
                        <SelectItem key={r.id} value={r.id}>
                          <ProviderLogo provider={r.provider} className="h-4 w-4 shrink-0" />
                          <span className="truncate">{runtimeRowLabel(r, machine.title)}</span>
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  ))}
                </SelectContent>
              </Select>
              <Button
                type="button"
                variant="outline"
                size="icon-sm"
                aria-label={t(($) => $.tab_body.memory.runtime_import.refresh)}
                disabled={!online || list.phase === "loading" || importing}
                onClick={() => setReloadToken((n) => n + 1)}
              >
                <RefreshCw className="h-3.5 w-3.5" aria-hidden="true" />
              </Button>
            </div>
          </div>
        )}

        <div className="min-h-40 flex-1 overflow-y-auto rounded-md border">
          {runtime && !online && (
            <p className="px-3 py-6 text-center text-caption text-muted-foreground">
              {t(($) => $.tab_body.memory.runtime_import.offline)}
            </p>
          )}
          {list.phase === "loading" && (
            <p className="flex items-center justify-center gap-2 px-3 py-6 text-caption text-muted-foreground">
              <Loader2 className="h-3.5 w-3.5 animate-spin motion-reduce:animate-none" aria-hidden="true" />
              {t(($) => $.tab_body.memory.runtime_import.loading)}
            </p>
          )}
          {list.phase === "error" && (
            <p role="alert" className="px-3 py-6 text-center text-caption text-destructive">
              {t(($) => $.tab_body.memory.runtime_import.failed, { error: list.message })}
            </p>
          )}
          {list.phase === "ready" && entries.length === 0 && (
            <p className="px-3 py-6 text-center text-caption text-muted-foreground">
              {t(($) => $.tab_body.memory.runtime_import.empty)}
            </p>
          )}
          {list.phase === "ready" && entries.length > 0 && (
            <div className="divide-y">
              <div className="flex items-center justify-between px-3 py-2 text-caption text-muted-foreground">
                <span>
                  {t(($) => $.tab_body.memory.runtime_import.selected, {
                    n: selected.size,
                    total: selectable.length,
                  })}
                </span>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  disabled={importing}
                  onClick={() =>
                    setSelected(
                      selected.size === selectable.length
                        ? new Set()
                        : new Set(selectable.map((e) => e.key)),
                    )
                  }
                >
                  {selected.size === selectable.length
                    ? t(($) => $.tab_body.memory.runtime_import.clear)
                    : t(($) => $.tab_body.memory.runtime_import.select_all)}
                </Button>
              </div>
              {groups.map(([tool, items]) => (
                <fieldset key={tool} className="px-3 py-2">
                  <legend className="sr-only">{TOOL_LABELS[tool] ?? tool}</legend>
                  <p aria-hidden="true" className="pb-1 text-caption font-medium">
                    {TOOL_LABELS[tool] ?? tool}
                  </p>
                  <ul className="space-y-1">
                    {items.map((e) => {
                      const id = `memory-import-${e.key}`;
                      return (
                        <li key={e.key} className="flex items-start gap-2 rounded-md px-1 py-1 hover:bg-surface-hover">
                          <Checkbox
                            id={id}
                            checked={selected.has(e.key)}
                            disabled={e.too_large || importing}
                            onCheckedChange={(checked) => toggle(e.key, checked === true)}
                            className="mt-0.5"
                          />
                          <label htmlFor={id} className="min-w-0 flex-1 cursor-pointer">
                            <span className="block truncate font-mono text-caption">{e.source_path}</span>
                            <span className="block text-micro text-muted-foreground">
                              {scopeLabel(e.scope)}
                              {e.label ? ` · ${e.label}` : ""}
                              {` · ${formatBytes(e.size_bytes)}`}
                              {e.too_large ? ` · ${t(($) => $.tab_body.memory.runtime_import.too_large)}` : ""}
                            </span>
                          </label>
                        </li>
                      );
                    })}
                  </ul>
                </fieldset>
              ))}
            </div>
          )}
        </div>

        {readErrors.length > 0 && (
          <div role="alert" className="space-y-1 rounded-md border border-destructive/40 px-3 py-2 text-caption text-destructive">
            <p className="font-medium">{t(($) => $.tab_body.memory.runtime_import.read_errors)}</p>
            <ul className="space-y-0.5">
              {readErrors.map((e) => (
                <li key={e.key || e.error} className="break-all">
                  {e.key ? <span className="font-mono">{e.key}: </span> : null}
                  {e.error}
                </li>
              ))}
            </ul>
          </div>
        )}

        <DialogFooter>
          <Button type="button" variant="ghost" onClick={onClose} disabled={importing}>
            {t(($) => $.tab_body.memory.runtime_import.cancel)}
          </Button>
          <Button type="button" onClick={handleImport} disabled={importing || selected.size === 0}>
            {importing ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin motion-reduce:animate-none" aria-hidden="true" />
            ) : (
              <HardDrive className="h-3.5 w-3.5" aria-hidden="true" />
            )}
            {importing
              ? t(($) => $.tab_body.memory.runtime_import.importing)
              : t(($) => $.tab_body.memory.runtime_import.import_action, { n: selected.size })}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
