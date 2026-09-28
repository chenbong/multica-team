"use client";
import { useState } from "react";
import { toast } from "sonner";
import { useAuthStore } from "@multica/core/auth";
import { useDuccCredential, useUpdateDuccCredential } from "@multica/core/runtimes";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { Dialog,DialogContent,DialogHeader,DialogTitle,DialogDescription,DialogFooter } from "@multica/ui/components/ui/dialog";
import { SettingsCard,SettingsRow,SettingsSection } from "./settings-layout";
import { useT } from "../../i18n";

export function DuccCredentialCard(){
 const {t}=useT("settings");
 const id=useAuthStore(s=>s.user?.id);
 const query=useDuccCredential(id),mutation=useUpdateDuccCredential(id);
 const [confirm,setConfirm]=useState<{action:"import"|"remove";daemon_id?:string;name?:string}|null>(null);
 const data=query.data;
 const change=async(input:{action:"enable"|"import"|"remove";enabled?:boolean;daemon_id?:string})=>{try{await mutation.mutateAsync(input);setConfirm(null);toast.success(t($=>$.ducc.saved))}catch{toast.error(t($=>$.ducc.failed))}};
 const states:Record<string,string>={unknown:t($=>$.ducc.state_unknown),missing:t($=>$.ducc.state_missing),present:t($=>$.ducc.state_present),ready:t($=>$.ducc.state_ready),invalid:t($=>$.ducc.state_invalid),installing:t($=>$.ducc.state_installing),install_failed:t($=>$.ducc.state_failed)};
 return <SettingsSection title={t($=>$.ducc.title)}><SettingsCard>
  <SettingsRow label={t($=>$.ducc.status)} description={t($=>$.ducc.scope)}>
   <div className="space-y-1 text-body">
    <p>{query.isError||data?.available===false?t($=>$.ducc.unavailable):query.isPending?t($=>$.ducc.loading):data?.pending_daemon?t($=>$.ducc.pending):data?.imported?t($=>$.ducc.imported):t($=>$.ducc.not_imported)}</p>
    {data?.username&&<p className="text-muted-foreground">{data.username}</p>}
    {data?.imported_at&&<p className="text-caption text-muted-foreground">{t($=>$.ducc.source)}：{data.machines.find(m=>m.daemon_id===data.source_daemon)?.name||data.source_daemon} · {new Date(data.imported_at).toLocaleString()}</p>}
   </div>
  </SettingsRow>
  <SettingsRow label={t($=>$.ducc.auto)} description={t($=>$.ducc.auto_desc)}>
   <Checkbox aria-label={t($=>$.ducc.auto)} checked={data?.enabled===true} disabled={!data?.available||mutation.isPending||query.isError} onCheckedChange={enabled=>void change({action:"enable",enabled:enabled===true})}/>
  </SettingsRow>
  <SettingsRow label={t($=>$.ducc.computers)} description={t($=>$.ducc.computers_desc)}>
   <div className="w-full space-y-3">{data?.machines.map(m=><div key={m.daemon_id} className="flex flex-wrap items-center justify-between gap-2">
    <div className="min-w-0"><p className="break-all text-body">{m.name}</p><p className="text-caption text-muted-foreground">{!m.supported?t($=>$.ducc.upgrade):!m.online?t($=>$.ducc.offline):states[m.state||"unknown"]||t($=>$.ducc.state_unknown)}</p></div>
    <Button size="sm" variant="outline" disabled={!m.supported||!m.online||!m.installed||mutation.isPending} onClick={()=>setConfirm({action:"import",daemon_id:m.daemon_id,name:m.name})}>{t($=>$.ducc.import_button)}</Button>
   </div>)}{data?.machines.length===0&&<p className="text-body text-muted-foreground">{t($=>$.ducc.no_computers)}</p>}</div>
  </SettingsRow>
  {data?.imported&&<SettingsRow label={t($=>$.ducc.remove)} description={t($=>$.ducc.remove_desc)}><Button size="sm" variant="outline" disabled={mutation.isPending} onClick={()=>setConfirm({action:"remove"})}>{t($=>$.ducc.remove)}</Button></SettingsRow>}
 </SettingsCard>
 <Dialog open={!!confirm} onOpenChange={v=>{if(!v&&!mutation.isPending)setConfirm(null)}}><DialogContent><DialogHeader><DialogTitle>{confirm?.action==="import"?t($=>$.ducc.import_button):t($=>$.ducc.remove)}</DialogTitle><DialogDescription>{confirm?.action==="import"?t($=>$.ducc.import_confirm,{name:confirm.name||""}):t($=>$.ducc.remove_desc)}</DialogDescription></DialogHeader><DialogFooter><Button variant="outline" disabled={mutation.isPending} onClick={()=>setConfirm(null)}>{t($=>$.ducc.cancel)}</Button><Button disabled={mutation.isPending} onClick={()=>confirm&&void change(confirm)}>{t($=>$.ducc.confirm)}</Button></DialogFooter></DialogContent></Dialog>
 </SettingsSection>
}
