"use client";

import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { Loader2, MessageSquare } from "lucide-react";
import { useAuthStore } from "@multica/core/auth";
import {
  infoFlowConversationURL, infoFlowGroupLabel, isInfoFlowConversationsUnavailable, parseInfoFlowGroupTarget,
  useBindIssueInfoFlowConversation, useIssueInfoFlowConversations, useUnbindIssueInfoFlowConversation, useCancelIssueInfoFlowRequest, useUpdateIssueInfoFlowSync,
  type InfoFlowConversationsStatus,
} from "@multica/core/issues/infoflow-conversations";
import { Button, buttonVariants } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Switch } from "@multica/ui/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@multica/ui/components/ui/tabs";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@multica/ui/components/ui/dialog";
import { useT } from "../../i18n";
import { IssueInfoFlowGroupBinding } from "./issue-infoflow-group-binding";

export function BindInfoFlowConversation({ workspaceId, issueId, managementURL = "" }: { workspaceId: string; issueId: string; managementURL?: string }) {
  const { t } = useT("issues");
  const userId = useAuthStore((state) => state.user?.id);
  const query = useIssueInfoFlowConversations(workspaceId, issueId);
  const bind = useBindIssueInfoFlowConversation(workspaceId, issueId);
  const unbind = useUnbindIssueInfoFlowConversation(workspaceId, issueId);
  const cancel = useCancelIssueInfoFlowRequest(workspaceId, issueId);
  const sync = useUpdateIssueInfoFlowSync(workspaceId, issueId);
  const [open, setOpen] = useState(false);
  const status = !query.isError && userId ? query.data : undefined;
  const canManage = status?.enabled === true && (status.group.canManage || status.private.canManage || status.group.request?.canCancel || status.private.request?.canCancel);
  const busy = bind.isPending || unbind.isPending || cancel.isPending || sync.isPending;
  // Only endpoint absence is a compatibility signal. A denied or malformed
  // unified response must not fall back to an older permission check.
  if (query.isError && isInfoFlowConversationsUnavailable(query.error) && !open) {
    return <IssueInfoFlowGroupBinding workspaceId={workspaceId} issueId={issueId} />;
  }
  const group = status?.group.binding?.state === "active" ? status.group.binding : null;
  const privates = status?.private.bindings.filter((binding) => binding.state === "active" && binding.robotChatId) || [];
  if (!open && !canManage && !group && !privates.length) return null;
  return <>
    <Dialog open={open} onOpenChange={(value) => { if (!busy) setOpen(value); }}>
      {(canManage || group || privates.length > 0) && <DialogTrigger render={<Button type="button" variant="outline" size="sm" />}>
        <MessageSquare aria-hidden="true" />{t(($) => $.infoflow_conversations.bind)}
      </DialogTrigger>}
      {open && <ConversationDialog key={`${userId}:${workspaceId}:${issueId}`} status={status} readError={query.isError || !userId}
        refreshing={query.isFetching} refresh={() => { void query.refetch(); }} bind={bind} unbind={unbind} cancel={cancel} sync={sync} managementURL={managementURL} />}
    </Dialog>
  </>;
}

function ConversationDialog({ status, readError, refreshing, refresh, bind, unbind, cancel, sync, managementURL }: {
  status?: InfoFlowConversationsStatus;
  readError: boolean;
  refreshing: boolean;
  refresh: () => void;
  managementURL: string;
  bind: ReturnType<typeof useBindIssueInfoFlowConversation>;
  unbind: ReturnType<typeof useUnbindIssueInfoFlowConversation>;
  cancel: ReturnType<typeof useCancelIssueInfoFlowRequest>;
  sync: ReturnType<typeof useUpdateIssueInfoFlowSync>;
}) {
  const { t } = useT("issues");
  const inputId = useId();
  const group = status?.group;
  const privateStatus = status?.private;
  const groupCanManage = status?.enabled === true && group?.canManage === true && !readError;
  const privateCanManage = status?.enabled === true && privateStatus?.canManage === true && !readError;
  const privateRobot = privateCanManage && privateStatus?.eligible ? privateStatus.robot : undefined;
  const robotBlocked = privateRobot && ["missing", "ambiguous", "unavailable"].includes(privateRobot.state);
  const reserved = group?.binding?.groupId || (group?.request && group.request.state !== "failed" ? group.request.groupId : undefined);
  const [kind, setKind] = useState<"group" | "private">(!groupCanManage && privateCanManage ? "private" : "group");
  const [mode, setMode] = useState<"existing" | "manual">(!reserved && !group?.request && group?.groups.length ? "existing" : "manual");
  const [selected, setSelected] = useState("");
  const [input, setInput] = useState(reserved || group?.request?.groupId || "");
  const [error, setError] = useState("");
  const inFlight = useRef(false);
  const busy = bind.isPending || unbind.isPending || cancel.isPending || sync.isPending;
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
    privateCanManage && privateStatus?.eligible === true && !robotBlocked && !privateWaiting && !privateActive;
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
    // The current service returns this assignment constraint as prose.
    "私聊仅支持由单个智能体负责的任务；小队、人工负责人或未分配任务请使用群聊": t(($) => $.infoflow_conversations.private_restriction),
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
  async function changeSync(bindingId: string, generation: string | undefined, enabled: boolean, permitted: boolean) {
    if (!permitted || !generation || busy || inFlight.current || refreshing || readError) return;
    inFlight.current = true; setError("");
    try { await sync.mutateAsync({bindingId, expectedGeneration:generation, syncEnabled:enabled}); }
    catch (cause) { setError(cause instanceof Error ? cause.message : t(($) => $.infoflow_conversations.sync_failed)); }
    finally { inFlight.current = false; }
  }
  return <DialogContent className="max-h-[85dvh] overflow-y-auto sm:max-w-lg" showCloseButton={!busy}>
    <form className="grid gap-4" onSubmit={(event) => void submit(event)}>
      <DialogHeader>
        <DialogTitle>{t(($) => $.infoflow_conversations.bind)}</DialogTitle>
        <DialogDescription>{kind === "private" ? t(($) => $.infoflow_conversations.private_restriction) : t(($) => $.infoflow_conversations.description)}</DialogDescription>
      </DialogHeader>
      <p role="note" className="text-caption text-muted-foreground">{t(($) => $.infoflow_conversations.sync_scope)}</p>
      {(readError || !status?.enabled) && <p role="alert">{t(($) => $.infoflow_groups.read_failed)}</p>}
      {!readError && status?.enabled && <Tabs value={kind} onValueChange={(value) => { if (!busy && (value === "group" || value === "private")) { setKind(value); setError(""); } }}>
        <TabsList aria-label={t(($) => $.infoflow_conversations.kind)}>
          <TabsTrigger value="group" disabled={busy}>{t(($) => $.infoflow_conversations.group)}</TabsTrigger>
          <TabsTrigger value="private" disabled={busy}>{t(($) => $.infoflow_conversations.private)}</TabsTrigger>
        </TabsList>
        <TabsContent value="group" className="grid gap-3 pt-2">
          <p className="text-caption text-muted-foreground">{t(($) => $.infoflow_groups.warning)}</p>
          {group?.binding && <ConversationBindingCard
            title={`${t(($) => $.infoflow_groups.target)}：${infoFlowGroupLabel(group.binding.groupId, group.binding.name)}`}
            status={groupActive ? t(($) => $.infoflow_groups.success) : t(($) => $.infoflow_conversations.group_pending)}
            sync={groupActive ? {
              id: `${inputId}-group-sync`, label: t(($) => $.infoflow_conversations.sync_group),
              checked: group.binding.syncEnabled !== false,
              disabled: !groupCanManage || !group.binding.bindingId || !group.binding.generation || busy || refreshing,
              pending: sync.isPending,
              onCheckedChange: (value) => { void changeSync(group.binding!.bindingId!, group.binding!.generation, value, groupCanManage); },
            } : undefined}
            actions={<>
              {groupActive && <a href={infoFlowConversationURL(group.binding.groupId, "group")}
                title={infoFlowGroupLabel(group.binding.groupId, group.binding.name)}
                className={buttonVariants({ variant: "outline", size: "sm" })}>{t(($) => $.infoflow_groups.open)}</a>}
              {groupCanManage && group.binding.bindingId && <Button type="button" variant="outline" size="sm" disabled={busy || refreshing}
                aria-label={t(($) => $.infoflow_conversations.unbind_group)} onClick={() => { void remove(group.binding!.bindingId!, groupCanManage); }}>
                {t(($) => $.infoflow_conversations.unbind)}
              </Button>}
            </>}
          />}
          {groupCanManage && !groupActive && <>
            {reserved ? <p className="text-caption text-muted-foreground">{t(($) => $.infoflow_conversations.reserved)}</p> : <fieldset className="grid gap-3" disabled={busy}>
              <legend className="sr-only">{t(($) => $.infoflow_groups.bind)}</legend>
              <label className="flex items-center gap-2 text-body">
                <input type="radio" name={`${inputId}-target-mode`} checked={mode === "existing"} onChange={() => switchMode("existing")} />
                {t(($) => $.infoflow_groups.existing)}
              </label>
              {mode === "existing" && <div className="grid gap-2 text-caption">
                <label htmlFor={`${inputId}-existing`}>{t(($) => $.infoflow_groups.choose)}</label>
                <select className="w-full min-w-0 rounded-lg border border-[color:var(--input)] bg-background p-2 text-body text-foreground"
                  id={`${inputId}-existing`} value={availableSelection} onChange={(event) => { setSelected(event.target.value); setError(""); }}>
                  <option value="">{t(($) => $.infoflow_groups.choose)}</option>
                  {group.groups.map((item) => <option key={item.groupId} value={item.groupId}>{infoFlowGroupLabel(item.groupId, item.name)}</option>)}
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
          {privateRobot?.name && <p className="break-words">{`${t(($) => $.infoflow_conversations.private_robot)}：${privateRobot.name}`}</p>}
          {privateRobot?.state === "missing" && <p role="status">
            {managementURL ? <a href={managementURL} target="_blank" rel="noreferrer" className="underline underline-offset-4">
              {t(($) => $.infoflow_conversations.bind_robot_first)}
            </a> : t(($) => $.infoflow_conversations.bind_robot_first)}
          </p>}
          {privateRobot?.state === "ambiguous" && <p role="status">{t(($) => $.infoflow_conversations.private_missing)}</p>}
          {privateRobot?.state === "unavailable" && <p role="status">{t(($) => $.infoflow_conversations.private_unavailable)}</p>}
          {privateStatus?.reason && reasonLabel(privateStatus.reason) !== t(($) => $.infoflow_conversations.private_restriction) &&
            <p className="text-caption">{reasonLabel(privateStatus.reason)}</p>}
          {privateStatus?.eligible && !privateCanManage && <p>{t(($) => $.infoflow_conversations.no_manage)}</p>}
          {privateCanManage && privateStatus.bindings.map((binding) => <ConversationBindingCard key={binding.bindingId}
            title={binding.robotName || binding.robotId}
            status={binding.state === "active" ? t(($) => $.infoflow_conversations.private_success) : t(($) => $.infoflow_conversations.needs_rebind)}
            details={binding.state === "needs_rebind" && <p className="text-caption text-muted-foreground">{reasonLabel(binding.reason, t(($) => $.infoflow_conversations.reassignment))}</p>}
            sync={binding.state === "active" ? {
              id: `${inputId}-private-sync-${binding.bindingId}`, label: t(($) => $.infoflow_conversations.sync_private),
              checked: binding.syncEnabled !== false, disabled: !binding.generation || busy || refreshing,
              pending: sync.isPending,
              onCheckedChange: (value) => { void changeSync(binding.bindingId, binding.generation, value, privateCanManage); },
            } : undefined}
            actions={<>
              {binding.state === "active" && binding.robotChatId && <a href={infoFlowConversationURL(binding.robotChatId, "private")} className={buttonVariants({ variant: "outline", size: "sm" })}>
                {t(($) => $.infoflow_conversations.open_private)}
              </a>}
              <Button type="button" variant="outline" size="sm" disabled={busy || refreshing}
                aria-label={`${t(($) => $.infoflow_conversations.unbind_private)} ${binding.robotName || binding.robotId}`}
                onClick={() => { void remove(binding.bindingId, privateCanManage); }}>{t(($) => $.infoflow_conversations.unbind)}</Button>
            </>}
          />)}
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

function ConversationBindingCard({ title, status, details, sync, actions }: {
  title: string;
  status: string;
  details?: ReactNode;
  sync?: {
    id: string;
    label: string;
    checked: boolean;
    disabled: boolean;
    pending: boolean;
    onCheckedChange: (value: boolean) => void;
  };
  actions: ReactNode;
}) {
  const { t } = useT("issues");
  return <section aria-label={title} data-slot="infoflow-binding-card" className="grid min-w-0 gap-2 rounded-lg border p-3">
    <p className="min-w-0 break-words [overflow-wrap:anywhere]">{title}</p>
    <p role="status">{status}</p>
    {details}
    {sync && <>
      <div className="flex items-center justify-between gap-3">
        <label htmlFor={sync.id}>{sync.label}</label>
        <Switch id={sync.id} checked={sync.checked} disabled={sync.disabled}
          aria-busy={sync.pending} onCheckedChange={sync.onCheckedChange} />
      </div>
      <p className="text-caption text-muted-foreground">{t(($) => $.infoflow_conversations.sync_help)}</p>
    </>}
    <div data-slot="infoflow-binding-actions" className="flex flex-wrap items-center gap-2">{actions}</div>
  </section>;
}
