// @vitest-environment node
import {afterEach,describe,it,expect,vi} from "vitest";
import {ApiClient} from "./client";
import {setCurrentWorkspace,getCurrentSlug} from "../platform/workspace-storage";

afterEach(()=>{vi.unstubAllGlobals();setCurrentWorkspace(null,null);});
describe("cross-workspace copy API scope",()=>{
 const agent={id:"source",workspace_id:"source-ws",name:"Source",avatar_url:null};
 it("clears the route slug when listing another workspace without changing global state",async()=>{
  setCurrentWorkspace("target-workspace","target-ws");
  const mock=vi.fn().mockImplementation(async(url:string)=>new Response(JSON.stringify(url.endsWith("/api/workspaces")?[{id:"source-ws",name:"Source workspace"}]:[agent]),{status:200,headers:{"Content-Type":"application/json"}}));
  vi.stubGlobal("fetch",mock);
  const api=new ApiClient("https://api.example.test");
  const sources=await api.listCopySources();
  expect(sources.map(source=>source.agent.id)).toEqual(["source"]);
  const headers=mock.mock.calls[1]![1].headers;
  expect(headers["X-Workspace-ID"]).toBe("source-ws");
  expect(headers["X-Workspace-Slug"]).toBe("");
  expect(getCurrentSlug()).toBe("target-workspace");
 });
 it("does the same for source detail loading",async()=>{
  setCurrentWorkspace("target-workspace","target-ws");
  const mock=vi.fn().mockResolvedValue(new Response(JSON.stringify(agent),{status:200,headers:{"Content-Type":"application/json"}}));
  vi.stubGlobal("fetch",mock);
  await new ApiClient("https://api.example.test").getCopySource("source-ws","source");
  expect(mock.mock.calls[0]![1].headers["X-Workspace-Slug"]).toBe("");
  expect(getCurrentSlug()).toBe("target-workspace");
 });
});
