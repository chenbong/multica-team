"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import type { Issue } from "@multica/core/types";
import { workspaceListOptions } from "@multica/core/workspace/queries";
import { useIssueTransferPreview, useTransferIssue } from "@multica/core/issues/transfer";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { useNavigation } from "../../navigation";
import { useT } from "../../i18n";

export function IssueTransferDialog({ issue, onClose }: { issue: Issue; onClose: () => void }) {
  const { t } = useT("issues");
  const navigation = useNavigation();
  const [target, setTarget] = useState("");
  const [status, setStatus] = useState("");
  const [confirmedFingerprint, setConfirmedFingerprint] = useState<string | null>(null);
  const [error, setError] = useState("");
  const workspaces = useQuery(workspaceListOptions());
  const preview = useIssueTransferPreview(issue.workspace_id, issue.id, target, status);
  const transfer = useTransferIssue(issue.id);
  const plan = preview.data;
  const busy = transfer.isPending;
  const ready = !!plan && !preview.isFetching && !preview.isError && plan.blockers.length === 0 &&
    (!plan.unassign || confirmedFingerprint === plan.fingerprint);
  const reset = () => { setConfirmedFingerprint(null); setError(""); };
  const blockers: Record<string, string> = {
    status_mapping_required: t(($) => $.transfer.block_status),
    project_or_parent: t(($) => $.transfer.block_project),
    custom_context: t(($) => $.transfer.block_context),
    active_or_linked_runs: t(($) => $.transfer.block_runs),
    children_or_dependencies: t(($) => $.transfer.block_children),
    attachments: t(($) => $.transfer.block_attachments),
    automation_or_sources: t(($) => $.transfer.block_automation),
    labels_pins_or_prs: t(($) => $.transfer.block_labels),
    notification_recipients: t(($) => $.transfer.block_recipients),
  };
  async function submit() {
    if (!ready || !plan || busy) return;
    setError("");
    try {
      const result = await transfer.mutateAsync({ target_workspace_id: target, target_status: plan.target_status,
        fingerprint: plan.fingerprint, confirm_unassign: plan.unassign });
      // Build a local path from validated fields, never navigate an arbitrary
      // server-provided URL. Route-driven workspace context updates on entry.
      if (!result.identifier) throw new Error(t(($) => $.transfer.invalid_result));
      navigation.push(`/${result.target_slug}/issues/${encodeURIComponent(result.identifier)}`);
      onClose();
    } catch (e) {
      setError(e instanceof Error ? e.message : t(($) => $.transfer.failed));
      setConfirmedFingerprint(null);
      void preview.refetch();
    }
  }
  return <Dialog open onOpenChange={(open) => { if (!open && !busy) onClose(); }}>
    <DialogContent className="max-w-lg">
      <DialogHeader>
        <DialogTitle>{t(($) => $.transfer.title)}</DialogTitle>
        <DialogDescription>{t(($) => $.transfer.warning)}</DialogDescription>
      </DialogHeader>
      <p className="text-caption">{issue.identifier} · {issue.title}</p>
      <label className="grid gap-2 text-caption">
        {t(($) => $.transfer.target)}
        <select className="rounded-md border bg-background p-2 text-foreground" value={target} disabled={busy || workspaces.isLoading}
          onChange={(e) => { setTarget(e.target.value); setStatus(""); reset(); }}>
          <option value="">{t(($) => $.transfer.choose)}</option>
          {workspaces.data?.filter((w) => w.id !== issue.workspace_id).map((w) => <option value={w.id} key={w.id}>{w.name}</option>)}
        </select>
      </label>
      {workspaces.isError && <p role="alert">{t(($) => $.transfer.failed)}</p>}
      {preview.isFetching && <p role="status">{t(($) => $.transfer.checking)}</p>}
      {preview.isError && <p role="alert">{preview.error.message}</p>}
      {plan && !preview.isError && <>
        <p className="text-caption">{t(($) => $.transfer.summary, { comments: plan.comments, runs: plan.runs, messages: plan.messages, prefix: plan.target_prefix })}</p>
        <label className="grid gap-2 text-caption">
          {t(($) => $.transfer.status)}
          <select className="rounded-md border bg-background p-2 text-foreground" value={plan.statuses.some((s) => s.key === (status || plan.target_status)) ? (status || plan.target_status) : ""}
            disabled={busy || preview.isFetching} onChange={(e) => { setStatus(e.target.value); reset(); }}>
            <option value="">{t(($) => $.transfer.choose_status)}</option>
            {plan.statuses.map((s) => <option key={s.key} value={s.key}>{s.name}</option>)}
          </select>
        </label>
        {plan.blockers.length > 0 && <ul role="alert" className="list-disc pl-5 text-caption">{plan.blockers.map((b) => <li key={b}>{blockers[b] || t(($) => $.transfer.block_unknown)}</li>)}</ul>}
        {plan.unassign && <label className="flex items-start gap-2 text-caption">
          <Checkbox checked={confirmedFingerprint === plan.fingerprint} disabled={busy || preview.isFetching}
            onCheckedChange={(value) => setConfirmedFingerprint(value ? plan.fingerprint : null)} />
          {t(($) => $.transfer.unassign)}
        </label>}
        {plan.retired_subscriptions > 0 && <p className="text-caption">{t(($) => $.transfer.subscriptions, { count: plan.retired_subscriptions })}</p>}
      </>}
      {error && <p role="alert">{error}</p>}
      <DialogFooter>
        <Button variant="outline" disabled={busy} onClick={onClose}>{t(($) => $.transfer.cancel)}</Button>
        <Button variant="outline" disabled={!target || busy || preview.isFetching} onClick={() => { reset(); void preview.refetch(); }}>{t(($) => $.transfer.refresh)}</Button>
        <Button disabled={!ready || busy} aria-busy={busy} onClick={() => void submit()}>{t(($) => $.transfer.confirm)}</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>;
}
