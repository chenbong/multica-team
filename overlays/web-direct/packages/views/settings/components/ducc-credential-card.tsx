"use client";
import { useState } from "react";
import { toast } from "sonner";
import { useAuthStore } from "@multica/core/auth";
import { useDuccCredential, useUpdateDuccCredential } from "@multica/core/runtimes";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@multica/ui/components/ui/select";
import { Dialog,DialogContent,DialogHeader,DialogTitle,DialogDescription,DialogFooter } from "@multica/ui/components/ui/dialog";
import { SettingsCard,SettingsRow,SettingsSection } from "./settings-layout";
import { useT } from "../../i18n";

export function DuccCredentialCard(){
 const {t}=useT("settings");
 const id=useAuthStore(s=>s.user?.id);
 const query=useDuccCredential(id),mutation=useUpdateDuccCredential(id);
 const [confirm,setConfirm]=useState<{action:"import"|"remove";daemon_id?:string;name?:string}|null>(null);
 const [selectedId,setSelectedId]=useState<string|null>(null);
 const data=query.data;
 const change=async(input:{action:"enable"|"import"|"remove";enabled?:boolean;daemon_id?:string})=>{try{await mutation.mutateAsync(input);setConfirm(null);toast.success(t($=>$.ducc.saved))}catch{toast.error(t($=>$.ducc.failed))}};
 const states:Record<string,string>={unknown:t($=>$.ducc.state_unknown),missing:t($=>$.ducc.state_missing),present:t($=>$.ducc.state_present),ready:t($=>$.ducc.state_ready),invalid:t($=>$.ducc.state_invalid),installing:t($=>$.ducc.state_installing),install_failed:t($=>$.ducc.state_failed)};
 const selected=data?.machines.find(m=>m.daemon_id===selectedId);
 const options=(data?.machines??[]).map(m=>({value:m.daemon_id,label:m.name}));
 const machineStatus=(m:NonNullable<typeof selected>)=>!m.supported?t($=>$.ducc.upgrade):!m.online?t($=>$.ducc.offline):states[m.state||"unknown"]||t($=>$.ducc.state_unknown);
 return <SettingsSection title={t($=>$.ducc.title)}><SettingsCard>
  <SettingsRow label={t($=>$.ducc.status)} description={t($=>$.ducc.scope)}>
   <div className="space-y-1 text-body">
    <p>{query.isError||data?.available===false?t($=>$.ducc.unavailable):query.isPending?t($=>$.ducc.loading):data?.pending_daemon?t($=>$.ducc.pending):data?.imported?t($=>$.ducc.imported):t($=>$.ducc.not_imported)}</p>
    {data?.username&&<p className="text-muted-foreground">{data.username}</p>}
    {data?.imported_at&&<p className="text-caption text-muted-foreground">{t($=>$.ducc.source)}：{data.machines.find(m=>m.daemon_id===data.source_daemon)?.name||data.source_name||data.source_daemon} · {new Date(data.imported_at).toLocaleString()}</p>}
   </div>
  </SettingsRow>
  <SettingsRow label={t($=>$.ducc.auto)} description={t($=>$.ducc.auto_desc)}>
   <Checkbox aria-label={t($=>$.ducc.auto)} checked={data?.enabled===true} disabled={!data?.available||mutation.isPending||query.isError} onCheckedChange={enabled=>void change({action:"enable",enabled:enabled===true})}/>
  </SettingsRow>
  <SettingsRow label={t($=>$.ducc.computers)} description={t($=>$.ducc.computers_desc)} size="text" align="start">
   <div className="w-full min-w-0 space-y-3">
    <Select items={options} value={selected?.daemon_id??null} onValueChange={value=>setSelectedId(value)} disabled={mutation.isPending||!options.length}>
     <SelectTrigger className="h-auto min-h-8 w-full min-w-0 whitespace-normal data-[size=default]:h-auto [&_[data-slot=select-value]]:line-clamp-none" aria-label={t($=>$.ducc.choose_computer)}><SelectValue className="min-w-0" placeholder={t($=>$.ducc.choose_computer)}>{selected?<span className="whitespace-normal break-all">{selected.name}</span>:undefined}</SelectValue></SelectTrigger>
     <SelectContent align="end" alignItemWithTrigger={false} className="w-[min(36rem,calc(100vw-2rem))]">
      {(data?.machines??[]).map(m=><SelectItem key={m.daemon_id} value={m.daemon_id} className="items-start [&>span]:min-w-0 [&>span]:shrink [&>span]:whitespace-normal"><span className="min-w-0"><span className="block whitespace-normal break-all">{m.name}</span><span className="block text-caption text-muted-foreground">{machineStatus(m)}</span></span></SelectItem>)}
     </SelectContent>
    </Select>
    {selected&&<p className="text-caption text-muted-foreground">{machineStatus(selected)}</p>}
    <Button size="sm" variant="outline" disabled={!selected?.supported||!selected?.online||!selected?.installed||mutation.isPending||query.isError} onClick={()=>selected&&setConfirm({action:"import",daemon_id:selected.daemon_id,name:selected.name})}>{t($=>$.ducc.import_button)}</Button>
    {data?.machines.length===0&&<p className="text-body text-muted-foreground">{t($=>$.ducc.no_computers)}</p>}
   </div>
  </SettingsRow>
  {data?.imported&&<SettingsRow label={t($=>$.ducc.remove)} description={t($=>$.ducc.remove_desc)}><Button size="sm" variant="outline" disabled={mutation.isPending} onClick={()=>setConfirm({action:"remove"})}>{t($=>$.ducc.remove)}</Button></SettingsRow>}
 </SettingsCard>
 <Dialog open={!!confirm} onOpenChange={v=>{if(!v&&!mutation.isPending)setConfirm(null)}}><DialogContent><DialogHeader><DialogTitle>{confirm?.action==="import"?t($=>$.ducc.import_button):t($=>$.ducc.remove)}</DialogTitle><DialogDescription>{confirm?.action==="import"?t($=>$.ducc.import_confirm,{name:confirm.name||""}):t($=>$.ducc.remove_desc)}</DialogDescription></DialogHeader><DialogFooter><Button variant="outline" disabled={mutation.isPending} onClick={()=>setConfirm(null)}>{t($=>$.ducc.cancel)}</Button><Button disabled={mutation.isPending} onClick={()=>confirm&&void change(confirm)}>{t($=>$.ducc.confirm)}</Button></DialogFooter></DialogContent></Dialog>
 </SettingsSection>
}
