"use client";
import { useIssuePermissions } from "@multica/core/issues";
import { DropdownMenuItem } from "@multica/ui/components/ui/dropdown-menu";
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
 const { t } = useT("issues"); const navigation = useNavigation();
 const [target,setTarget] = useState(""); const [includeSubissues,setIncludeSubissues] = useState(false);
 const [mappings,setMappings] = useState<Record<string,string>>({});
 const [confirmed,setConfirmed] = useState<string | null>(null); const [error,setError] = useState("");
 const workspaces=useQuery(workspaceListOptions());
 const preview=useIssueTransferPreview(issue.workspace_id,issue.id,target,"",includeSubissues,mappings);
 const transfer=useTransferIssue(issue.id); const plan=preview.data; const busy=transfer.isPending;
 const ready=!!plan&&!preview.isFetching&&!preview.isError&&!plan.blockers.length&&confirmed===plan.fingerprint;
 const reset=()=>{setConfirmed(null);setError("");};
 const submit=async()=>{
  if(!ready||!plan||busy)return;setError("");
  try{
   const result=await transfer.mutateAsync({target_workspace_id:target,include_subissues:includeSubissues,status_mappings:Object.fromEntries(plan.status_mappings.map(s=>[s.source,s.target])),fingerprint:plan.fingerprint,confirm_unassign:plan.unassign,confirm_cleanup:true});
   if(!result.identifier)throw new Error(t($=>$.transfer.invalid_result));
   navigation.push(`/${result.target_slug}/issues/${encodeURIComponent(result.identifier)}`);onClose();
  }catch(e){setError(e instanceof Error?e.message:t($=>$.transfer.failed));setConfirmed(null);void preview.refetch();}
 };
 return <Dialog open onOpenChange={v=>{if(!v&&!busy)onClose();}}><DialogContent className="max-h-[90dvh] max-w-lg overflow-y-auto">
  <DialogHeader><DialogTitle>{t($=>$.transfer.title)}</DialogTitle><DialogDescription>{t($=>$.transfer.warning)}</DialogDescription></DialogHeader>
  <p className="text-caption">{issue.identifier} · {issue.title}</p>
  <label className="grid gap-2 text-caption">{t($=>$.transfer.target)}<select className="rounded-md border bg-background p-2" value={target} disabled={busy||workspaces.isLoading} onChange={e=>{setTarget(e.target.value);setMappings({});reset();}}><option value="">{t($=>$.transfer.choose)}</option>{workspaces.data?.filter(w=>w.id!==issue.workspace_id).map(w=><option key={w.id} value={w.id}>{w.name}</option>)}</select></label>
  <label className="flex items-center gap-2 text-caption"><Checkbox checked={includeSubissues} disabled={busy} onCheckedChange={v=>{setIncludeSubissues(!!v);reset();}}/>{t($=>$.transfer_expanded.tree)}</label>
  {preview.isFetching&&<p role="status" className="text-caption">{t($=>$.transfer.checking)}</p>}
  {(workspaces.isError||preview.isError)&&<p role="alert" className="text-caption text-destructive">{preview.error?.message||t($=>$.transfer.failed)}</p>}
  {plan&&!preview.isError&&<>
   <p className="text-caption">{t($=>$.transfer_expanded.count,{count:plan.affected_issues})} {t($=>$.transfer.summary,{comments:plan.comments,runs:plan.runs,messages:plan.messages,prefix:plan.target_prefix})}</p>
   {plan.status_mappings.map(mapping=><label key={mapping.source} className="grid gap-2 text-caption">{t($=>$.transfer.status)} · {mapping.name}<select className="rounded-md border bg-background p-2" value={mapping.target} disabled={busy||preview.isFetching} onChange={e=>{setMappings(previous=>({...previous,[mapping.source]:e.target.value}));reset();}}><option value="">{t($=>$.transfer.choose_status)}</option>{plan.statuses.map(s=><option key={s.key} value={s.key}>{s.name}</option>)}</select></label>)}
   {plan.blockers.length>0&&<p role="alert" className="text-caption text-destructive">{plan.blockers.some(code=>code!=="status_mapping_required")?t($=>$.transfer_expanded.integrity):t($=>$.transfer_expanded.status_required)}</p>}
   <p className="text-caption text-muted-foreground">{t($=>$.transfer_expanded.effects)}</p>
   <p className="text-caption text-muted-foreground">{t($=>$.transfer_expanded.private_history)}</p>
   <label className="flex items-start gap-2 text-caption"><Checkbox checked={confirmed===plan.fingerprint} disabled={busy||preview.isFetching||plan.blockers.length>0} onCheckedChange={v=>setConfirmed(v?plan.fingerprint:null)}/>{t($=>$.transfer_expanded.confirm_cleanup)}</label>
  </>}
  {error&&<p role="alert" className="text-caption text-destructive">{error}</p>}
  <DialogFooter><Button variant="outline" disabled={busy} onClick={onClose}>{t($=>$.transfer.cancel)}</Button><Button variant="outline" disabled={!target||busy||preview.isFetching} onClick={()=>{reset();void preview.refetch();}}>{t($=>$.transfer.refresh)}</Button><Button disabled={!ready||busy} aria-busy={busy} onClick={()=>void submit()}>{t($=>$.transfer.confirm)}</Button></DialogFooter>
 </DialogContent></Dialog>;
}

// Mounted inside the menu portal so listing many tasks does not start one
// permission poller per closed dropdown.
export function IssueTransferMenuItem({issue,workspaceAdmin,onSelect}:{issue:Issue;workspaceAdmin:boolean;onSelect:()=>void}){
 const query=useIssuePermissions(issue.workspace_id,issue.id);const {t}=useT("issues");
 const allowed=workspaceAdmin||query.data?.canDelete===true;
 return <DropdownMenuItem disabled={!allowed} title={allowed?undefined:t($=>$.transfer_expanded.forbidden)} onClick={onSelect}>{t($=>$.transfer.title)}</DropdownMenuItem>;
}
