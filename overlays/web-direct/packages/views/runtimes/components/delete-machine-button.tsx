"use client";

import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Loader2, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { useDeleteOfflineMachine } from "@multica/core/runtimes/mutations";
import { api } from "@multica/core/api";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { useT } from "../../i18n";
import type { RuntimeMachine } from "./runtime-machines";

type PersonalMachineDeletePlan = Awaited<ReturnType<typeof api.personalMachineDeletePlan>>;

export function DeleteMachineButton({ machine }: { machine: RuntimeMachine }) {
  const userId = useAuthStore((state) => state.user?.id);
  const allowed = !!userId && !!machine.daemonId && machine.mode === "local"
    && machine.runtimes.length > 0
    && machine.runtimes.every((runtime) => runtime.status !== "online" && runtime.owner_id === userId);
  if (!allowed) return null;

  // A different account, computer, or registration gets a fresh confirmation.
  return <DeleteMachineAction key={`${userId}:${machine.id}:${machine.runtimes[0]!.id}`} machine={machine} userId={userId} />;
}

function DeleteMachineAction({ machine, userId }: { machine: RuntimeMachine; userId: string }) {
  const { t } = useT("runtimes");
  const wsId = useWorkspaceId();
  const [loading, setLoading] = useState(false);
  const mutation = useDeleteOfflineMachine(wsId);
  const [plan, setPlan] = useState<(PersonalMachineDeletePlan & { runtimeId: string }) | null>(null);
  const [confirmed, setConfirmed] = useState(false);
  const [error, setError] = useState("");
  const alive = useRef(true);
  const submitting = useRef(false);
  const { data: workspaces = [] } = useQuery({
    queryKey: ["workspaces", "list", userId],
    queryFn: () => api.listWorkspaces(),
    enabled: !!plan?.agents.length,
  });
  useEffect(() => {
    alive.current = true;
    return () => { alive.current = false; };
  }, []);
  const isCurrent = () => alive.current && useAuthStore.getState().user?.id === userId;

  async function loadPlan() {
    if (loading) return;
    setLoading(true);
    setConfirmed(false);
    setError("");
    const runtimeId = machine.runtimes[0]!.id;
    try {
      const next = await api.personalMachineDeletePlan(runtimeId);
      if (isCurrent()) setPlan({ ...next, runtimeId });
    } catch (cause) {
      if (isCurrent()) toast.error(cause instanceof Error ? cause.message : t(($) => $.machine_delete.failed));
    } finally {
      if (isCurrent()) setLoading(false);
    }
  }

  async function deleteMachine() {
    if (!plan || !confirmed || error || submitting.current || !isCurrent()) return;
    submitting.current = true;
    try {
      await mutation.mutateAsync({ runtimeId: plan.runtimeId, runtimeIds: plan.ids, agentIds: plan.agents.map((agent) => agent.id) });
      if (isCurrent()) {
        setPlan(null);
        toast.success(t(($) => $.machine_delete.success));
      }
    } catch (cause) {
      if (isCurrent()) setError(cause instanceof Error ? cause.message : t(($) => $.machine_delete.failed));
    } finally {
      submitting.current = false;
    }
  }

  return <>
    <Button variant="ghost" size="sm" className="mr-3 shrink-0 text-destructive"
      disabled={loading || mutation.isPending} aria-busy={loading}
      aria-label={`${t(($) => $.machine_delete.button)} ${machine.title}`} onClick={loadPlan}>
      {loading ? <Loader2 className="size-3.5 animate-spin" /> : <Trash2 className="size-3.5" />}
      {t(($) => $.machine_delete.button)}
    </Button>
    <Dialog open={!!plan} onOpenChange={(open) => { if (!open && !submitting.current) setPlan(null); }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t(($) => $.machine_delete.title, {name:machine.title})}</DialogTitle>
          <DialogDescription>{t(($) => $.machine_delete.description, {count:plan?.ids.length ?? 0})}</DialogDescription>
        </DialogHeader>
        <p className="text-body text-muted-foreground">{t(($) => $.machine_delete.effects)}</p>
        {!!plan?.agents.length && <ul className="max-h-40 space-y-1 overflow-auto text-body">
          {plan.agents.map((agent) => <li key={agent.id} className="flex flex-wrap justify-between gap-x-3 break-all">
            <span>{agent.name}</span>
            <span className="text-caption text-muted-foreground">{workspaces.find((workspace) => workspace.id === agent.workspace_id)?.name ?? agent.workspace_id}</span>
          </li>)}
        </ul>}
        <label className="flex items-start gap-2 text-body">
          <Checkbox checked={confirmed} disabled={mutation.isPending} onCheckedChange={(value) => setConfirmed(value === true)} />
          {t(($) => $.machine_delete.confirm_unbind)}
        </label>
        <p className="text-caption text-muted-foreground">{t(($) => $.machine_delete.reconnect)}</p>
        {error && <p role="alert" className="text-body text-destructive">{error}</p>}
        <DialogFooter>
          <Button variant="outline" disabled={mutation.isPending} onClick={() => setPlan(null)}>{t(($) => $.machine_delete.cancel)}</Button>
          <Button variant="destructive" aria-busy={mutation.isPending} disabled={mutation.isPending || !!error || !confirmed} onClick={deleteMachine}>
            {mutation.isPending ? t(($) => $.machine_delete.deleting) : t(($) => $.machine_delete.button)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </>;
}
