"use client";
import {useState} from "react";
import {useQuery} from "@tanstack/react-query";
import {api} from "@multica/core/api";
import {useAuthStore} from "@multica/core/auth";
import {Button} from "@multica/ui/components/ui/button";
import {Input} from "@multica/ui/components/ui/input";
import {Dialog,DialogContent,DialogHeader,DialogTitle,DialogDescription} from "@multica/ui/components/ui/dialog";
import {useNavigation} from "../../navigation";
import {useT} from "../../i18n";

export function CopyAgentChooser({blankHref}:{blankHref:string}){
 const {t}=useT("agents");const navigation=useNavigation();
 const userId=useAuthStore(s=>s.user?.id);
 const [open,setOpen]=useState(false),[search,setSearch]=useState("");
 const query=useQuery({queryKey:["agent-copy-sources",userId],queryFn:()=>api.listCopySources(),enabled:open&&!!userId,staleTime:0});
 return <><Button variant="outline" className="mx-auto mt-5" onClick={()=>setOpen(true)}>{t($=>$.platform_expansion.copy)}</Button>
 <Dialog open={open} onOpenChange={setOpen}><DialogContent><DialogHeader><DialogTitle>{t($=>$.platform_expansion.copy)}</DialogTitle><DialogDescription>{t($=>$.platform_expansion.copy_hint)}</DialogDescription></DialogHeader>
 <Input aria-label={t($=>$.platform_expansion.search)} placeholder={t($=>$.platform_expansion.search)} value={search} onChange={e=>setSearch(e.target.value)}/>
 <div className="max-h-80 overflow-y-auto">{query.isPending?<p>{t($=>$.platform_expansion.loading)}</p>:query.isError?<p role="alert">{t($=>$.platform_expansion.failed)}</p>:query.data?.filter(item=>(item.agent.name+" "+item.workspaceName).toLowerCase().includes(search.toLowerCase())).map(item=><Button key={item.agent.id} variant="ghost" className="h-auto w-full justify-between gap-3 py-3" onClick={()=>{const params=new URLSearchParams({duplicate:item.agent.id,duplicate_ws:item.agent.workspace_id});navigation.push(blankHref+(blankHref.includes("?")?"&":"?")+params);}}><span className="truncate">{item.agent.name}</span><span className="truncate text-caption text-muted-foreground">{item.workspaceName}</span></Button>)}</div>
 </DialogContent></Dialog></>;
}
