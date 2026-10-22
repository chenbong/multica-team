// Only this lab adapter uses fixtures for account/session metadata. Files and
// context are ALWAYS fetched over HTTP -> server WS -> separate daemon -> disk.
import type { ApiClient } from "@multica/core/api/client";
import type { Agent, ChatSession, Workspace } from "@multica/core/types";
import type { WorkspaceContext, WorkspaceQuery, WorkspaceResult } from "@multica/core/types/remote-workspace";
export * from "@multica/core/api/client";
export * from "@multica/core/api/schema";
export { WSClient } from "@multica/core/api/ws-client";
export const workspace = { id: "lab-workspace", slug: "local-lab", name: "本地验证工作区", title: "本地验证工作区" } as unknown as Workspace;
export const agent = {
 id:"lab-agent",workspace_id:"lab-workspace",name:"本地 daemon 验证助手",description:"只验证文件与上下文链路，不运行模型",owner_id:"lab-owner",runtime_id:"lab-runtime",runtime_mode:"local",runtime_config:{},visibility:"private",permission_mode:"private",kind:"user",status:"idle",max_concurrent_tasks:1,archived_at:null,avatar_url:null,
} as unknown as Agent;
export const sessions = ["one","two"].map((suffix,index) => ({
 id:`lab-chat-${suffix}`,workspace_id:"lab-workspace",agent_id:agent.id,creator_id:"lab-owner",title:index===0?"验证远程目录和文件内容":"另一会话：检查隔离",status:"active",created_at:new Date().toISOString(),updated_at:new Date().toISOString(),project_id:null,
})) as ChatSession[];
export async function liveWorkspace(session: string, query: WorkspaceQuery | {operation:"context";binding:string;references:WorkspaceContext["references"]}, signal?: AbortSignal): Promise<WorkspaceResult & {context?:string}> {
 const response=await fetch("/lab-api/workspace",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({...query,session_id:session}),signal});
 const body=await response.json();if(!response.ok)throw new Error(body.error ?? `HTTP ${response.status}`);return body;
}
export const labApi = {
 getBaseUrl:()=>location.origin,getToken:()=>null,
 chatWorkspace:liveWorkspace,
 listWorkspaces:async()=>[workspace],listAgents:async()=>[agent],listMembers:async()=>[],listSquads:async()=>[],listProjects:async()=>[],
 listRuntimes:async()=>[],listAgentRuntimes:async()=>[],listWorkspaceAgentTaskSnapshot:async()=>[],
 getPendingChatTasks:async()=>({tasks:[]}),listChatSessions:async()=>sessions,
};
// Missing metadata methods return explicit empty fixtures, never access an
// external service. All mutating actions other than test send are disabled.
export const api = new Proxy(labApi, {get(target,key){if(key in target)return target[key as keyof typeof target];return async()=>[];}}) as unknown as ApiClient;
