"use client";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@multica/core/hooks";
import { useIssueAdministrators, useUpdateIssueAdministrators } from "@multica/core/issues";
import { memberListOptions } from "@multica/core/workspace/queries";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@multica/ui/components/ui/dialog";
import { useT } from "../../i18n";

export function IssueAdministratorsSection({ issueId }: { issueId: string }) {
 const { t } = useT("issues"); const ws = useWorkspaceId();
 const query = useIssueAdministrators(ws, issueId); const mutation = useUpdateIssueAdministrators(ws, issueId);
 const { data: members = [] } = useQuery(memberListOptions(ws));
 const [open, setOpen] = useState(false); const [selected, setSelected] = useState<Set<string>>(new Set()); const [error, setError] = useState("");
 const save = async () => { setError(""); try { await mutation.mutateAsync([...selected]); setOpen(false); } catch (e) { setError(e instanceof Error ? e.message : t($ => $.administrators.failed)); } };
 return <section className="space-y-2 border-t px-4 py-3">
  <div className="flex items-center justify-between gap-2"><h3 className="text-caption font-medium">{t($ => $.administrators.title)}</h3>
   <Dialog open={open} onOpenChange={v => { if (!mutation.isPending) { if (v) setSelected(new Set(query.data?.administrators.map(a => a.userId))); setError(""); setOpen(v); } }}>
    {query.data?.canManage && <DialogTrigger render={<Button variant="ghost" size="xs" />}>{t($ => $.administrators.edit)}</DialogTrigger>}
    <DialogContent><DialogHeader><DialogTitle>{t($ => $.administrators.title)}</DialogTitle><DialogDescription>{t($ => $.administrators.hint)}</DialogDescription></DialogHeader>
     <div className="max-h-72 space-y-2 overflow-y-auto">{members.map(member => <label key={member.user_id} className="flex items-center gap-2 text-body"><Checkbox checked={selected.has(member.user_id)} disabled={mutation.isPending} onCheckedChange={v => setSelected(previous => { const next = new Set(previous); if (v) next.add(member.user_id); else next.delete(member.user_id); return next; })} />{member.name}</label>)}</div>
     {error && <p role="alert" className="text-caption text-destructive">{error}</p>}
     <DialogFooter><Button variant="outline" onClick={() => setOpen(false)} disabled={mutation.isPending}>{t($ => $.administrators.cancel)}</Button><Button onClick={() => void save()} disabled={!selected.size || mutation.isPending} aria-busy={mutation.isPending}>{t($ => $.administrators.save)}</Button></DialogFooter>
    </DialogContent>
   </Dialog>
  </div>
  <p className="break-words text-caption text-muted-foreground">{query.isError ? t($ => $.administrators.failed) : query.isPending ? t($ => $.administrators.loading) : query.data?.administrators.map(a => a.name).join("、")}</p>
 </section>;
}
