"use client";

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import type { PersonalSecretMetadata } from "@multica/core/api/platform-expansion";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from "@multica/ui/components/ui/dialog";
import { SettingsSection, SettingsCard } from "./settings-layout";
import { useT } from "../../i18n";

export function PersonalSecretsSection() {
 const {t}=useT("settings");
 const userId=useAuthStore(s=>s.user?.id);
 const key=["personal-secrets",userId];
 const qc=useQueryClient();
 const query=useQuery({queryKey:key,queryFn:()=>api.listPersonalSecrets(),enabled:!!userId,staleTime:0});
 const [open,setOpen]=useState(false);
 const [edit,setEdit]=useState<PersonalSecretMetadata|null>(null);
 const [name,setName]=useState(""),[description,setDescription]=useState(""),[value,setValue]=useState("");
 const [busy,setBusy]=useState(false),[error,setError]=useState("");
 const [revealed,setRevealed]=useState("");
 const [deleting,setDeleting]=useState<PersonalSecretMetadata|null>(null);
 const reset=()=>{setOpen(false);setValue("");setRevealed("");setError("");};
 const action=async(fn:()=>Promise<unknown>)=>{setBusy(true);setError("");try{await fn();await qc.invalidateQueries({queryKey:key});return true;}catch{setError(t($=>$.platform_expansion.failed));return false;}finally{setBusy(false);}};
 return <SettingsSection title={t($=>$.platform_expansion.secrets)} action={<Button size="sm" onClick={()=>{setEdit(null);setName("");setDescription("");setValue("");setRevealed("");setError("");setOpen(true);}}>{t($=>$.platform_expansion.add)}</Button>}>
  <p className="text-caption text-muted-foreground">{t($=>$.platform_expansion.secret_warning)}</p>
  <SettingsCard>
   {query.isError?<p role="alert">{t($=>$.platform_expansion.failed)}</p>:query.isPending?<p>{t($=>$.platform_expansion.loading)}</p>:query.data?.secrets.length===0?<p className="p-4 text-muted-foreground">{t($=>$.platform_expansion.empty)}</p>:query.data?.secrets.map(item=><div key={item.id} className="flex flex-wrap items-center justify-between gap-3 border-b p-4 last:border-0"><div className="min-w-0"><p className="break-all font-medium">{item.name}</p><p className="break-all text-caption text-muted-foreground">{item.description}</p></div><div className="flex gap-2"><Button variant="outline" size="sm" onClick={()=>{setEdit(item);setName(item.name);setDescription(item.description);setValue("");setRevealed("");setError("");setOpen(true);}}>{t($=>$.platform_expansion.edit)}</Button><Button variant="ghost" size="sm" onClick={()=>{setError("");setDeleting(item);}}>{t($=>$.platform_expansion.remove)}</Button></div></div>)}
  </SettingsCard>
  <Dialog open={open} onOpenChange={next=>{if(!busy&&!next)reset();}}>
   <DialogContent className="grid-cols-[minmax(0,1fr)]"><DialogHeader className="min-w-0 pr-6"><DialogTitle>{edit?t($=>$.platform_expansion.edit_secret):t($=>$.platform_expansion.add_secret)}</DialogTitle><DialogDescription className="[overflow-wrap:anywhere]">{t($=>$.platform_expansion.secret_warning)}</DialogDescription></DialogHeader>
    <label className="min-w-0">{t($=>$.platform_expansion.name)}<Input value={name} maxLength={120} autoComplete="off" onChange={e=>setName(e.target.value)}/></label>
    <label className="min-w-0">{t($=>$.platform_expansion.description)}<Input value={description} maxLength={4000} autoComplete="off" onChange={e=>setDescription(e.target.value)}/></label>
    <label className="min-w-0">{t($=>$.platform_expansion.value)}<Textarea className="min-w-0 max-w-full field-sizing-fixed" value={value} rows={5} autoComplete="off" spellCheck={false} placeholder={edit?t($=>$.platform_expansion.keep_value):""} onChange={e=>setValue(e.target.value)}/></label>
    {edit&&<Button variant="outline" disabled={busy} onClick={()=>void action(async()=>{const data=await api.readPersonalSecret(edit.id);setRevealed(data.value);})}>{t($=>$.platform_expansion.reveal)}</Button>}
    {revealed&&<pre className="min-w-0 max-w-full max-h-40 overflow-auto whitespace-pre-wrap break-all rounded border p-3">{revealed}</pre>}
    {error&&<p role="alert" className="text-destructive">{error}</p>}
    <DialogFooter><Button variant="outline" disabled={busy} onClick={reset}>{t($=>$.platform_expansion.cancel)}</Button><Button disabled={busy||!name.trim()||(!edit&&!value)} onClick={()=>void action(async()=>{await api.savePersonalSecret(edit?.id??null,{name,description,...(value?{value}:{})});reset();})}>{t($=>$.platform_expansion.save)}</Button></DialogFooter>
   </DialogContent>
  </Dialog>
  <Dialog open={!!deleting} onOpenChange={next=>{if(!next&&!busy)setDeleting(null);}}><DialogContent className="grid-cols-[minmax(0,1fr)]"><DialogHeader className="min-w-0 pr-6"><DialogTitle className="[overflow-wrap:anywhere]">{t($=>$.platform_expansion.remove)} {deleting?.name}</DialogTitle><DialogDescription className="[overflow-wrap:anywhere]">{t($=>$.platform_expansion.delete_warning)}</DialogDescription></DialogHeader>{error&&<p role="alert">{error}</p>}<DialogFooter><Button variant="outline" disabled={busy} onClick={()=>setDeleting(null)}>{t($=>$.platform_expansion.cancel)}</Button><Button variant="destructive" disabled={busy} onClick={()=>void action(async()=>{if(deleting)await api.deletePersonalSecret(deleting.id);setDeleting(null);})}>{t($=>$.platform_expansion.remove)}</Button></DialogFooter></DialogContent></Dialog>
 </SettingsSection>;
}
