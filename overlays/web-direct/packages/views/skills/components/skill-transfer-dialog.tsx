"use client";

import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import type { Skill, SkillSummary } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { useT } from "../../i18n";

type Recipient = { user_id:string;name:string;email:string };
export function SkillTransferDialog({skill,open,onOpenChange,onTransferred}:{skill:SkillSummary;open:boolean;onOpenChange:(open:boolean)=>void;onTransferred?:(skill:Skill)=>void}) {
 const {t}=useT("skills");const qc=useQueryClient();
 const [search,setSearch]=useState("");const [query,setQuery]=useState("");
 const [recipient,setRecipient]=useState<Recipient|null>(null);const [review,setReview]=useState(false);const [saving,setSaving]=useState(false);
 useEffect(()=>{if(open){setSearch("");setQuery("");setRecipient(null);setReview(false)}},[open,skill.id]);
 useEffect(()=>{const timer=setTimeout(()=>setQuery(search.trim()),250);return ()=>clearTimeout(timer)},[search]);
 const ready=open && Array.from(query).length>=2 && query.length<=200;
 const users=useQuery({queryKey:["skill-transfer-recipients",skill.id,query],queryFn:()=>api.skillTransferRecipients(skill.id,query),enabled:ready,retry:false,staleTime:0,gcTime:0});
 async function transfer(){
  if(!recipient || !review || saving)return;
  setSaving(true);
  try{
   const updated=await api.transferSkill(skill.id,recipient.user_id);
   // All workspace lists use the global shared skill identity. Replace cached
   // detail first so the former creator's controls immediately become read-only.
   qc.setQueriesData({queryKey:["workspaces"],predicate:q=>q.queryKey.includes("skills") && q.queryKey.includes(skill.id)},updated);
   onTransferred?.(updated);
   await qc.invalidateQueries({queryKey:["workspaces"]});
   toast.success(t(($)=>$.transfer.saved));onOpenChange(false);
  }catch(error){toast.error(error instanceof Error?error.message:t(($)=>$.transfer.failed))}
  finally{setSaving(false)}
 }
 return <Dialog open={open} onOpenChange={v=>{if(!saving)onOpenChange(v)}}><DialogContent className="w-[calc(100%-2rem)] max-w-md min-w-0 overflow-hidden"><DialogHeader>
  <DialogTitle>{t(($)=>review?$.transfer.confirm_title:$.transfer.title)}</DialogTitle>
  <DialogDescription>{t(($)=>$.transfer.description)}</DialogDescription>
 </DialogHeader>
 {review && recipient ? <p className="break-words text-body">{t(($)=>$.transfer.summary,{skill:skill.name,name:recipient.name,email:recipient.email})}</p> : <div className="min-w-0 space-y-2">
 <label className="text-body" htmlFor="skill-transfer-search">{t(($)=>$.transfer.search)}</label>
 <Input id="skill-transfer-search" value={search} maxLength={200} placeholder={t(($)=>$.transfer.placeholder)} onChange={e=>{setSearch(e.target.value);setRecipient(null)}}/>
 {ready && users.isPending && <p role="status">…</p>}
 {ready && users.isError && <p role="alert">{t(($)=>$.transfer.search_failed)}</p>}
 {ready && !users.isPending && !users.isError && users.data?.length===0 && <p>{t(($)=>$.transfer.empty)}</p>}
 <div role="radiogroup" aria-label={t(($)=>$.transfer.search)} className="max-h-64 space-y-1 overflow-y-auto">{ready && query===search.trim() && users.data?.map(u=><button type="button" role="radio" aria-checked={recipient?.user_id===u.user_id} key={u.user_id} onClick={()=>setRecipient(u)} className="w-full rounded-md border p-2 text-left aria-checked:border-primary aria-checked:bg-accent"><span className="block break-words">{u.name}</span><span className="block break-all text-caption text-muted-foreground">{u.email}</span></button>)}</div>
 </div>}
 <DialogFooter><Button variant="ghost" disabled={saving} onClick={()=>review?setReview(false):onOpenChange(false)}>{t(($)=>review?$.transfer.back:$.transfer.cancel)}</Button><Button disabled={saving || !recipient || (!review && (users.isError || users.isPending || query!==search.trim()))} onClick={()=>review?void transfer():setReview(true)}>{t(($)=>saving?$.transfer.saving:review?$.transfer.confirm:$.transfer.review)}</Button></DialogFooter>
 </DialogContent></Dialog>
}
