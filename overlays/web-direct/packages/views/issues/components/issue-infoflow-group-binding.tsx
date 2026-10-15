"use client";

import { useEffect, useId, useRef, useState } from "react";
import { Loader2, MessageSquare } from "lucide-react";
import { useAuthStore } from "@multica/core/auth";
import {
  infoFlowGroupURL, infoFlowGroupLabel, isInfoFlowGroupBindingUnavailable, isInfoFlowGroupBindingWaiting,
  parseInfoFlowGroupTarget, useBindIssueInfoFlowGroup, useIssueInfoFlowGroupBinding,
  type InfoFlowGroupBindingStatus,
} from "@multica/core/issues/infoflow-group-binding";
import { Button, buttonVariants } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@multica/ui/components/ui/dialog";
import { useT } from "../../i18n";
import { IssueInfoFlowGroups } from "./issue-infoflow-groups";

function reservedGroup(status?: InfoFlowGroupBindingStatus): string | undefined {
  return status?.binding?.groupId || (status?.request && status.request.state !== "failed" ? status.request.groupId : undefined);
}

export function IssueInfoFlowGroupBinding({ workspaceId, issueId }: { workspaceId: string; issueId: string }) {
  const { t } = useT("issues");
  const userId = useAuthStore((state) => state.user?.id);
  const query = useIssueInfoFlowGroupBinding(workspaceId, issueId);
  const mutation = useBindIssueInfoFlowGroup(workspaceId, issueId);
  const [open, setOpen] = useState(false);
  const status = query.isError ? undefined : query.data;
  const canManage = status?.enabled === true && status.canManage === true;
  // A 404 from an older backend does not disable its existing read-only
  // destination. All other failures hide both actions and stale links.
  if (query.isError && isInfoFlowGroupBindingUnavailable(query.error) && !open) {
    return <IssueInfoFlowGroups workspaceId={workspaceId} issueId={issueId} />;
  }
  const active = status?.binding?.state === "active" ? status.binding : undefined;
  if (!open && !active && !canManage) return null;
  return <>
    {active && <a href={infoFlowGroupURL(active.groupId)} className={buttonVariants({ variant: "outline", size: "sm" })}
      title={infoFlowGroupLabel(active.groupId, active.name)}>
      <MessageSquare aria-hidden="true" />{t(($) => $.infoflow_groups.open)}
    </a>}
    <Dialog open={open} onOpenChange={(value) => { if (!mutation.isPending) setOpen(value); }}>
      {canManage && !active && <DialogTrigger render={<Button variant="outline" size="sm" />}>
        <MessageSquare aria-hidden="true" />{t(($) => $.infoflow_groups.bind)}
      </DialogTrigger>}
      {open && <BindingDialog key={`${userId}:${workspaceId}:${issueId}`} status={status} readError={query.isError} refreshing={query.isFetching}
        refresh={() => { void query.refetch(); }} mutation={mutation} canManage={canManage} />}
    </Dialog>
  </>;
}

function BindingDialog({ status, readError, refreshing, refresh, mutation, canManage }: {
  status?: InfoFlowGroupBindingStatus;
  readError: boolean;
  refreshing: boolean;
  refresh: () => void;
  mutation: ReturnType<typeof useBindIssueInfoFlowGroup>;
  canManage: boolean;
}) {
  const { t } = useT("issues");
  const inputId = useId();
  const reserved = reservedGroup(status);
  const [mode, setMode] = useState<"existing" | "manual">(!reserved && !status?.request && status?.groups.length ? "existing" : "manual");
  const [selected, setSelected] = useState("");
  const [input, setInput] = useState(reserved || status?.request?.groupId || "");
  const [error, setError] = useState("");
  const inFlight = useRef(false);
  const busy = mutation.isPending;
  const active = status?.binding?.state === "active";
  const waiting = isInfoFlowGroupBindingWaiting(status);
  const failed = status?.request?.state === "failed";
  const availableSelection = status?.groups.some((group) => group.groupId === selected) ? selected : "";
  const target = parseInfoFlowGroupTarget(reserved || (mode === "existing" ? availableSelection : input));
  const requestWaiting = !!status?.request && status.request.state !== "failed" && !active;
  const reason = status?.request?.reason || status?.binding?.reason || "";
  const reasonCopy: Record<string, string> = {
    approval_pending: t(($) => $.infoflow_groups.approval),
    invitation_failed: t(($) => $.infoflow_groups.invitation_failed),
    invitation_pending: t(($) => $.infoflow_groups.waiting),
    membership_unconfirmed: t(($) => $.infoflow_groups.membership),
    membership_check_failed: t(($) => $.infoflow_groups.membership),
    leader_not_in_group: t(($) => $.infoflow_groups.waiting),
    actor_not_in_group: t(($) => $.infoflow_groups.actor),
    inviter_not_in_group: t(($) => $.infoflow_groups.inviter),
    leader_robot_missing_or_ambiguous: t(($) => $.infoflow_groups.leader),
    leader_robot_unavailable: t(($) => $.infoflow_groups.unavailable),
  };
  const reasonLabel = Object.prototype.hasOwnProperty.call(reasonCopy, reason) ? reasonCopy[reason] : undefined;
  useEffect(() => {
    if (readError || !canManage) { setSelected(""); setInput(""); }
  }, [readError, canManage]);
  function switchMode(value: "existing" | "manual") {
    setMode(value); setSelected(""); setInput(""); setError("");
  }
  async function submit(event: React.FormEvent) {
    event.preventDefault();
    if (busy || inFlight.current || !canManage || readError || refreshing || requestWaiting || active) return;
    if (!target) { setError(t(($) => $.infoflow_groups.invalid)); return; }
    inFlight.current = true; setError("");
    try { await mutation.mutateAsync(target); }
    catch (cause) { setError(cause instanceof Error ? cause.message : t(($) => $.infoflow_groups.failed)); }
    finally { inFlight.current = false; }
  }
  return <DialogContent className="sm:max-w-lg" showCloseButton={!busy}>
    <form className="grid gap-4" onSubmit={(event) => void submit(event)}>
      <DialogHeader>
        <DialogTitle>{t(($) => $.infoflow_groups.bind)}</DialogTitle>
        <DialogDescription>{t(($) => $.infoflow_groups.warning)}</DialogDescription>
      </DialogHeader>
      {(readError || !canManage) && <p role="alert">{t(($) => $.infoflow_groups.read_failed)}</p>}
      {canManage && !readError && <>
        {reserved ? <div className="grid gap-1 text-caption">
          <p className="flex flex-wrap gap-2"><span>{t(($) => $.infoflow_groups.target)}</span><span className="break-words">{infoFlowGroupLabel(reserved, status?.binding?.name)}</span></p>
          {!active && <p className="text-muted-foreground">{t(($) => $.infoflow_groups.reserved)}</p>}
        </div> : <>
          <fieldset className="grid gap-3" disabled={busy}>
            <legend className="sr-only">{t(($) => $.infoflow_groups.bind)}</legend>
            <label className="flex items-center gap-2 text-body">
              <input type="radio" name="infoflow-target-mode" checked={mode === "existing"} onChange={() => switchMode("existing")} />
              {t(($) => $.infoflow_groups.existing)}
            </label>
            {mode === "existing" && <div className="grid gap-2 text-caption">
              <label htmlFor={`${inputId}-existing`}>{t(($) => $.infoflow_groups.choose)}</label>
              <select className="w-full min-w-0 rounded-lg border border-[color:var(--input)] bg-background p-2 text-body text-foreground"
                id={`${inputId}-existing`} value={availableSelection} onChange={(event) => { setSelected(event.target.value); setError(""); }}>
                <option value="">{t(($) => $.infoflow_groups.choose)}</option>
                {status?.groups.map((group) => <option key={group.groupId} value={group.groupId}>
                  {infoFlowGroupLabel(group.groupId, group.name)}
                </option>)}
              </select>
              {status?.groups.length === 0 && <span className="text-muted-foreground">{t(($) => $.infoflow_groups.empty)}</span>}
            </div>}
            <label className="flex items-center gap-2 text-body">
              <input type="radio" name="infoflow-target-mode" checked={mode === "manual"} onChange={() => switchMode("manual")} />
              {t(($) => $.infoflow_groups.manual)}
            </label>
            {mode === "manual" && <div className="grid gap-2 text-caption">
              <label htmlFor={`${inputId}-manual`}>{t(($) => $.infoflow_groups.target)}</label>
              <Input id={`${inputId}-manual`} type="text" inputMode="numeric" autoComplete="off" maxLength={64} value={input}
                placeholder={t(($) => $.infoflow_groups.placeholder)} aria-invalid={!!input && !target}
                aria-describedby={!!input && !target ? `${inputId}-invalid` : undefined}
                onChange={(event) => { setInput(event.target.value); setError(""); }} />
              {!!input && !target && <span id={`${inputId}-invalid`} role="alert">{t(($) => $.infoflow_groups.invalid)}</span>}
            </div>}
          </fieldset>
        </>}
        {active && <>
          <p role="status">{t(($) => $.infoflow_groups.success)}</p>
          <a href={infoFlowGroupURL(status.binding!.groupId)} className={buttonVariants({ variant: "outline" })}>
            <MessageSquare aria-hidden="true" />{t(($) => $.infoflow_groups.open)}
          </a>
        </>}
        {waiting && <p role="status" className="flex items-center gap-2">
          <Loader2 aria-hidden="true" className="size-4 animate-spin" />
          {reasonLabel || reason || t(($) => $.infoflow_groups.waiting)}
        </p>}
        {failed && <p role="alert">{reasonLabel || reason || t(($) => $.infoflow_groups.failed)}</p>}
      </>}
      {error && <p role="alert">{error}</p>}
      <DialogFooter>
        <DialogClose render={<Button type="button" variant="outline" disabled={busy} />}>{t(($) => $.infoflow_groups.close)}</DialogClose>
        <Button type="button" variant="outline" disabled={busy || refreshing} onClick={refresh}>{t(($) => $.infoflow_groups.refresh)}</Button>
        {canManage && !active && <Button type="submit" disabled={busy || refreshing || !target || requestWaiting} aria-busy={busy}>
          {busy && <Loader2 aria-hidden="true" className="size-4 animate-spin" />}
          {busy ? t(($) => $.infoflow_groups.submitting) : failed ? t(($) => $.infoflow_groups.retry) : t(($) => $.infoflow_groups.submit)}
        </Button>}
      </DialogFooter>
    </form>
  </DialogContent>;
}
