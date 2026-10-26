"use client";
import {useEffect,useRef,useState} from "react";
import Link from "next/link";
import {usePathname,useSearchParams} from "next/navigation";
import {QueryClient,QueryClientProvider,useQuery,useQueryClient} from "@tanstack/react-query";
import {z} from "zod";
import {api} from "@multica/core/api";
import {useAuthStore} from "@multica/core/auth";
import {useWorkspaceId} from "@multica/core/hooks";
import {LiveActivityQueue,businessRoute,businessVisitKey,readyBusinessPage} from "./live-activity-model";

const statusSchema=z.object({enabled:z.boolean(),collection_enabled:z.boolean().optional().default(false)});
function useLiveStatus(){return useQuery({queryKey:["public-live-status"],queryFn:async()=>{
 try{const response=await fetch("/api/public/live/status",{credentials:"omit",cache:"no-store",signal:AbortSignal.timeout(5000)});if(!response.ok)return {enabled:false,collection_enabled:false};return statusSchema.parse(await response.json());}
 catch{return {enabled:false,collection_enabled:false};}
},retry:false,staleTime:60000,refetchInterval:60000});}

// Mounted only inside the authenticated DashboardGuard, never on /live/login.
export function LiveActivityReporter(){
 const user=useAuthStore(state=>state.user?.id??"");const workspace=useWorkspaceId();
 const pathname=usePathname(),search=useSearchParams(),client=useQueryClient(),status=useLiveStatus();
 const queue=useRef<LiveActivityQueue|null>(null);
 const route=businessRoute(pathname,new URLSearchParams(search.toString()));
 const key=route?.key??pathname;
 useEffect(()=>{const instance=new LiveActivityQueue(event=>api.recordLiveActivity(event));queue.current=instance;return()=>{instance.dispose();queue.current=null;};},[]);
 useEffect(()=>{
  const update=()=>{const queries=client.getQueryCache().getAll();queue.current?.context(user,route?businessVisitKey(route,workspace,queries):key,!!route&&status.data?.collection_enabled===true&&readyBusinessPage(route,workspace,queries));};
  update();return client.getQueryCache().subscribe(update);
 // The stable key intentionally excludes filter queries/fragments.
 // eslint-disable-next-line react-hooks/exhaustive-deps
 },[user,key,workspace,status.data?.collection_enabled,client]);
 useEffect(()=>{
  const onInteraction=(event:Event)=>{if(event.isTrusted&&document.visibilityState==="visible")queue.current?.interaction();};
  const events=["pointerdown","keydown","input","wheel","touchstart"];
  for(const name of events)document.addEventListener(name,onInteraction,{capture:true,passive:true});
  return()=>{for(const name of events)document.removeEventListener(name,onInteraction,true);};
 },[]);
 return null;
}
function DashboardLinkContent(){const {data}=useLiveStatus();return data?.enabled?<Link href="/live" target="_blank" rel="noopener noreferrer" className="text-caption text-muted-foreground underline underline-offset-4">平台实时看板</Link>:null;}
export function LiveDashboardLink(){const [client]=useState(()=>new QueryClient());return <QueryClientProvider client={client}><DashboardLinkContent/></QueryClientProvider>;}
