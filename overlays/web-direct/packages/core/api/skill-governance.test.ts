import {afterEach,describe,expect,it,vi} from "vitest";
import {ApiClient} from "./client";
import {SkillSummarySchema,RuntimeLocalSkillConflictSchema} from "./schemas";
const row={id:"skill",workspace_id:"ws",name:"kept",created_by:"outside",creator:{user_id:"outside",name:"Outside creator",avatar_url:null}};
afterEach(()=>vi.unstubAllGlobals());
describe("skill governance API boundary",()=>{
 it("rejects malformed local-import creator and scope metadata without granting overwrite",()=>{
 const value=RuntimeLocalSkillConflictSchema.parse({existing_creator_name:{bad:true},existing_scope:[],can_overwrite:"yes"});expect(value.can_overwrite).toBe(false);expect(value.existing_creator_name).toBeUndefined();expect(value.existing_scope).toBeUndefined();
 });
 it("retains a creator summary without requiring a workspace member",()=>{
  expect(SkillSummarySchema.parse(row).creator?.name).toBe("Outside creator");
 });
 it("degrades malformed creator metadata without losing the skill",()=>{
  const result=SkillSummarySchema.parse({...row,creator:{name:13}});expect(result.id).toBe("skill");expect(result.creator).toBeNull();
 });
 it("sends an optional replacement URL while preserving the returned identity",async()=>{
  const fetcher=vi.fn().mockResolvedValue(new Response(JSON.stringify({...row,content:"new body",files:[]}),{status:200,headers:{"Content-Type":"application/json"}}));vi.stubGlobal("fetch",fetcher);
  const skill=await new ApiClient("https://api.example.test").refreshSkill("skill",{url:"https://github.com/acme/skills/tree/main/helper"});
  expect(skill.name).toBe("kept");expect(skill.creator?.name).toBe("Outside creator");
  expect(JSON.parse(String(fetcher.mock.calls[0]?.[1]?.body))).toEqual({url:"https://github.com/acme/skills/tree/main/helper"});
 });
});
