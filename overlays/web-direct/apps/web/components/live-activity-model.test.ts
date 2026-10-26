// @vitest-environment node
import {afterEach,describe,expect,it,vi} from "vitest";
import {LiveActivityQueue,businessRoute,businessVisitKey,readyBusinessPage} from "./live-activity-model";
const tick=async()=>{for(let i=0;i<8;i++)await Promise.resolve();};
afterEach(()=>vi.useRealTimers());
describe("business-only visits",()=>{
 it("excludes public, login, errors and unknown pages",()=>{
  for(const p of ["/live","/login","/w/not-a-page","/w/issues/not-found","/w/issues/BH-5/not-found"]){expect(businessRoute(p,new URLSearchParams())).toBeNull();}
 });
 it("does not count filters or fragments as another visit, but changes chat resource",()=>{
  const route=businessRoute("/w/issues",new URLSearchParams("filter=mine"));expect(route?.key).toBe("/w/issues");
  expect(businessRoute("/w/chat",new URLSearchParams("session=00000000-0000-0000-0000-000000000001"))?.key).toContain("?session=");
 });
 it("waits for resource success and rejects missing/error resources",()=>{
  const r=businessRoute("/w/issues/BH-5",new URLSearchParams())!;
  expect(readyBusinessPage(r,"ws",[])).toBe(false);
  for(const status of ["pending","error"]){expect(readyBusinessPage(r,"ws",[{queryKey:["issue","ws","BH-5"],state:{status}}])).toBe(false);}
  expect(readyBusinessPage(r,"ws",[{queryKey:["issue","ws","BH-5"],state:{status:"success",data:{id:"00000000-0000-4000-8000-000000000001"}}}])).toBe(true);
 });
 it("waits for failed or pending list query, ignores unrelated resources",()=>{
  const r=businessRoute("/w/issues",new URLSearchParams())!;
  expect(readyBusinessPage(r,"ws",[{queryKey:["issues","ws"],state:{status:"pending"}}])).toBe(false);
  expect(readyBusinessPage(r,"ws",[{queryKey:["agents","ws"],state:{status:"error"}}])).toBe(true);
 });
 it("does not let disabled list variants suppress a successful visible view",()=>{
  const r=businessRoute("/w/issues",new URLSearchParams())!;
  const records=[{queryKey:["issues","ws","kanban"],state:{status:"success"},isActive:()=>true},{queryKey:["issues","ws","table"],state:{status:"pending"},isActive:()=>false}];
  expect(readyBusinessPage(r,"ws",records)).toBe(true);
  records[1]!.isActive=()=>true;expect(readyBusinessPage(r,"ws",records)).toBe(false);
 });
 it("ignores inactive pending detail observers but still rejects an active resource failure",()=>{
  const r=businessRoute("/w/issues/BH-5",new URLSearchParams())!;
  const records=[{queryKey:["issues","ws","BH-5"],state:{status:"success",data:{id:"00000000-0000-4000-8000-000000000001"}},isActive:()=>true},{queryKey:["issues","ws","BH-5","unused"],state:{status:"pending"},isActive:()=>false}];
  expect(readyBusinessPage(r,"ws",records)).toBe(true);records[0]!.state.status="error";expect(readyBusinessPage(r,"ws",records)).toBe(false);
 });
 it("uses the same semantic visit for UUID and canonical issue URLs, scoped by workspace",()=>{
  const id="00000000-0000-4000-8000-000000000001",alias=businessRoute("/w/issues/BH-5",new URLSearchParams())!,direct=businessRoute(`/w/issues/${id}`,new URLSearchParams())!;
  const queries=[{queryKey:["issues","ws","detail","BH-5"],state:{status:"success",data:{id}}}];
  expect(businessVisitKey(alias,"ws",queries)).toBe(businessVisitKey(direct,"ws",queries));
  expect(readyBusinessPage(alias,"other",queries)).toBe(false);
  expect(readyBusinessPage(alias,"ws",[{queryKey:["issues","ws","BH-5"],state:{status:"success",data:null}}])).toBe(false);
 });
});
describe("idempotent activity queue",()=>{
 it("does not count an issue's loading/canonical replace as a second view",async()=>{
  const send=vi.fn().mockResolvedValue(undefined),q=new LiveActivityQueue(send);
  q.context("u","ws:issue:uuid",true);await tick();q.context("u","/w/issues/BH-5",false);q.context("u","ws:issue:uuid",true);await tick();expect(send).toHaveBeenCalledTimes(1);
  q.context("u","other",false);q.context("u","other",true);await tick();q.context("u","ws:issue:uuid",true);await tick();expect(send).toHaveBeenCalledTimes(3);q.dispose();
 });
 it("counts navigation, back and deliberate new tabs separately",async()=>{
  let n=0;const send=vi.fn().mockResolvedValue(undefined),q=new LiveActivityQueue(send,Date.now,()=>String(++n));
  q.context("u","a",true);q.context("u","a",true);await tick();q.context("u","b",true);await tick();q.context("u","a",true);await tick();
  const tab=new LiveActivityQueue(send);tab.context("u","a",true);await tick();
  expect(send).toHaveBeenCalledTimes(4);expect(new Set(send.mock.calls.map(([v])=>v.event_id)).size).toBe(4);q.dispose();tab.dispose();
 });
 it("limits interaction reports to actual one-minute activity and does not heartbeat",async()=>{
  let now=0;const send=vi.fn().mockResolvedValue(undefined),q=new LiveActivityQueue(send,()=>now);
  q.context("u","a",true);await tick();q.interaction();now=59999;q.interaction();await tick();expect(send).toHaveBeenCalledTimes(1);
  now=60000;q.interaction();await tick();expect(send.mock.calls[1]![0].page_view).toBe(false);
  now=999999;await tick();expect(send).toHaveBeenCalledTimes(2);q.context("u","a",false);q.interaction();await tick();expect(send).toHaveBeenCalledTimes(2);q.dispose();
 });
 it("retries exactly the same event identifier, not another PV",async()=>{
  vi.useFakeTimers();const send=vi.fn().mockRejectedValueOnce(Error()).mockResolvedValue(undefined),q=new LiveActivityQueue(send);
  q.context("u","a",true);await tick();await vi.advanceTimersByTimeAsync(1000);
  expect(send).toHaveBeenCalledTimes(2);expect(send.mock.calls[0]![0]).toBe(send.mock.calls[1]![0]);q.dispose();
 });
 it("cancels pending retries on logout without replaying under another account",async()=>{
  vi.useFakeTimers();const send=vi.fn().mockRejectedValue(Error()),q=new LiveActivityQueue(send);
  q.context("u","a",true);await tick();q.context("","",false);await vi.advanceTimersByTimeAsync(10000);expect(send).toHaveBeenCalledTimes(1);q.dispose();expect(vi.getTimerCount()).toBe(0);
 });
 it("does not retry permanent authentication failures",async()=>{
  vi.useFakeTimers();const send=vi.fn().mockRejectedValue({status:401}),q=new LiveActivityQueue(send);q.context("u","a",true);await tick();await vi.advanceTimersByTimeAsync(10000);expect(send).toHaveBeenCalledTimes(1);q.dispose();
 });
 it("StrictMode cleanup discards an unsent mount, and never collects while disabled",async()=>{
  const send=vi.fn().mockResolvedValue(undefined),old=new LiveActivityQueue(send);old.context("u","a",true);old.dispose();const q=new LiveActivityQueue(send);q.context("u","a",false);await tick();expect(send).not.toHaveBeenCalled();q.context("u","a",true);await tick();expect(send).toHaveBeenCalledTimes(1);expect(Object.keys(send.mock.calls[0]![0]).sort()).toEqual(["event_id","page_view"]);q.dispose();
 });
});
