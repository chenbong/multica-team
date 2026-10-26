// @vitest-environment node
import {describe,it,expect} from "vitest";
import type {Agent,AgentRuntime,RuntimeModel} from "@multica/core/types";
import {bulkRuntimePlan,runtimePlanFingerprint} from "./bulk-runtime-plan";

describe("bulk runtime plans",()=>{
 const agent={id:"a",runtime_id:"r1",model:"shared-model",thinking_level:"high",service_tier:"fast"} as Agent;
 const source={id:"r1",provider:"claude"} as AgentRuntime;
 const target={id:"r2",provider:"codex"} as AgentRuntime;
 const models=[{id:"shared-model",label:"shared",thinking:{supported_levels:[{value:"high",label:"High"}]},service_tiers:[{id:"fast",name:"Fast"}]}] as RuntimeModel[];
 it("preserves supported settings even across provider families",()=>expect(bulkRuntimePlan(agent,source,target,models)).toEqual({runtime_id:"r2",model:"shared-model",thinking_level:"high",service_tier:"fast"}));
 it("resets unsupported settings to target defaults",()=>expect(bulkRuntimePlan(agent,source,target,[{id:"target-default",label:"Default",default:true}])).toEqual({runtime_id:"r2",model:"target-default",thinking_level:"",service_tier:""}));
 it("uses runtime defaults when a cross-provider catalog cannot be read",()=>expect(bulkRuntimePlan(agent,source,target,null)).toEqual({runtime_id:"r2",model:"",thinking_level:"",service_tier:""}));
 it("keeps values during a same-provider move without an authoritative catalog",()=>expect(bulkRuntimePlan(agent,source,{...target,provider:"claude"},null).thinking_level).toBe("high"));
 it("detects configuration changes without including secrets",()=>{
  expect(runtimePlanFingerprint(agent)).not.toBe(runtimePlanFingerprint({...agent,model:"changed"}));
  expect(runtimePlanFingerprint(agent)).toBe(runtimePlanFingerprint({...agent,custom_args:["fixture-secret"]}));
 });
});
