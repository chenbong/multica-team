import {afterEach,describe,expect,it,vi} from "vitest";
import {act,cleanup,render as rawRender,screen,waitFor} from "@testing-library/react";
import {QueryClient,QueryClientProvider} from "@tanstack/react-query";
import type {ReactNode} from "react";
import {LiveDashboardPreview} from "./preview";
import {liveSchemas} from "./public-data";
import LiveLayout from "./layout";
const render=(ui:ReactNode)=>rawRender(<QueryClientProvider client={new QueryClient()}>{ui}</QueryClientProvider>);

afterEach(()=>{cleanup();vi.unstubAllGlobals();});
describe("read-only live data boundary",()=>{
 it("shows accumulated values with collecting badges instead of blank partial windows",async()=>{
  const at="2026-10-23T06:00:00Z";
  vi.stubGlobal("fetch",vi.fn(async(url:string)=>({ok:true,json:async()=>url.endsWith("recent")?{updated_at:at,stale:false,refresh_seconds:10,data:{observed_at:at,window_start:"2026-10-22T06:00:00Z",active_users:12,page_views:723,messages:3,created_issues:2,completed_issues:1,unavailable_metrics:[],partial_metrics:["active_users","page_views"]}}:{updated_at:null,stale:false,refresh_seconds:10,data:null}})));
  render(<LiveDashboardPreview mode="live"/>);
  await waitFor(()=>expect(screen.getByText("723")).toBeInTheDocument());
  expect(screen.getAllByText("采集中")).toHaveLength(2);
  expect(screen.getByText("723").closest(".tm-live-todayCard")).toHaveTextContent("页面浏览量采集中");
 });
 it("never falls back to demo counts when live data is unavailable",async()=>{
  const request=vi.fn(async()=>({ok:true,json:async()=>({updated_at:null,stale:false,refresh_seconds:15,data:null})}));vi.stubGlobal("fetch",request);
  render(<LiveDashboardPreview mode="live" readOnlyPreview/>);
  await waitFor(()=>expect(request).toHaveBeenCalledTimes(3));
  expect(screen.getByRole("note")).toHaveTextContent("真实聚合数据");
  expect(screen.queryByText("1,268")).not.toBeInTheDocument();
  expect(screen.queryByText("演示状态 · 非实时数据")).not.toBeInTheDocument();
  expect(screen.queryByRole("region",{name:"本地预览控制"})).not.toBeInTheDocument();
  expect(screen.getAllByText("—").length).toBeGreaterThan(5);
  for(const args of request.mock.calls)expect(args).toHaveLength(2);
 });
 it("rejects raw usernames, invalid rolling windows and unexpected response fields",()=>{
  const observed_at="2026-10-22T12:00:00Z";
  const value={updated_at:observed_at,stale:false,refresh_seconds:15,data:{observed_at,window_start:"2026-10-22T11:00:00Z",active_users:null,working_agents:1,working_issues:1,working_chats:0,online_machines:1,events:[{kind:"completed",at:observed_at,count:1,members:["@ex***e"]}],unavailable_metrics:["active_users"]}};
  expect(liveSchemas.realtime.safeParse(value).success).toBe(true);
  expect(liveSchemas.realtime.safeParse({...value,email:"private@example.test"}).success).toBe(false);
  expect(liveSchemas.realtime.safeParse({...value,data:{...value.data,events:[{...value.data.events[0],members:["@unmasked-user"]}]}}).success).toBe(false);
  expect(liveSchemas.recent.safeParse({...value,data:{observed_at,window_start:"2026-10-22T00:00:00Z",active_users:null,page_views:null,messages:1,created_issues:1,completed_issues:1,unavailable_metrics:[]}}).success).toBe(false);
 });
 it("does not show development claims in the production live view",()=>{
  vi.stubGlobal("fetch",vi.fn().mockRejectedValue(Error("offline")));
  render(<LiveDashboardPreview mode="live"/>);
  expect(screen.queryByRole("note")).not.toBeInTheDocument();
  expect(screen.queryByText(/演示数据/)).not.toBeInTheDocument();
 });
 it("keeps public statistics when anonymous auth bootstrap clears the app cache",async()=>{
  const appClient=new QueryClient();
  const at="2026-10-22T12:00:00Z";
  vi.stubGlobal("fetch",vi.fn(async(url:string)=>({ok:true,json:async()=>url.endsWith("recent")?{updated_at:at,stale:false,refresh_seconds:60,data:{observed_at:at,window_start:"2026-10-21T12:00:00Z",active_users:42,page_views:12345,messages:3,created_issues:2,completed_issues:1,unavailable_metrics:[]}}:{updated_at:null,stale:false,refresh_seconds:15,data:null}})));
  rawRender(<QueryClientProvider client={appClient}><LiveLayout><LiveDashboardPreview mode="live"/></LiveLayout></QueryClientProvider>);
  await waitFor(()=>expect(screen.getByText("12,345")).toBeInTheDocument());
  act(()=>appClient.clear());
  expect(screen.getByText("12,345")).toBeInTheDocument();
 });
 it.skipIf(process.env.LIVE_LOCAL_CONTRACT!=="1")("validates all three local Go response contracts",async()=>{
  for(const key of ["realtime","recent","history"] as const){const response=await fetch(`http://127.0.0.1:19999/api/public/live/${key}`);expect(response.ok).toBe(true);expect(liveSchemas[key].parse(await response.json()).data).not.toBeNull();}
 });
});
