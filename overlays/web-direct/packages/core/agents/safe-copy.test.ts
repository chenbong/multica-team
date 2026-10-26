// @vitest-environment node
import {describe,it,expect} from "vitest";
import type {Agent} from "../types";
import {safeCopyDraft} from "./safe-copy";

describe("safe user-agent copy",()=>{
 const source={id:"a",name:"Source",workspace_id:"other",avatar_url:"emoji:old",runtime_id:"private-machine",model:"model",thinking_level:"high",service_tier:"fast",instructions:"Instructions",description:"Description",skills:[{id:"skill"}],permission_mode:"public_to",invocation_targets:[{target_type:"workspace"}],conversation_starters:[],custom_env:{TOKEN:"must-not-copy"},custom_args:["--token=must-not-copy"]} as unknown as Agent;
 it("uses a new avatar and requires explicit runtime while preserving non-secret configuration",()=>{
  const draft=safeCopyDraft(source," copy");
  expect(draft).toMatchObject({avatarUrl:null,runtimeId:"",model:"model",thinkingLevel:"high",serviceTier:"fast",permissionScope:"workspace"});
  expect([...draft.skillIds]).toEqual(["skill"]);
  expect(JSON.stringify(draft)).not.toContain("must-not-copy");
  expect(source.runtime_id).toBe("private-machine");
 });
 it("rejects system agents",()=>expect(()=>safeCopyDraft({...source,system_key:"mika"}," copy")).toThrow());
});
