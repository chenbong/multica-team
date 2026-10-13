"use client";

import { useEffect, useId, useRef, useState } from "react";
import { Loader2, MessageSquare } from "lucide-react";
import { useAuthStore } from "@multica/core/auth";
import {
  infoFlowConversationURL, isInfoFlowConversationsUnavailable, parseInfoFlowGroupTarget,
  useBindIssueInfoFlowConversation, useIssueInfoFlowConversations, useUnbindIssueInfoFlowConversation, useCancelIssueInfoFlowRequest,
  type InfoFlowConversationsStatus,
} from "@multica/core/issues/infoflow-conversations";
import { Button, buttonVariants } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@multica/ui/components/ui/tabs";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@multica/ui/components/ui/dialog";
import { useT } from "../../i18n";
import { IssueInfoFlowGroupBinding } from "./issue-infoflow-group-binding";

export function BindInfoFlowConversation({ workspaceId, issueId }: { workspaceId: string; issueId: string }) {
  const { t } = useT("issues");
  const userId = useAuthStore((state) => state.user?.id);
  const query = useIssueInfoFlowConversations(workspaceId, issueId);
  const bind = useBindIssueInfoFlowConversation(workspaceId, issueId);
  const unbind = useUnbindIssueInfoFlowConversation(workspaceId, issueId);
  const cancel = useCancelIssueInfoFlowRequest(workspaceId, issueId);
  const [open, setOpen] = useState(false);
  const status = !query.isError && userId ? query.data : undefined;
  const canManage = status?.enabled === true && (status.group.canManage || status.private.canManage || status.group.request?.canCancel || status.private.request?.canCancel);
  const busy = bind.isPending || unbind.isPending || cancel.isPending;
  // Only endpoint absence is a compatibility signal. A denied or malformed
  // unified response must not fall back to an older permission check.
  if (query.isError && isInfoFlowConversationsUnavailable(query.error) && !open) {
    return <IssueInfoFlowGroupBinding workspaceId={workspaceId} issueId={issueId} />;
  }
  const group = status?.group.binding?.state === "active" ? status.group.binding : null;
  const privates = status?.private.bindings.filter((binding) => binding.state === "active" && binding.robotChatId) || [];
  if (!open && !canManage && !group && !privates.length) return null;
  return <>
    {group && <a href={infoFlowConversationURL(group.groupId, "group")} className={buttonVariants({ variant: "outline", size: "sm" })}>
      <MessageSquare aria-hidden="true" />{t(($) => $.infoflow_groups.open)}
    </a>}
    {privates.map((binding) => <a key={binding.bindingId} href={infoFlowConversationURL(binding.robotChatId, "private")}
      className={buttonVariants({ variant: "outline", size: "sm" })}
      title={binding.robotName || binding.robotId}>
      <MessageSquare aria-hidden="true" />{t(($) => $.infoflow_conversations.open_private)}
    </a>)}
    <Dialog open={open} onOpenChange={(value) => { if (!busy) setOpen(value); }}>
      {canManage && <DialogTrigger render={<Button type="button" variant="outline" size="sm" />}>
        <MessageSquare aria-hidden="true" />{t(($) => $.infoflow_conversations.bind)}
      </DialogTrigger>}
      {open && <ConversationDialog key={`${userId}:${workspaceId}:${issueId}`} status={status} readError={query.isError || !userId}
        refreshing={query.isFetching} refresh={() => { void query.refetch(); }} bind={bind} unbind={unbind} cancel={cancel} />}
    </Dialog>
  </>;
}

function ConversationDialog({ status, readError, refreshing, refresh, bind, unbind, cancel }: {
  status?: InfoFlowConversationsStatus;
  readError: boolean;
  refreshing: boolean;
  refresh: () => void;
  bind: ReturnType<typeof useBindIssueInfoFlowConversation>;
  unbind: ReturnType<typeof useUnbindIssueInfoFlowConversation>;
  cancel: ReturnType<typeof useCancelIssueInfoFlowRequest>;
}) {
  const { t } = useT("issues");
  const inputId = useId();
  const group = status?.group;
  const privateStatus = status?.private;
  const groupCanManage = status?.enabled === true && group?.canManage === true && !readError;
  const privateCanManage = status?.enabled === true && privateStatus?.canManage === true && !readError;
  const reserved = group?.binding?.groupId || (group?.request && group.request.state !== "failed" ? group.request.groupId : undefined);
  const [kind, setKind] = useState<"group" | "private">(!groupCanManage && privateCanManage ? "private" : "group");
  const [mode, setMode] = useState<"existing" | "manual">(!reserved && !group?.request && group?.groups.length ? "existing" : "manual");
  const [selected, setSelected] = useState("");
  const [input, setInput] = useState(reserved || group?.request?.groupId || "");
  const [error, setError] = useState("");
  const inFlight = useRef(false);
  const busy = bind.isPending || unbind.isPending || cancel.isPending;
  const groupActive = group?.binding?.state === "active";
  const groupFailed = group?.request?.state === "failed";
  const groupWaiting = !groupActive && !groupFailed && (group?.binding?.state === "pending" || !!group?.request);
  const privateWaiting = privateStatus?.request?.state === "queued" || privateStatus?.request?.state === "pending";
  const privateFailed = privateStatus?.request?.state === "failed";
  const privateActive = privateStatus?.bindings.some((binding) => binding.state === "active");
  const needsRebind = privateStatus?.bindings.some((binding) => binding.state === "needs_rebind");
  const availableSelection = group?.groups.some((item) => item.groupId === selected) ? selected : "";
  const target = parseInfoFlowGroupTarget(reserved || (mode === "existing" ? availableSelection : input));
  const request = kind === "group" ? group?.request : privateStatus?.request;
  const cancellable = status?.enabled === true && !readError && request?.canCancel === true && !!request.generation;
  const writable = kind === "group" ? groupCanManage && !groupActive && !groupWaiting && !!target :
    privateCanManage && privateStatus?.eligible === true && !privateWaiting && !privateActive;
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
    private_robot_missing_or_ambiguous: t(($) => $.infoflow_conversations.private_missing),
    private_robot_unavailable: t(($) => $.infoflow_conversations.private_unavailable),
    private_chat_disabled: t(($) => $.infoflow_conversations.private_disabled),
    private_metadata_failed: t(($) => $.infoflow_conversations.private_metadata),
    assignment_changed: t(($) => $.infoflow_conversations.reassignment),
    private_requires_single_agent: t(($) => $.infoflow_conversations.private_restriction),
  };
  function reasonLabel(reason?: string, fallback = "") {
    return reason && Object.prototype.hasOwnProperty.call(reasonCopy, reason) ? reasonCopy[reason] : reason || fallback;
  }
  useEffect(() => {
    if (!groupCanManage) { setSelected(""); setInput(""); }
    if (readError || !status?.enabled) setError("");
  }, [groupCanManage, readError, status?.enabled]);
  function switchMode(value: "existing" | "manual") {
    setMode(value); setSelected(""); setInput(""); setError("");
  }
  async function submit(event: React.FormEvent) {
    event.preventDefault();
    if (busy || inFlight.current || refreshing || readError || !writable) return;
    inFlight.current = true; setError("");
    try {
      await bind.mutateAsync(kind === "group" && target ? { kind: "group", ...target } : { kind: "private" });
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : t(($) => $.infoflow_conversations.failed));
    } finally { inFlight.current = false; }
  }
  async function remove(bindingId: string, permitted: boolean) {
    if (!permitted || busy || inFlight.current || refreshing || readError) return;
    inFlight.current = true; setError("");
    try { await unbind.mutateAsync(bindingId); }
    catch (cause) { setError(cause instanceof Error ? cause.message : t(($) => $.infoflow_conversations.failed)); }
    finally { inFlight.current = false; }
  }
  async function cancelRequest() {
    if (!cancellable || !request?.generation || busy || inFlight.current || refreshing || readError) return;
    inFlight.current = true; setError("");
    try { await cancel.mutateAsync({ kind, request_id: request.requestId, expected_generation: request.generation }); }
    catch (cause) { setError(cause instanceof Error ? cause.message : t(($) => $.infoflow_conversations.failed)); }
    finally { inFlight.current = false; }
  }
  return <DialogContent className="max-h-[85dvh] overflow-y-auto sm:max-w-lg" showCloseButton={!busy}>
    <form className="grid gap-4" onSubmit={(event) => void submit(event)}>
      <DialogHeader>
        <DialogTitle>{t(($) => $.infoflow_conversations.bind)}</DialogTitle>
        <DialogDescription>{t(($) => $.infoflow_conversations.description)}</DialogDescription>
      </DialogHeader>
      {(readError || !status?.enabled) && <p role="alert">{t(($) => $.infoflow_groups.read_failed)}</p>}
      {!readError && status?.enabled && <Tabs value={kind} onValueChange={(value) => { if (!busy && (value === "group" || value === "private")) { setKind(value); setError(""); } }}>
        <TabsList aria-label={t(($) => $.infoflow_conversations.kind)}>
          <TabsTrigger value="group" disabled={busy}>{t(($) => $.infoflow_conversations.group)}</TabsTrigger>
          <TabsTrigger value="private" disabled={busy}>{t(($) => $.infoflow_conversations.private)}</TabsTrigger>
        </TabsList>
        <TabsContent value="group" className="grid gap-3 pt-2">
          <p className="text-caption text-muted-foreground">{t(($) => $.infoflow_groups.warning)}</p>
          {group?.binding && <div className="grid gap-2">
            <p>{t(($) => $.infoflow_groups.target)}：{group.binding.groupId}</p>
            {groupActive ? <p role="status">{t(($) => $.infoflow_groups.success)}</p> :
              <p className="text-caption">{t(($) => $.infoflow_conversations.group_pending)}</p>}
            {groupCanManage && group.binding.bindingId && <Button type="button" variant="outline" disabled={busy || refreshing}
              aria-label={t(($) => $.infoflow_conversations.unbind_group)} onClick={() => { void remove(group.binding!.bindingId!, groupCanManage); }}>
              {t(($) => $.infoflow_conversations.unbind)}
            </Button>}
          </div>}
          {groupCanManage && !groupActive && <>
            {reserved ? <p className="text-caption text-muted-foreground">{t(($) => $.infoflow_conversations.reserved)}</p> : <fieldset className="grid gap-3" disabled={busy}>
              <legend className="sr-only">{t(($) => $.infoflow_groups.bind)}</legend>
              <label className="flex items-center gap-2 text-body">
                <input type="radio" name={`${inputId}-target-mode`} checked={mode === "existing"} onChange={() => switchMode("existing")} />
                {t(($) => $.infoflow_groups.existing)}
              </label>
              {mode === "existing" && <div className="grid gap-2 text-caption">
                <label htmlFor={`${inputId}-existing`}>{t(($) => $.infoflow_groups.choose)}</label>
                <select className="w-full min-w-0 rounded-lg border border-input bg-background p-2 text-body text-foreground"
                  id={`${inputId}-existing`} value={availableSelection} onChange={(event) => { setSelected(event.target.value); setError(""); }}>
                  <option value="">{t(($) => $.infoflow_groups.choose)}</option>
                  {group.groups.map((item) => <option key={item.groupId} value={item.groupId}>{item.name ? `${item.name} · ${item.groupId}` : item.groupId}</option>)}
                </select>
                {!group.groups.length && <span className="text-muted-foreground">{t(($) => $.infoflow_groups.empty)}</span>}
              </div>}
              <label className="flex items-center gap-2 text-body">
                <input type="radio" name={`${inputId}-target-mode`} checked={mode === "manual"} onChange={() => switchMode("manual")} />
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
            </fieldset>}
          </>}
          {!groupCanManage && !groupActive && <p>{t(($) => $.infoflow_conversations.no_manage)}</p>}
          {groupWaiting && <p role="status" className="flex items-center gap-2"><Loader2 aria-hidden="true" className="size-4 animate-spin" />
            {reasonLabel(group?.request?.reason || group?.binding?.reason, t(($) => $.infoflow_groups.waiting))}
          </p>}
          {groupFailed && <p role="alert">{reasonLabel(group?.request?.reason, t(($) => $.infoflow_groups.failed))}</p>}
        </TabsContent>
        <TabsContent value="private" className="grid gap-3 pt-2">
          <p>{t(($) => $.infoflow_conversations.my_private)}</p>
          <p className="text-caption text-muted-foreground">{t(($) => $.infoflow_conversations.private_routing)}</p>
          {!privateStatus?.eligible && <p role="status">{t(($) => $.infoflow_conversations.private_restriction)}</p>}
          {privateStatus?.reason && <p className="text-caption">{reasonLabel(privateStatus.reason)}</p>}
          {!privateCanManage && <p>{t(($) => $.infoflow_conversations.no_manage)}</p>}
          {privateCanManage && privateStatus.bindings.map((binding) => <div key={binding.bindingId} className="grid gap-2 rounded-lg border p-3">
            <p className="break-all">{binding.robotName || binding.robotId}</p>
            <p role="status">{binding.state === "active" ? t(($) => $.infoflow_conversations.private_success) : t(($) => $.infoflow_conversations.needs_rebind)}</p>
            {binding.state === "needs_rebind" && <p className="text-caption text-muted-foreground">{reasonLabel(binding.reason, t(($) => $.infoflow_conversations.reassignment))}</p>}
            <div className="flex flex-wrap gap-2">
              {binding.state === "active" && binding.robotChatId && <a href={infoFlowConversationURL(binding.robotChatId, "private")} className={buttonVariants({ variant: "outline", size: "sm" })}>
                {t(($) => $.infoflow_conversations.open_private)}
              </a>}
              <Button type="button" variant="outline" size="sm" disabled={busy || refreshing}
                aria-label={`${t(($) => $.infoflow_conversations.unbind_private)} ${binding.robotName || binding.robotId}`}
                onClick={() => { void remove(binding.bindingId, privateCanManage); }}>{t(($) => $.infoflow_conversations.unbind)}</Button>
            </div>
          </div>)}
          {privateWaiting && <p role="status" className="flex items-center gap-2"><Loader2 aria-hidden="true" className="size-4 animate-spin" />
            {reasonLabel(privateStatus?.request?.reason, t(($) => $.infoflow_conversations.private_waiting))}
          </p>}
          {privateFailed && <p role="alert">{reasonLabel(privateStatus?.request?.reason, t(($) => $.infoflow_conversations.failed))}</p>}
        </TabsContent>
      </Tabs>}
      {error && <p role="alert">{error}</p>}
      {kind === "group" && cancellable && <p className="text-caption text-muted-foreground">{t(($) => $.infoflow_conversations.cancel_group_note)}</p>}
      <DialogFooter>
        <DialogClose render={<Button type="button" variant="outline" disabled={busy} />}>{t(($) => $.infoflow_groups.close)}</DialogClose>
        <Button type="button" variant="outline" disabled={busy || refreshing} onClick={refresh}>{t(($) => $.infoflow_groups.refresh)}</Button>
        {cancellable && <Button type="button" variant="outline" disabled={busy || refreshing} aria-busy={cancel.isPending}
          aria-label={kind === "group" ? t(($) => $.infoflow_conversations.cancel_group) : t(($) => $.infoflow_conversations.cancel_private)}
          onClick={() => { void cancelRequest(); }}>
          {cancel.isPending && <Loader2 aria-hidden="true" className="size-4 animate-spin" />}{t(($) => $.infoflow_conversations.cancel_request)}
        </Button>}
        {!readError && (kind === "group" ? groupCanManage && !groupActive : privateCanManage && privateStatus?.eligible && !privateActive) &&
          <Button type="submit" disabled={busy || refreshing || !writable} aria-busy={busy}>
            {bind.isPending && <Loader2 aria-hidden="true" className="size-4 animate-spin" />}
            {bind.isPending ? t(($) => $.infoflow_groups.submitting) : kind === "private" && needsRebind ? t(($) => $.infoflow_conversations.rebind) :
              (kind === "group" ? groupFailed : privateFailed) ? t(($) => $.infoflow_groups.retry) : t(($) => $.infoflow_groups.submit)}
          </Button>}
      </DialogFooter>
    </form>
  </DialogContent>;
}
