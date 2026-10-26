import type { Agent,AgentRuntime,RuntimeModel,UpdateAgentRequest } from "@multica/core/types";
import {findModelCapabilityEntry} from "./inspector/model-capability";

export function bulkRuntimePlan(agent:Agent,source:AgentRuntime|undefined,target:AgentRuntime,catalog:readonly RuntimeModel[]|null):UpdateAgentRequest {
 let model=agent.model??"",thinking=agent.thinking_level??"",tier=agent.service_tier??"";
 let entry=catalog?findModelCapabilityEntry(catalog,model,target.provider):undefined;
 if(model&&catalog&&!entry){model=catalog.find(m=>m.default)?.id??"";entry=catalog.find(m=>m.id===model);}
 if(catalog===null&&source?.provider!==target.provider){model="";thinking="";tier="";}
 if(entry){
  if(thinking&&!entry.thinking?.supported_levels.some(level=>level.value===thinking))thinking="";
  if(tier&&!entry.service_tiers?.some(level=>level.id===tier)&&!(tier==="default"&&entry.supports_explicit_standard_service_tier))tier="";
 } else if(catalog!==null){thinking="";tier="";}
 return {runtime_id:target.id,model,thinking_level:thinking,service_tier:tier};
}

export function runtimePlanFingerprint(agent:Agent):string {
 return JSON.stringify([agent.id,agent.updated_at,agent.owner_id,agent.archived_at,agent.runtime_id,agent.model,agent.thinking_level,agent.service_tier]);
}
