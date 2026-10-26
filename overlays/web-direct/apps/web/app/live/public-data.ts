"use client";
import {useQuery} from "@tanstack/react-query";
import {z} from "zod";

const time=z.string().refine(value=>/^\d{4}-\d{2}-\d{2}T/.test(value)&&Number.isFinite(Date.parse(value)));
const count=z.number().int().min(0).max(Number.MAX_SAFE_INTEGER).nullable();
const meta={observed_at:time,unavailable_metrics:z.array(z.enum(["active_users","page_views"])),partial_metrics:z.array(z.enum(["active_users","page_views"])).default([])};
const event=z.object({kind:z.enum(["completed","agent_started","created","machine_connected"]),at:time,count:z.number().int().positive(),members:z.array(z.string().regex(/^@[a-z0-9._-]{0,2}\*{3}[a-z0-9._-]?$/i)).max(5)}).strict();
const scale=z.object({users:count,issues:count,agents:count,machines:count}).strict();
const realtime=z.object({...meta,window_start:time,active_users:count,working_agents:count,working_issues:count,working_chats:count,online_machines:count,scale:scale.default({users:null,issues:null,agents:null,machines:null}),events:z.array(event).max(20)}).strict().refine(data=>Math.abs(Date.parse(data.observed_at)-Date.parse(data.window_start)-3600000)<=1);
const recent=z.object({...meta,window_start:time,active_users:count,page_views:count,messages:count,created_issues:count,completed_issues:count}).strict().refine(data=>Math.abs(Date.parse(data.observed_at)-Date.parse(data.window_start)-86400000)<=1);
const day=z.object({date:z.string().regex(/^\d{4}-\d{2}-\d{2}$/),partial:z.boolean().default(false),active_users:count,page_views:count,messages:count,created_issues:count,completed_issues:count}).strict();
const history=z.object({...meta,days:z.array(day).length(7),scale:z.object({users:count,issues:count,agents:count,machines:count}).strict(),capabilities:z.object({multi_agent_issues:count,infoflow_inputs:count,wakeup_triggers:count}).strict()}).strict();
const envelope=<T extends z.ZodType>(data:T)=>z.object({updated_at:time.nullable(),stale:z.boolean(),refresh_seconds:z.number().int().positive(),data:data.nullable()}).strict();
export const liveSchemas={realtime:envelope(realtime),recent:envelope(recent),history:envelope(history)};
type State={realtime:z.infer<typeof liveSchemas.realtime>;recent:z.infer<typeof liveSchemas.recent>;history:z.infer<typeof liveSchemas.history>};
const initial:State={realtime:{updated_at:null,stale:false,refresh_seconds:10,data:null},recent:{updated_at:null,stale:false,refresh_seconds:10,data:null},history:{updated_at:null,stale:false,refresh_seconds:300,data:null}};

export function usePublicLive(enabled:boolean){
 return {realtime:useLiveSection("realtime",liveSchemas.realtime,10,enabled),recent:useLiveSection("recent",liveSchemas.recent,10,enabled),history:useLiveSection("history",liveSchemas.history,300,enabled)};
}
function useLiveSection<K extends keyof State>(key:K,schema:z.ZodType<State[K]>,seconds:number,enabled:boolean):State[K]{
 const query=useQuery({queryKey:["public-live",key],enabled,initialData:initial[key],retry:false,refetchInterval:seconds*1000,refetchIntervalInBackground:false,queryFn:async({signal})=>{
  const response=await fetch(`/api/public/live/${key}`,{credentials:"omit",cache:"no-store",signal:AbortSignal.any([signal,AbortSignal.timeout(10000)])});
  if(!response.ok)throw Error("Public statistics unavailable");
  const value=schema.parse(await response.json());
  // A restarted API without a restored cache must not erase good browser data.
  if(value.data===null)throw Error("Public statistics warming up");
  return value;
 }});
 const value=query.data??initial[key];return schema.parse(query.isError?{...value,stale:true}:value);
}
