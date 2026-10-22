// TEST-ONLY API boundary for the unmodified Next.js application and controller.
// Bind loopback; never proxy arbitrary URLs or real accounts. No model or DB.
import http from 'node:http';
import {readFileSync} from 'node:fs';
import {createRequire} from 'node:module';
import {randomUUID} from 'node:crypto';
const require=createRequire(process.env.TEAMMESH_LAB_WEB_STAGE+'/apps/web/package.json');
const {WebSocketServer}=require('next/dist/compiled/ws');
const token=readFileSync(process.env.TEAMMESH_LAB_TOKEN_FILE,'utf8').trim();
const wsID='11111111-1111-4111-8111-111111111111',userID='22222222-2222-4222-8222-222222222222',agentID='33333333-3333-4333-8333-333333333333',runtimeID='44444444-4444-4444-8444-444444444444';
const sessionIDs=['55555555-5555-4555-8555-555555555551','55555555-5555-4555-8555-555555555552'];
const now=()=>new Date().toISOString();
const user={id:userID,name:'本地测试用户',email:'tester@example.test',avatar_url:null,onboarded_at:now(),onboarding_questionnaire:{source:'other',source_other:'local test'},starter_content_state:'imported',language:'zh-Hans',timezone:'Asia/Shanghai',created_at:now(),updated_at:now()};
const workspace={id:wsID,slug:'local-lab',name:'本地验证工作区',description:'Test API only',context:null,settings:{},repos:[],issue_prefix:'LAB',avatar_url:null,created_at:now(),updated_at:now()};
const member={id:randomUUID(),workspace_id:wsID,user_id:userID,role:'owner',name:user.name,email:user.email,avatar_url:null,created_at:now()};
const agent={id:agentID,workspace_id:wsID,name:'远程文件验证助手',description:'本地接口模拟，不调用模型',owner_id:userID,runtime_id:runtimeID,runtime_mode:'local',runtime_config:{},permission_mode:'private',visibility:'private',kind:'user',status:'idle',max_concurrent_tasks:1,archived_at:null,avatar_url:null,instructions:'',custom_env:{},custom_args:[],mcp_config:[],model:null,thinking_level:null,runtime_availability:'online',created_at:now(),updated_at:now()};
const sessions=sessionIDs.map((id,index)=>({id,workspace_id:wsID,agent_id:agentID,creator_id:userID,title:index?'另一会话：验证目录隔离':'远程文件工作区验收',status:'active',project_id:null,has_unread:false,created_at:now(),updated_at:now()}));
const messages=new Map(sessions.map(session=>[session.id,[{id:randomUUID(),chat_session_id:session.id,role:'assistant',task_id:null,created_at:now(),content:'## 原版 Web 应用 · 本地接口验证\n\n当前使用仓库原有的导航、页面布局、聊天控制逻辑、消息列表和输入框，不再是手写外壳。\n\n**仅 API 返回测试账号、会话数据；文件通过独立 daemon 读取临时磁盘目录。**\n\n点击「文件」体验代码工作区。添加文件后发送，返回的是经过 daemon 校验的上下文，**不是 AI 模型回答**。\n\n此环境不连接线上服务、不使用生产数据库。'}]]));
const sockets=new Set();
const emit=(type,payload)=>{for(const ws of sockets)if(ws.readyState===1)ws.send(JSON.stringify({type,payload,workspace_id:wsID}));};
async function daemonCall(id,query){
 const index=sessionIDs.indexOf(id);if(index<0)throw Object.assign(new Error('unknown_test_session'),{status:403});
 const response=await fetch('http://127.0.0.1:4181/lab/workspace',{method:'POST',headers:{Authorization:'Bearer '+token,'Content-Type':'application/json'},body:JSON.stringify({...query,session_id:index?'lab-chat-two':'lab-chat-one'}),signal:AbortSignal.timeout(10000)});
 const result=await response.json();if(!response.ok)throw Object.assign(new Error(result.error),{status:response.status});return result;
}
function json(res,status,value){res.writeHead(status,{'Content-Type':'application/json','Cache-Control':'no-store'});res.end(status===204?'':JSON.stringify(value));}
const server=http.createServer(async(req,res)=>{
 const url=new URL(req.url,'http://127.0.0.1:4183');const p=url.pathname;
 if(!['127.0.0.1:4182','localhost:4182','127.0.0.1:4183','localhost:4183'].includes(req.headers.host)){json(res,403,{error:'loopback_host_required'});return}
 // Next same-origin proxy forwards browser Origin. Reject every non-lab origin.
 if(req.headers.origin && !['http://127.0.0.1:4182','http://localhost:4182'].includes(req.headers.origin)){json(res,403,{error:'cross_origin_denied'});return}
 let body={};try{let text='';for await(const chunk of req){text+=chunk;if(Buffer.byteLength(text)>512*1024){json(res,413,{error:'body_too_large'});return}}if(text)body=JSON.parse(text);
 console.log(req.method,p);
 if(/^\/api\/tasks\/[^/]+\/messages$/.test(p)){json(res,200,[]);return}
 if(p==='/api/me'){json(res,200,user);return}
 if(p==='/api/config'){json(res,200,{allow_signup:false,google_client_id:'',feature_flags:{},server_version:'local-api-lab',agent_conversation_starters_supported:true});return}
 if(p==='/api/workspaces'){json(res,200,[workspace]);return}
 if(p===`/api/workspaces/${wsID}`){json(res,200,workspace);return}
 if(p.endsWith('/members')){json(res,200,[member]);return}
 if(p==='/api/agents'){json(res,200,[agent]);return}
 if(p===`/api/agents/${agentID}`){json(res,200,agent);return}
 if(p==='/api/runtimes'){json(res,200,[{id:runtimeID,workspace_id:wsID,owner_id:userID,name:'临时目录 daemon',daemon_id:'lab-daemon',provider:'codex',runtime_mode:'local',status:'online',last_seen_at:now(),metadata:{capabilities:['remote-workspace-v1']},created_at:now(),updated_at:now()}]);return}
 if(p==='/api/chat/sessions' && req.method==='GET'){json(res,200,sessions);return}
 const match=p.match(/^\/api\/chat\/sessions\/([^/]+)(.*)$/);
 if(match){const id=match[1],suffix=match[2],session=sessions.find(s=>s.id===id);if(!session){json(res,404,{error:'unknown_test_session'});return}
  if(suffix==='/workspace' && req.method==='POST'){if(!['list','read'].includes(body.operation)){json(res,400,{error:'invalid_operation'});return}json(res,200,await daemonCall(id,body));return}
  if(suffix==='/messages/page'){json(res,200,{messages:messages.get(id),limit:50,has_more:false,next_cursor:null});return}
  if(suffix==='/messages' && req.method==='GET'){json(res,200,messages.get(id));return}
  if(suffix==='/messages' && req.method==='POST'){
   if(typeof body.content!=='string'||!body.content.trim()){json(res,400,{error:'content_required'});return}
   const context=body.workspace_context?await daemonCall(id,{operation:'context',binding:body.workspace_context.binding,references:body.workspace_context.references}):null;
   const taskID=randomUUID(),messageID=randomUUID(),created=now();
   const content=body.content+(context?.context??'');
   const userMessage={id:messageID,chat_session_id:id,role:'user',content,task_id:taskID,created_at:created};messages.get(id).push(userMessage);
   json(res,201,{message_id:messageID,task_id:taskID,created_at:created,content,supports_queue:true,queued:false,attachment_ids:[]});
   setTimeout(()=>{
    const reply={id:randomUUID(),chat_session_id:id,role:'assistant',task_id:taskID,created_at:now(),content:context?.context?'**文件链路验证成功（非模型回答）**\n\n```text\n'+context.context+'\n```':'测试消息已收到。此环境不运行 AI 模型。'};
    messages.get(id).push(reply);session.last_message={content:reply.content,role:'assistant',created_at:reply.created_at};session.updated_at=reply.created_at;
    emit('chat:done',{...reply,message_id:reply.id,elapsed_ms:0});
   },500);return;
  }
  if(suffix==='/pending-task'){json(res,200,{});return}
  if(suffix==='/draft-restores'){json(res,200,{restores:[]});return}
  if(suffix==='/read'){json(res,204,null);return}
  if(suffix===''){json(res,200,session);return}
 }
 if(p.endsWith('/web-panels')){json(res,200,{panels:[],can_add:false,limit:50});return}
 if(p==='/api/issue-statuses'){json(res,200,{statuses:[],categories:['unstarted','started','done','closed'],total:0});return}
 if(p==='/api/notification-preferences'){json(res,200,{workspace_id:wsID,preferences:{}});return}
 if(p==='/api/search-index/snapshot'){json(res,200,{issues:[],comments:[],projects:[],next_after_number:0,done:true});return}
 if(p==='/api/search-index/changes'){json(res,200,{issues:[],comments:[],projects:[],deleted:{issues:[],comments:[],projects:[]},cursor:'local-0',has_more:false});return}
 if(p==='/api/me/notifications/preferences'){json(res,200,{});return}
 if(p==='/api/chat/pending-tasks'){json(res,200,{tasks:[]});return}
 if(p==='/api/inbox/unread-count'){json(res,200,{count:0});return}
 if(p.includes('/search-index/manifest')){json(res,200,{cursor:'local-0',issue_count:0,comment_count:0,project_count:0,text_bytes:0});return}
 if(req.method==='GET' && /(projects|squads|skills|labels|issue-statuses|issue-views|properties|pins|resource-web-panels|web-panels|unread-summary|agent-task-snapshot|pinned-agents|invitations|integrations)/.test(p)){json(res,200,[]);return}
 if(req.method==='POST' && ['/api/client-usage','/api/client-usage/runtime-probe'].includes(p)){json(res,204,null);return}
 console.log('UNHANDLED',req.method,p);json(res,404,{error:'not_implemented_in_local_fixture'});
 }catch(error){console.error('API',error.message);json(res,error.status??502,{error:error.message})}
});
const wss=new WebSocketServer({noServer:true});
server.on('upgrade',(req,socket,head)=>{if(!req.url.startsWith('/ws') || req.headers.origin && !['http://127.0.0.1:4182','http://localhost:4182'].includes(req.headers.origin)){socket.destroy();return}wss.handleUpgrade(req,socket,head,ws=>{sockets.add(ws);ws.on('close',()=>sockets.delete(ws));});});
server.listen(4183,'127.0.0.1',()=>console.log('Original-Web API fixture on loopback 4183; no UI/controller replacements'));
