import {useState} from 'react';
import {useChatStore} from '@multica/core/chat';
import {useQueryClient} from '@tanstack/react-query';
import type {Attachment,ChatMessage} from '@multica/core/types';
import type {WorkspaceContext} from '@multica/core/types/remote-workspace';
import {agent,sessions,liveWorkspace} from './api';
import {toast} from 'sonner';
import {workspaceError} from '../chat/components/remote-workspace-state';
const initial=(session:string):ChatMessage[]=>[{
 id:'welcome-'+session,chat_session_id:session,role:'assistant',created_at:new Date().toISOString(),task_id:null,
 content:'## 本地真实文件链路验证\n\n这里使用项目原有的 **ChatPage、会话列表、消息列表、富文本输入框**。\n\n点击「文件」，左侧会话列表会收起，给代码工作区留空间。打开 `src/auth/login.ts`，右键添加文件或选择代码后发送。\n\n**文件来自独立 daemon 进程的临时磁盘目录，不是写死的 API 数据。**\n\n发送只回显经 daemon 校验的上下文，不调用模型，不接生产数据库。',
}];
const saved:Record<string,ChatMessage[]>={};
export function useChatController(_options:unknown){
 const selected=useChatStore(s=>s.activeSessionId) ?? sessions[0]!.id;
 const setActiveSession=useChatStore(s=>s.setActiveSession);
 const [,render]=useState(0);const qc=useQueryClient();
 const messages=saved[selected]??initial(selected);
 const noop=()=>{};
 return {
 wsId:'lab-workspace',user:{id:'lab-owner'},agents:[agent],availableAgents:[agent],agentsSettled:true,sessions,projects:[],
 activeSessionId:selected,selectedAgentId:agent.id,activeProjectId:null,projectContextUnsupported:false,isProjectUpdating:false,
 currentSession:sessions.find(s=>s.id===selected)!,isSessionArchived:false,isAgentArchived:false,isAgentAccessRevoked:false,isAgentRuntimeBound:true,
 activeAgent:agent,customizeConversationStartersHref:'',noAgent:false,availability:'online' as const,
 messages,pendingTask:null,pendingTaskId:null,showSkeleton:false,hasMessages:true,firstItemIndex:10000,hasOlderMessages:false,isFetchingOlderMessages:false,fetchOlderMessages:noop,
 restoreDraftRequest:null,handleRestoreDraftApplied:noop,focusInputRequest:0,conversationStarterRequest:null,handleConversationStarterApplied:noop,prefillConversationStarter:noop,
 handleSend:async(content:string,_ids?:string[],commit?:()=>void,_attachments?:Attachment[],context?:WorkspaceContext)=>{
  const session=selected;
  try {
   const reply=context?await liveWorkspace(session,{operation:'context',binding:context.binding,references:context.references}):null;
   const now=new Date().toISOString();const base=saved[session]??initial(session);
   saved[session]=[...base,{id:crypto.randomUUID(),chat_session_id:session,role:'user',content,created_at:now,task_id:null},
    {id:crypto.randomUUID(),chat_session_id:session,role:'assistant',content:reply?.context?`**daemon 已读取并验证以下真实文件上下文（非模型回答）：**\n\n${"```text\n" + reply.context + "\n```"}`:'这是测试发送，没有调用 AI 模型。请添加文件或选区后再次发送。',created_at:now,task_id:null}];
   commit?.();render(n=>n+1);void qc.invalidateQueries({queryKey:['lab-messages']});return true;
  }catch(error){toast.error(workspaceError(error));return false;}
 },
 handleStop:noop,handleSendQueuedTaskNow:noop,handleEditQueuedTask:noop,handleRemoveQueuedTask:noop,handleClearQueuedTasks:noop,uploadEnabled:false,
 handleNewChat:()=>toast.info('验证环境使用两条固定会话'),handleStartNewChat:()=>toast.info('验证环境使用两条固定会话'),
 handleSelectSession:(session:{id:string})=>setActiveSession(session.id),handleProjectChange:noop,advanceSelectionAfterArchive:noop,archiveSession:()=>toast.info('验证环境不提供归档操作'),setActiveSession,setSelectedAgentId:noop,
 };
}
