"use client";
import {useState} from "react";
import {useQuery} from "@tanstack/react-query";
import {api} from "@multica/core/api";
import {useWorkspaceId} from "@multica/core/hooks";
import {runtimeListOptions,runtimeModelsOptions} from "@multica/core/runtimes";
import type {MemberWithUser,UpdateAgentRequest} from "@multica/core/types";
import {Button} from "@multica/ui/components/ui/button";
import {Dialog,DialogContent,DialogHeader,DialogTitle,DialogDescription,DialogFooter} from "@multica/ui/components/ui/dialog";
import {RuntimePicker} from "./inspector/runtime-picker";
import type {AgentListRow} from "./agents-page";
import {bulkRuntimePlan,runtimePlanFingerprint} from "./bulk-runtime-plan";
import {useT} from "../../i18n";

type Planned={id:string;name:string;fingerprint:string;update:UpdateAgentRequest|null;message:string;previous?:string};
export function BulkRuntimeChange({rows,members,currentUserId,onDone}:{rows:AgentListRow[];members:MemberWithUser[];currentUserId:string|null;onDone:()=>void}){
 const {t}=useT("agents");const wsId=useWorkspaceId();
 const [open,setOpen]=useState(false),[runtimeId,setRuntimeId]=useState("");
 const [plan,setPlan]=useState<Planned[]|null>(null),[busy,setBusy]=useState(false),[result,setResult]=useState<string[]|null>(null),[error,setError]=useState("");
 const runtimes=useQuery({...runtimeListOptions(wsId),enabled:open});
 const target=runtimes.data?.find(r=>r.id===runtimeId);
 const models=useQuery(runtimeModelsOptions(open&&target?.status==="online"?runtimeId:null));
 const catalog=models.isSuccess?(models.data.supported?models.data.models:[]):null;
 const reset=()=>{setPlan(null);setResult(null);setError("");};
 const preview=async()=>{if(!target)return;setBusy(true);setError("");try{
   const next:Planned[]=[];
   for(const row of rows){
    if(!row.canManage||row.agent.archived_at){next.push({id:row.agent.id,name:row.agent.name,fingerprint:"",update:null,message:t($=>$.platform_expansion.skipped)});continue;}
    const latest=await api.getAgent(row.agent.id);
    if(latest.archived_at){next.push({id:latest.id,name:latest.name,fingerprint:"",update:null,message:t($=>$.platform_expansion.skipped)});continue;}
    next.push({id:latest.id,name:latest.name,fingerprint:runtimePlanFingerprint(latest),update:bulkRuntimePlan(latest,runtimes.data?.find(r=>r.id===latest.runtime_id),target,catalog),message:"",previous:[latest.model||t($=>$.platform_expansion.defaults),latest.thinking_level||t($=>$.platform_expansion.defaults),latest.service_tier||t($=>$.platform_expansion.defaults)].join(" · ")});
   }setPlan(next);
 }catch{setError(t($=>$.platform_expansion.failed));}finally{setBusy(false);}};
 const apply=async()=>{if(!plan)return;setBusy(true);setError("");const results:string[]=[];
  for(const item of plan){if(!item.update){results.push(item.name+": "+item.message);continue;}
   try{const latest=await api.getAgent(item.id);if(runtimePlanFingerprint(latest)!==item.fingerprint){results.push(item.name+": "+t($=>$.platform_expansion.changed));continue;}
    await api.updateAgent(item.id,item.update);results.push(item.name+": "+t($=>$.platform_expansion.success));
   }catch{results.push(item.name+": "+t($=>$.platform_expansion.failed));}
  }setResult(results);setBusy(false);
 };
 return <><Button variant="ghost" size="sm" onClick={()=>{reset();setRuntimeId("");setOpen(true);}}>{t($=>$.platform_expansion.runtime)}</Button>
 <Dialog open={open} onOpenChange={next=>{if(!busy){setOpen(next);if(!next&&result)onDone();}}}><DialogContent className="sm:max-w-xl"><DialogHeader><DialogTitle>{t($=>$.platform_expansion.runtime)}</DialogTitle><DialogDescription>{t($=>$.platform_expansion.runtime_hint)}</DialogDescription></DialogHeader>
 <RuntimePicker value={runtimeId} runtimes={runtimes.data??[]} members={members} currentUserId={currentUserId} variant="field" canEdit={!busy&&!result} onChange={id=>{setRuntimeId(id);reset();}}/>
 {error&&<p role="alert" className="text-destructive">{error}</p>}
 <div className="max-h-72 space-y-3 overflow-auto">{result?result.map((line,index)=><p key={index}>{line}</p>):plan?.map(item=><div key={item.id} className="rounded border p-3"><p className="font-medium">{item.name}</p>{item.update?<p className="break-all text-caption"><span className="text-muted-foreground">{item.previous}</span> → {item.update.model||t($=>$.platform_expansion.defaults)} · {item.update.thinking_level||t($=>$.platform_expansion.defaults)} · {item.update.service_tier||t($=>$.platform_expansion.defaults)}</p>:<p>{item.message}</p>}</div>)}</div>
 <DialogFooter>{result?<Button onClick={()=>{setOpen(false);onDone();}}>{t($=>$.platform_expansion.close)}</Button>:<><Button variant="outline" disabled={busy} onClick={()=>setOpen(false)}>{t($=>$.platform_expansion.cancel)}</Button><Button disabled={busy||!target||(models.isPending&&models.fetchStatus==="fetching")} onClick={()=>void (plan?apply():preview())}>{plan?t($=>$.platform_expansion.confirm):t($=>$.platform_expansion.preview)}</Button></>}</DialogFooter>
 </DialogContent></Dialog></>;
}
