"use client";

import { useCallback, useEffect, useState } from "react";
import { Check, ChevronDown, ChevronRight, Loader2, X } from "lucide-react";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import type {
  AgentMemoryProposal,
  AgentMemoryResponse,
  AgentMemorySettings,
  AgentMemoryWriteback,
  UpdateAgentMemorySettingsRequest,
} from "@multica/core/types/agent-memory";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Switch } from "@multica/ui/components/ui/switch";
import { useT } from "../../../i18n";

const MODES: AgentMemoryWriteback[] = ["off", "review", "auto"];

/**
 * Memory write-back (multica-team overlay): how the agent's own memory edits
 * come back after a run, and the queue of changes waiting for review. The
 * policy saves immediately; accepting or rejecting a change replaces the
 * stored memory, so it is blocked while the Memory tab has unsaved edits.
 */
export function MemoryWritebackPanel({
  agentId,
  settings,
  pendingCount,
  dirty,
  onServerResponse,
}: {
  agentId: string;
  settings: AgentMemorySettings;
  pendingCount: number;
  dirty: boolean;
  onServerResponse: (resp: AgentMemoryResponse, opts?: { settingsOnly?: boolean }) => void;
}) {
  const { t } = useT("agents");
  const [proposals, setProposals] = useState<AgentMemoryProposal[]>([]);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [savingSettings, setSavingSettings] = useState(false);
  const [expandedId, setExpandedId] = useState<string | null>(null);

  const loadProposals = useCallback(async () => {
    if (pendingCount === 0) {
      setProposals([]);
      return;
    }
    try {
      const resp = await api.listAgentMemoryProposals(agentId, "pending");
      setProposals(resp.proposals ?? []);
    } catch (err) {
      toast.error(err instanceof Error && err.message ? err.message : t(($) => $.tab_body.memory.writeback.load_failed));
    }
  }, [agentId, pendingCount, t]);

  useEffect(() => {
    void loadProposals();
  }, [loadProposals]);

  const updateSettings = async (patch: UpdateAgentMemorySettingsRequest) => {
    setSavingSettings(true);
    try {
      const resp = await api.updateAgentMemorySettings(agentId, patch);
      onServerResponse(resp, { settingsOnly: true });
      toast.success(t(($) => $.tab_body.memory.writeback.settings_saved));
    } catch (err) {
      toast.error(err instanceof Error && err.message ? err.message : t(($) => $.tab_body.memory.writeback.settings_failed));
    } finally {
      setSavingSettings(false);
    }
  };

  const resolve = async (proposal: AgentMemoryProposal, accept: boolean) => {
    setBusyId(proposal.id);
    try {
      const resp = accept
        ? await api.acceptAgentMemoryProposal(agentId, proposal.id)
        : await api.rejectAgentMemoryProposal(agentId, proposal.id);
      onServerResponse(resp);
      toast.success(
        accept
          ? t(($) => $.tab_body.memory.writeback.accepted_toast, { path: proposal.path })
          : t(($) => $.tab_body.memory.writeback.rejected_toast, { path: proposal.path }),
      );
    } catch (err) {
      toast.error(err instanceof Error && err.message ? err.message : t(($) => $.tab_body.memory.writeback.resolve_failed));
    } finally {
      setBusyId(null);
    }
  };

  const modeLabel = (m: AgentMemoryWriteback) =>
    m === "off"
      ? t(($) => $.tab_body.memory.writeback.mode_off)
      : m === "auto"
        ? t(($) => $.tab_body.memory.writeback.mode_auto)
        : t(($) => $.tab_body.memory.writeback.mode_review);
  const modeHint =
    settings.writeback === "off"
      ? t(($) => $.tab_body.memory.writeback.hint_off)
      : settings.writeback === "auto"
        ? t(($) => $.tab_body.memory.writeback.hint_auto)
        : t(($) => $.tab_body.memory.writeback.hint_review);
  const noteLabel = (note: string) =>
    note.includes("changed after")
      ? t(($) => $.tab_body.memory.writeback.note_conflict)
      : note.includes("syncs from its source")
        ? t(($) => $.tab_body.memory.writeback.note_sync)
        : note;

  return (
    <section aria-labelledby="memory-writeback-title" className="space-y-2 rounded-lg border p-3">
      <div className="flex flex-wrap items-center gap-2">
        <h3 id="memory-writeback-title" className="text-body font-medium">
          {t(($) => $.tab_body.memory.writeback.title)}
        </h3>
        <div role="group" aria-label={t(($) => $.tab_body.memory.writeback.mode_label)} className="ml-auto flex gap-1">
          {MODES.map((m) => (
            <Button
              key={m}
              type="button"
              size="xs"
              variant={settings.writeback === m ? "secondary" : "ghost"}
              aria-pressed={settings.writeback === m}
              disabled={savingSettings}
              onClick={() => m !== settings.writeback && void updateSettings({ writeback: m })}
            >
              {modeLabel(m)}
            </Button>
          ))}
        </div>
      </div>
      <p className="text-caption text-muted-foreground">{modeHint}</p>
      {settings.writeback !== "off" && (
        <label className="flex items-center gap-2 text-caption">
          <Switch
            size="sm"
            checked={settings.capture_runtime_memory}
            disabled={savingSettings}
            onCheckedChange={(checked) => void updateSettings({ capture_runtime_memory: checked })}
          />
          {t(($) => $.tab_body.memory.writeback.capture_label)}
        </label>
      )}

      {pendingCount > 0 && (
        <div className="space-y-1.5 border-t pt-2">
          <p className="text-caption font-medium">{t(($) => $.tab_body.memory.writeback.pending_title, { n: pendingCount })}</p>
          {dirty && <p className="text-micro text-muted-foreground">{t(($) => $.tab_body.memory.writeback.dirty_hint)}</p>}
          <ul className="space-y-1.5">
            {proposals.map((p) => {
              const open = expandedId === p.id;
              return (
                <li key={p.id} className="rounded-md border">
                  <div className="flex items-center gap-2 px-2 py-1.5">
                    <button
                      type="button"
                      aria-expanded={open}
                      onClick={() => setExpandedId(open ? null : p.id)}
                      className="flex min-w-0 flex-1 items-center gap-1.5 rounded text-left text-caption focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                    >
                      {open ? (
                        <ChevronDown className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
                      ) : (
                        <ChevronRight className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
                      )}
                      <span className="min-w-0 truncate font-mono">{p.path}</span>
                      <Badge variant="outline" className="shrink-0 px-1 text-micro">
                        {p.action === "delete"
                          ? t(($) => $.tab_body.memory.writeback.action_delete)
                          : p.current_content === null
                            ? t(($) => $.tab_body.memory.writeback.action_create)
                            : t(($) => $.tab_body.memory.writeback.action_update)}
                      </Badge>
                      <Badge variant="outline" className="shrink-0 px-1 text-micro">
                        {p.origin === "runtime"
                          ? t(($) => $.tab_body.memory.writeback.origin_runtime)
                          : t(($) => $.tab_body.memory.writeback.origin_agent)}
                      </Badge>
                    </button>
                    <Button
                      type="button"
                      size="xs"
                      variant="outline"
                      disabled={dirty || busyId !== null}
                      aria-label={t(($) => $.tab_body.memory.writeback.accept_aria, { path: p.path })}
                      onClick={() => void resolve(p, true)}
                    >
                      {busyId === p.id ? (
                        <Loader2 className="h-3 w-3 animate-spin motion-reduce:animate-none" aria-hidden="true" />
                      ) : (
                        <Check className="h-3 w-3" aria-hidden="true" />
                      )}
                      {t(($) => $.tab_body.memory.writeback.accept)}
                    </Button>
                    <Button
                      type="button"
                      size="xs"
                      variant="ghost"
                      disabled={dirty || busyId !== null}
                      aria-label={t(($) => $.tab_body.memory.writeback.reject_aria, { path: p.path })}
                      onClick={() => void resolve(p, false)}
                    >
                      <X className="h-3 w-3" aria-hidden="true" />
                      {t(($) => $.tab_body.memory.writeback.reject)}
                    </Button>
                  </div>
                  {p.note && <p className="px-2 pb-1.5 text-micro text-muted-foreground">{noteLabel(p.note)}</p>}
                  {open && (
                    <div className="grid gap-2 border-t p-2 md:grid-cols-2">
                      <div className="min-w-0">
                        <p className="mb-1 text-micro text-muted-foreground">{t(($) => $.tab_body.memory.writeback.current)}</p>
                        <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-words rounded bg-muted p-2 font-mono text-micro">
                          {p.current_content ?? t(($) => $.tab_body.memory.writeback.no_file)}
                        </pre>
                      </div>
                      <div className="min-w-0">
                        <p className="mb-1 text-micro text-muted-foreground">{t(($) => $.tab_body.memory.writeback.proposed)}</p>
                        <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-words rounded bg-muted p-2 font-mono text-micro">
                          {p.action === "delete" ? t(($) => $.tab_body.memory.writeback.will_delete) : p.content}
                        </pre>
                      </div>
                    </div>
                  )}
                </li>
              );
            })}
          </ul>
        </div>
      )}
    </section>
  );
}
