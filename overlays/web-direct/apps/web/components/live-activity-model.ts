export type ActivityEvent={event_id:string;page_view:boolean};
const uuid=/^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i;
const resource=/^(?:[a-z][a-z0-9]*-\d+|[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12})$/i;
const roots=new Set(["issues","agents","chat","runtimes","skills","projects","autopilots","squads","members","inbox","my-issues","settings","usage","billing"]);
export function businessRoute(pathname:string,search:URLSearchParams){
 const parts=pathname.split("/").filter(Boolean);
 if(parts.length<2||!roots.has(parts[1]!)||!/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(parts[0]!))return null;
 const section=parts[1]!,tail=parts.slice(2);let id:string|null=null;
 if(section==="chat"&&search.has("session")){id=search.get("session");if(!id||!uuid.test(id))return null;}
 if(tail.length){
  if(section==="agents"&&tail[0]==="new"){
   if(tail.length>1&&!((tail.length===2&&["manual","ai"].includes(tail[1]!))||(tail.length===3&&tail[1]==="ai"&&uuid.test(tail[2]!))))return null;
  }else if(section==="runtimes"&&tail.length===3&&tail[1]==="runtime"&&resource.test(tail[0]!)&&resource.test(tail[2]!)){id=tail[2]!;}
  else if(tail.length===1&&resource.test(tail[0]!)){id=tail[0]!;}else return null;
 }
 return {key:pathname+(section==="chat"&&id?`?session=${id}`:""),section,resource:id};
}
type ActivityQuery={queryKey:readonly unknown[];state:{status:string;data?:unknown};isActive?:()=>boolean};
function issueID(data:unknown){if(typeof data!=="object"||data===null||!("id" in data)||typeof data.id!=="string"||!uuid.test(data.id))return null;return data.id.toLowerCase();}
export function businessVisitKey(route:NonNullable<ReturnType<typeof businessRoute>>,workspace:string,queries:ActivityQuery[]){
 if(route.section!=="issues"||!route.resource)return route.key;
 const id=uuid.test(route.resource)?route.resource.toLowerCase():queries.filter(q=>q.queryKey.includes(workspace)&&q.queryKey.some(k=>typeof k==="string"&&k.toLowerCase()===route.resource!.toLowerCase())).map(q=>issueID(q.state.data)).find(Boolean);
 return id?`${workspace}:issues:${id}`:route.key;
}
export function readyBusinessPage(route:NonNullable<ReturnType<typeof businessRoute>>,workspace:string,queries:ActivityQuery[]){
 const strings=(key:readonly unknown[])=>key.filter((value):value is string=>typeof value==="string").map(value=>value.toLowerCase());
 // Disabled view variants remain "pending" forever in TanStack Query. They
 // are not loading/error pages and must not suppress a completed visible view.
 const active=(q:typeof queries[number])=>q.isActive?.()??true;
 if(route.resource){
  const records=queries.filter(q=>strings(q.queryKey).includes(route.resource!.toLowerCase())&&(route.section!=="issues"||strings(q.queryKey).includes(workspace.toLowerCase())));
  return records.length>0&&records.every(q=>!active(q)||(q.state.status!=="error"&&q.state.status!=="pending"))&&records.some(q=>q.state.status==="success"&&(route.section!=="issues"||!!issueID(q.state.data)));
 }
 const aliases=route.section==="my-issues"?["issues"]:route.section==="usage"?["usage","dashboard"]:route.section==="billing"?["billing","subscription"]:[route.section];
 const records=queries.filter(q=>{const key=strings(q.queryKey);return active(q)&&key.includes(workspace.toLowerCase())&&aliases.some(alias=>key.includes(alias));});
 return records.every(q=>q.state.status!=="error"&&q.state.status!=="pending");
}

export function activityID(){
 if(typeof crypto.randomUUID==="function")return crypto.randomUUID();
 // getRandomValues is available on self-hosted HTTP origins where randomUUID
 // may be unavailable. Never use Math.random for an idempotency identifier.
 const bytes=crypto.getRandomValues(new Uint8Array(16));bytes[6]=(bytes[6]!&15)|64;bytes[8]=(bytes[8]!&63)|128;
 const hex=Array.from(bytes,b=>b.toString(16).padStart(2,"0")).join("");return `${hex.slice(0,8)}-${hex.slice(8,12)}-${hex.slice(12,16)}-${hex.slice(16,20)}-${hex.slice(20)}`;
}

export class LiveActivityQueue{
 private owner="";private page="";private counted=false;private eligible=false;private lastActivity=-Infinity;
 private events:ActivityEvent[]=[];private active=false;private generation=0;private disposed=false;
 private timer:ReturnType<typeof setTimeout>|undefined;
 private retryResolve:(()=>void)|undefined;
 private cancelRetry(){if(this.timer)clearTimeout(this.timer);this.timer=undefined;this.retryResolve?.();this.retryResolve=undefined;}
 constructor(private send:(event:ActivityEvent)=>Promise<void>,private now=Date.now,private id=activityID){}
 context(owner:string,key:string,eligible:boolean){
  if(this.disposed)return;
  if(owner!==this.owner){this.owner=owner;this.events=[];this.generation++;this.lastActivity=-Infinity;this.active=false;this.cancelRetry();this.page="";}
  this.eligible=eligible;
  // Retain the last counted semantic page during loading. In particular,
  // UUID -> issue-number canonical replacement is still the same visit.
  if(!owner||!key||!eligible)return;
  if(key!==this.page){this.page=key;this.counted=false;}
  if(owner&&key&&eligible&&!this.counted){this.counted=true;this.lastActivity=this.now();this.enqueue(true);}
 }
 interaction(){if(!this.disposed&&this.owner&&this.eligible&&this.now()-this.lastActivity>=60000){this.lastActivity=this.now();this.enqueue(false);}}
 private enqueue(page_view:boolean){if(this.events.length>=64)return;this.events.push({event_id:this.id(),page_view});queueMicrotask(()=>void this.flush());}
 private async flush(){
  if(this.active||this.disposed)return;this.active=true;const generation=this.generation;
  while(this.events.length&&!this.disposed&&generation===this.generation){
   const event=this.events[0]!;let sent=false;
   for(let attempt=0;attempt<3&&!this.disposed&&generation===this.generation;attempt++){
    try{await this.send(event);sent=true;break;}catch(error){
     if(this.disposed||generation!==this.generation)break;
     const status=typeof error==="object"&&error!==null&&"status" in error?Number(error.status):0;
     if(status>=400&&status<500&&status!==408&&status!==429)break;
     if(attempt<2)await new Promise<void>(resolve=>{this.retryResolve=resolve;this.timer=setTimeout(()=>{this.timer=undefined;this.retryResolve=undefined;resolve();},(attempt+1)*1000);});
    }
   }
   if(generation!==this.generation)break;
   // Exhausted events are dropped rather than replayed under a later account.
   if(this.events[0]===event)this.events.shift();
   if(!sent&&this.disposed)break;
  }
  if(generation===this.generation)this.active=false;
 }
 dispose(){this.disposed=true;this.generation++;this.events=[];this.cancelRetry();}
}
