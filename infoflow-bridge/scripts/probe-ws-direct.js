// Explicit, bounded acceptance probe. Only use after pausing this robot in the
// production Bridge. No agent/CLI calls, task writes, credential or payload logs.
import fs from 'node:fs';
import path from 'node:path';
import {randomUUID,createHash} from 'node:crypto';
import {setTimeout as delay} from 'node:timers/promises';
import {Client} from '@baidu/infoflow-sdk-nodejs';
import {createWSClient} from '../src/ws-transport.js';

const output=process.env.PROBE_OUTPUT;
if(!output||!path.isAbsolute(output))throw Error('Protected absolute PROBE_OUTPUT required');
const configPath=process.env.INFOFLOW_CONFIG_PATH;
const robotId=process.env.PROBE_ROBOT_ID;
const user=process.env.PROBE_USER;
const wsGateway=process.env.PROBE_WS_GATEWAY;
const wsConnectDomain=process.env.PROBE_WS_CONNECT_DOMAIN;
if(!wsGateway||!wsConnectDomain)throw Error('Explicit internal gateway and connect domain required');
const config=JSON.parse(fs.readFileSync(configPath));
const robot=config.robots.find(r=>r.id===robotId);
if(!robot||robot.enabled!==false||robot.purpose==='verification'||!user||!robot.allowUsers?.includes(user))throw Error('Robot must be paused and sender must already be allowed');
const identity=r=>createHash('sha256').update(JSON.stringify([r.id,r.appId,r.appKey,r.appSecret,r.agent,r.profile,r.workspaceId])).digest('hex');
const initialIdentity=identity(robot);
const quiet=Object.fromEntries(['info','debug','trace','warn','error'].map(k=>[k,()=>{}]));
const runId=randomUUID();
const marker='TMESH_DIRECT_'+runId.slice(0,8);
const report={runId,marker,robot:robot.name,startedAt:new Date().toISOString(),phase:'connecting',passed:false,
 connected:0,ping:0,pong:0,received:[],sent:0,unexpectedDisconnects:0,nonTestInputs:0,acknowledged:0};
let fatal=null,plannedClose=false,finished=false;
const safeError=e=>String(e?.code||e?.name||'TRANSPORT_ERROR').replace(/[^A-Za-z0-9_.-]/g,'').slice(0,80);
const save=()=>{fs.writeFileSync(output+'.next',JSON.stringify(report,null,2),{mode:0o600});fs.renameSync(output+'.next',output);};
const fail=reason=>{fatal??=reason;};
for(const signal of ['SIGTERM','SIGINT'])process.on(signal,()=>fail('probe_cancelled'));
const client=createWSClient({appKey:robot.appKey,appSecret:robot.appSecret,baseUrl:robot.baseUrl,
 wsGateway,wsConnectDomain,
 logger:quiet,autoRegister:true,autoAck:true,endpointTimeout:15000},'');
const api=new Client({appKey:robot.appKey,appSecret:robot.appSecret,agentId:robot.appId,baseUrl:robot.baseUrl,logger:quiet});
const seen=new Set(),allowedAck=new Set();
const originalAck=client.sendEventAck.bind(client);
client.sendEventAck=frame=>{if(allowedAck.has(String(frame.seqId))){originalAck(frame);report.acknowledged++;}};
const originalHandle=client.handleMessage.bind(client);
client.handleMessage=async bytes=>{
 try{
  const frame=client.frameCodec.decode(bytes);let raw=client.frameCodec.parsePayload(frame);
  if(typeof raw?.Encrypt==='string'){try{raw={...raw,...JSON.parse(raw.Encrypt)};}catch{}}
  if(frame.method===1){
   const sender=raw?.FromUserId??raw?.fromUserId??raw?.message?.header?.fromuserid;
   const content=raw?.Content??raw?.content;
   if(sender===user&&typeof content==='string'&&content.trim().startsWith(marker+' '))allowedAck.add(String(frame.seqId));
   else if(sender&&!raw?.groupid&&!raw?.groupId&&raw?.FromUserType!=='robot'){
    report.nonTestInputs++;fail('non_test_private_input_received_without_ack');
   }else if(raw?.eventtype==='MESSAGE_RECEIVE'&&raw?.groupid){
    report.nonTestInputs++;fail('non_test_group_input_received_without_ack');
   }
  }
 }catch{fail('frame_decode_failed');}
 return originalHandle(bytes);
};
const send=async text=>{await api.im.message.sendToUser(user,text,'md');report.sent++;save();};
for(const event of ['private.text','private.markdown'])client.on(event,async e=>{
 const raw=e?.data?.raw??{},text=String(raw.Content??raw.content??'').trim();
 if(raw.FromUserId!==user||!text.startsWith(marker+' '))return;
 const id=String(raw.MsgId??raw.msgId??'');if(!id||seen.has(id))return;seen.add(id);
 report.received.push({id,at:new Date().toISOString(),phase:report.phase});save();
 try{await send('【内网直连验收】已收到第 '+report.received.length+' 条测试消息，当前阶段：'+report.phase+'。这条消息未提交给智能体或已有任务。');}
 catch{fail('echo_send_failed');}
});
client.on('error',e=>{report.lastErrorCode=safeError(e?.data?.error??e?.error??e);save();});
client.on('heartbeat',e=>{const type=e?.data?.type;if(type==='ping'||type==='pong'){report[type]++;if(type==='pong')report.lastPongAt=new Date().toISOString();save();}});
client.on('disconnected',()=>{if(finished)return;if(!plannedClose)report.unexpectedDisconnects++;report.lastDisconnectedAt=new Date().toISOString();save();});
client.on('connected',()=>{
 report.connected++;report.lastConnectedAt=new Date().toISOString();
 if(client.ws){client.ws.once('close',(code,reason)=>{report.lastClose={code,reasonPresent:!!reason?.length};save();});}
 save();
});
const originalEndpoint=client.fetchWsEndpoint.bind(client);
client.fetchWsEndpoint=async(...args)=>{const endpoint=await originalEndpoint(...args);report.endpointProtocol=new URL(endpoint.url).protocol;report.endpointHost=new URL(endpoint.url).hostname;save();return endpoint;};
const wait=async(ms)=>{
 const end=Date.now()+ms;
 while(Date.now()<end){
  if(fatal)throw Error(fatal);
  const current=JSON.parse(fs.readFileSync(configPath)).robots.find(r=>r.id===robotId);
  if(!current||current.enabled!==false||identity(current)!==initialIdentity)throw Error('paused_robot_configuration_changed');
  save();await delay(Math.min(1000,end-Date.now()));
 }
};
let ticker;
try{
 save();await client.connect();
 if(client.getState()!=='connected')throw Error('connection_not_established');
 report.heartbeatMs=client.getHeartbeatInterval();report.serverConfig=client.serverConfig;
 const registerEnd=Date.now()+15000;while(!client.registered&&Date.now()<registerEnd)await wait(250);
 if(!client.registered)throw Error('websocket_callback_registration_failed');
 report.phase='before_disconnect';save();
 await send('【PDC-CPU 内网直连验收】测试连接已就绪。请分别发送：`'+marker+' 1` 和 `'+marker+' 2`。仅这些测试消息会被应答，不调用智能体。');
 ticker=setInterval(()=>console.log(JSON.stringify({phase:report.phase,elapsedSeconds:Math.round((Date.now()-Date.parse(report.startedAt))/1000),ping:report.ping,pong:report.pong,received:report.received.length,connected:report.connected})),30000);
 await wait(Math.max(600000,5*report.heartbeatMs));
 if(report.pong<4||report.received.filter(r=>r.phase==='before_disconnect').length<2||report.unexpectedDisconnects)throw Error('pre_disconnect_acceptance_failed');
 report.phase='reconnecting';report.forcedAt=new Date().toISOString();plannedClose=true;save();
 await send('【内网直连验收】即将仅断开测试连接，验证 SDK 按服务端参数自动重连。');
 const previous=report.connected;client.ws.terminate();
 const budget=Math.max(60000,(client.serverConfig?.reconnect_interval??120)*1000+(client.serverConfig?.reconnect_nonce??30)*1000+45000);
 const until=Date.now()+budget;while(report.connected===previous&&Date.now()<until)await wait(500);
 plannedClose=false;
 if(report.connected===previous||client.getState()!=='connected')throw Error('automatic_reconnect_failed');
 report.reconnectMs=Date.now()-Date.parse(report.forcedAt);report.phase='after_reconnect';save();
 const pongBefore=report.pong;
 await send('【内网直连验收】自动重连已恢复。请发送：`'+marker+' 3`，确认重连后仍能收发。');
 await wait(Math.max(240000,2*report.heartbeatMs));
 if(report.pong-pongBefore<2||!report.received.some(r=>r.phase==='after_reconnect')||report.unexpectedDisconnects)throw Error('post_reconnect_acceptance_failed');
 report.phase='passed';report.passed=true;
 await send('【内网直连验收】消息收发、持续心跳及强制断线恢复均已通过。测试连接现在退出，正式 Bridge 将恢复接管。');
}catch(e){report.phase='failed';report.failure=String(e.message).match(/^[a-z0-9_]+$/)?e.message:safeError(e);process.exitCode=1;}
finally{finished=true;if(ticker)clearInterval(ticker);client.disconnect();client.wsProxyAgent?.destroy();report.finishedAt=new Date().toISOString();save();console.log(JSON.stringify({phase:report.phase,passed:report.passed,failure:report.failure,ping:report.ping,pong:report.pong,received:report.received.length,reconnectMs:report.reconnectMs}));}
