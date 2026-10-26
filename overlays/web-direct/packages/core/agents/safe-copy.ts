import { EMPTY_AGENT_DRAFT, deriveDuplicateAccess, type AgentDraft } from "./draft";
import type { Agent } from "../types";

export function safeCopyDraft(source:Agent,suffix:string):AgentDraft {
 if(source.system_key)throw new Error("Built-in agents cannot be copied");
 return {...EMPTY_AGENT_DRAFT,name:source.name+suffix,description:source.description??"",
  instructions:source.instructions??"",conversationStarters:(source.conversation_starters??[]).map(s=>({...s})),
  avatarUrl:null,runtimeId:"",model:source.model??"",thinkingLevel:source.thinking_level??"",
  serviceTier:source.service_tier??"",skillIds:new Set(source.skills.map(s=>s.id)),
  ...deriveDuplicateAccess(source)};
}
