"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Trash2 } from "lucide-react";
import { toast } from "sonner";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { useDeleteOfflineMachine } from "@multica/core/runtimes/mutations";
import { agentListOptions, memberListOptions } from "@multica/core/workspace/queries";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { useT } from "../../i18n";
import type { RuntimeMachine } from "./runtime-machines";

export function DeleteMachineButton({ machine }: { machine: RuntimeMachine }) {
  const { t } = useT("runtimes");
  const wsId = useWorkspaceId();
  const userId = useAuthStore((s) => s.user?.id);
  const members = useQuery(memberListOptions(wsId));
  const agents = useQuery(agentListOptions(wsId));
  const mutation = useDeleteOfflineMachine(wsId);
  const [plan, setPlan] = useState<{ ids: string[]; agents: { id: string; name: string }[] } | null>(null);
  const [confirmed, setConfirmed] = useState(false);
  const [error, setError] = useState("");
  const member = members.data?.find((m) => m.user_id === userId);
  const admin = member?.role === "owner" || member?.role === "admin";
  const allowed = !!userId && !!machine.daemonId && machine.mode === "local" && machine.runtimes.length > 0
    && machine.runtimes.every((r) => r.status !== "online" && (admin || r.owner_id === userId));
  if (!allowed) return null;
  return <>
    <Button variant="ghost" size="sm" className="mr-3 shrink-0 text-destructive" disabled={!agents.isSuccess}
      aria-label={`${t(($) => $.machine_delete.button)} ${machine.title}`}
      onClick={() => {
        const ids = machine.runtimes.map((r) => r.id);
        setPlan({ ids, agents: (agents.data ?? []).filter((a) => !a.archived_at && !!a.runtime_id && ids.includes(a.runtime_id)).map((a) => ({id:a.id,name:a.name})) });
        setConfirmed(false); setError("");
      }}><Trash2 className="size-3.5" />{t(($) => $.machine_delete.button)}</Button>
    <Dialog open={!!plan} onOpenChange={(open) => { if (!open && !mutation.isPending) setPlan(null); }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t(($) => $.machine_delete.title, {name:machine.title})}</DialogTitle>
          <DialogDescription>{t(($) => $.machine_delete.description, {count:plan?.ids.length ?? 0})}</DialogDescription>
        </DialogHeader>
        <p className="text-body text-muted-foreground">{t(($) => $.machine_delete.effects)}</p>
        {!!plan?.agents.length && <>
          <ul className="max-h-40 overflow-auto text-body">{plan.agents.map((a) => <li key={a.id}>{a.name}</li>)}</ul>
          <label className="flex items-start gap-2 text-body"><Checkbox checked={confirmed} onCheckedChange={(v) => setConfirmed(v === true)} />{t(($) => $.machine_delete.confirm_unbind)}</label>
        </>}
        <p className="text-caption text-muted-foreground">{t(($) => $.machine_delete.reconnect)}</p>
        {error && <p role="alert" className="text-body text-destructive">{error}</p>}
        <DialogFooter>
          <Button variant="outline" disabled={mutation.isPending} onClick={() => setPlan(null)}>{t(($) => $.machine_delete.cancel)}</Button>
          <Button variant="destructive" disabled={mutation.isPending || !!error || (!!plan?.agents.length && !confirmed)} onClick={async () => {
            if (!plan) return;
            try {
              await mutation.mutateAsync({runtimeId:plan.ids[0]!, runtimeIds:plan.ids, agentIds:plan.agents.map((a) => a.id)});
              setPlan(null); toast.success(t(($) => $.machine_delete.success));
            } catch (e) { setError(e instanceof Error ? e.message : t(($) => $.machine_delete.failed)); }
          }}>{mutation.isPending ? t(($) => $.machine_delete.deleting) : t(($) => $.machine_delete.button)}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </>;
}
