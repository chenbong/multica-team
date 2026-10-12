// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";
import { IssueTransferPlanSchema } from "./issue-transfer-schema";
const id="11111111-1111-4111-8111-111111111111", target="22222222-2222-4222-8222-222222222222";
const plan={issue_id:id,source_workspace_id:id,target_workspace_id:target,source_identifier:"OLD-1",target_name:"Destination",target_slug:"destination",target_prefix:"DST",target_status:"cancelled",statuses:[{key:"cancelled",name:"Cancelled"}],revision:1,comments:2,runs:1,messages:3,unassign:false,retired_subscriptions:0,blockers:[],fingerprint:"a".repeat(64)};
afterEach(()=>vi.unstubAllGlobals());
describe("issue transfer API boundary",()=>{
  it("uses a distinct preview endpoint and preserves confirmation data",async()=>{
    const fetch=vi.fn().mockResolvedValue(new Response(JSON.stringify(plan)));vi.stubGlobal("fetch",fetch);
    const api=new ApiClient("https://api.example.test");
    expect(await api.previewIssueTransfer(id,{target_workspace_id:target})).toEqual(plan);
    expect(String(fetch.mock.calls[0]?.[0])).toContain(`/api/issues/${id}/transfer-preview`);
    expect(JSON.parse(fetch.mock.calls[0]?.[1].body)).toEqual({target_workspace_id:target});
  });
  it("requires a verified result before navigating after a move",async()=>{
    const fetch=vi.fn().mockResolvedValue(new Response(JSON.stringify({...plan,identifier:"DST-3",path:"/destination/issues/DST-3"})));vi.stubGlobal("fetch",fetch);
    const data={target_workspace_id:target,fingerprint:plan.fingerprint,confirm_unassign:true};
    expect((await new ApiClient("https://api.example.test").transferIssue(id,data)).identifier).toBe("DST-3");
    expect(JSON.parse(fetch.mock.calls[0]?.[1].body)).toEqual(data);
  });
  it.each([{}, {...plan,blockers:undefined}, {...plan,unassign:undefined}, {...plan,target_slug:"//external.example"}, {...plan,comments:-1}, {...plan,fingerprint:"invalid"}])("fails closed on malformed preview %#",async(body)=>{
    vi.stubGlobal("fetch",vi.fn().mockResolvedValue(new Response(JSON.stringify(body))));
    await expect(new ApiClient("https://api.example.test").previewIssueTransfer(id,{target_workspace_id:target})).rejects.toThrow("Invalid transfer preview");
  });
  it("rejects responses naming a different destination",async()=>{
    vi.stubGlobal("fetch",vi.fn().mockResolvedValue(new Response(JSON.stringify({...plan,target_workspace_id:id}))));
    await expect(new ApiClient("https://api.example.test").previewIssueTransfer(id,{target_workspace_id:target})).rejects.toThrow();
  });
  it("keeps unknown blockers so the UI cannot silently approve them",()=>{
    expect(IssueTransferPlanSchema.parse({...plan,blockers:["new_guard"]}).blockers).toEqual(["new_guard"]);
  });
});
