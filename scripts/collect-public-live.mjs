import fs from 'node:fs/promises';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {spawn} from 'node:child_process';

const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'..');
export const intervals={realtime:10,recent:10,history:300};
const number=value=>{if(value===null)return null;if(!Number.isSafeInteger(value)||value<0)throw Error('Invalid aggregate count');return value;};
const timestamp=value=>{if(typeof value!=='string'||!Number.isFinite(Date.parse(value)))throw Error('Invalid aggregate timestamp');return value;};
function counts(raw,keys){return Object.fromEntries(keys.map(key=>[key,number(raw[key])]));}

// Pick only the public contract: raw fields cannot accidentally pass through.
export function validateSection(section,raw){
 const result={observed_at:timestamp(raw.observed_at),unavailable_metrics:[],partial_metrics:[]};
 if(section==='realtime'){
  Object.assign(result,counts(raw,['active_users','working_agents','working_issues','working_chats','online_machines']));
  result.window_start=timestamp(raw.window_start);
  result.scale=raw.scale?counts(raw.scale,['users','issues','agents','machines']):{users:null,issues:null,agents:null,machines:null};
  if(Math.abs(Date.parse(result.observed_at)-Date.parse(result.window_start)-3600000)>1)throw Error('Invalid rolling one-hour window');
  if(!Array.isArray(raw.events)||raw.events.length>20)throw Error('Invalid event feed');
  result.events=raw.events.map(event=>{
   if(!['completed','agent_started','created','machine_connected'].includes(event.kind)||!Array.isArray(event.members)||event.members.length>5)throw Error('Invalid public event');
   const members=event.members.map(member=>{if(typeof member!=='string'||!/^@[a-z0-9._-]{0,2}\*{3}[a-z0-9._-]?$/i.test(member))throw Error('Unredacted account rejected');return member;});
   return {kind:event.kind,at:timestamp(event.at),count:number(event.count),members};
  });
 }else if(section==='recent'){
  Object.assign(result,counts(raw,['active_users','page_views','messages','created_issues','completed_issues']));result.window_start=timestamp(raw.window_start);
  if(Math.abs((Date.parse(result.observed_at)-Date.parse(result.window_start))-86400000)>1)throw Error('Invalid rolling 24-hour window');
 }else if(section==='history'){
  if(!Array.isArray(raw.days)||raw.days.length!==7)throw Error('Invalid history range');
  result.days=raw.days.map(day=>{if(typeof day.date!=='string'||!/^\d{4}-\d{2}-\d{2}$/.test(day.date))throw Error('Invalid history date');return {date:day.date,partial:day.partial===true,...counts(day,['active_users','page_views','messages','created_issues','completed_issues'])};});
  for(let i=1;i<7;i++)if(Date.parse(result.days[i].date)-Date.parse(result.days[i-1].date)!==86400000)throw Error('Nonconsecutive history');
  const today=new Intl.DateTimeFormat('en-CA',{timeZone:'Asia/Shanghai',year:'numeric',month:'2-digit',day:'2-digit'}).format(new Date(result.observed_at));
  if(Date.parse(today)-Date.parse(result.days[6].date)!==86400000)throw Error('History must exclude today');
  result.scale=counts(raw.scale,['users','issues','agents','machines']);
  result.capabilities=counts(raw.capabilities,['multi_agent_issues','infoflow_inputs','wakeup_triggers']);
 }else throw Error('Unknown section');
 if(Array.isArray(raw.unavailable_metrics))result.unavailable_metrics=raw.unavailable_metrics.filter(x=>['active_users','page_views'].includes(x));
 if(Array.isArray(raw.partial_metrics))result.partial_metrics=raw.partial_metrics.filter(x=>['active_users','page_views'].includes(x));
 return result;
}

export async function collectSection(section,config){
 if(!Object.hasOwn(intervals,section))throw Error('Unknown section');
 if(!/^[a-zA-Z0-9._@:-]+$/.test(config.host)||!Number.isInteger(config.port)||config.port<1||config.port>65535||!/^\/[a-zA-Z0-9/._-]+$/.test(config.docker)||!/^[-a-zA-Z0-9_.]+$/.test(config.container)||!/^\w+$/.test(config.database))throw Error('Invalid read-only source configuration');
 const quote=value=>"'"+value.replaceAll("'","'\\''")+"'";
 const inner='exec psql -XqAt -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$1"';
 const command=`${config.docker} exec -i ${config.container} sh -ec ${quote(inner)} live-stats ${quote(config.database)}`;
 const sql=await fs.readFile(path.join(root,'stats/live',section+'.sql'),'utf8');
 if(!/^--[^\n]*\nBEGIN TRANSACTION.*READ ONLY;|^BEGIN TRANSACTION.*READ ONLY;/m.test(sql)||!/SET LOCAL statement_timeout/.test(sql))throw Error('Read-only transaction required');
 const output=await new Promise((resolve,reject)=>{
  const child=spawn('ssh',['-p',String(config.port),'-o','BatchMode=yes','-o','StrictHostKeyChecking=yes','-o','ConnectTimeout=5',config.host,command],{stdio:['pipe','pipe','pipe']});
  const chunks=[];let size=0,failed=false;
  const fail=()=>{failed=true;child.kill('SIGTERM');reject(Error('Read-only aggregate query failed'));};
  const timer=setTimeout(fail,15000);
  child.stdout.on('data',chunk=>{size+=chunk.length;if(size>256*1024)fail();else chunks.push(chunk);});
  // Do not forward remote stderr: even diagnostic messages stay private.
  child.stderr.on('data',()=>{});child.stdin.on('error',()=>{});child.on('error',fail);
  child.on('close',code=>{clearTimeout(timer);if(failed)return;if(code!==0)reject(Error('Read-only aggregate query failed'));else resolve(Buffer.concat(chunks).toString('utf8').trim());});
  child.stdin.end(sql);
 });
 return validateSection(section,JSON.parse(output));
}

if(process.argv[1]&&path.resolve(process.argv[1])===fileURLToPath(import.meta.url)){
 const [configFile,section,outputFile]=process.argv.slice(2);
 try{
  if(!configFile||!section||!outputFile)throw Error('Expected private config, section and private output path');
  const config=JSON.parse(await fs.readFile(configFile,'utf8')),result=await collectSection(section,config);
  await fs.writeFile(outputFile,JSON.stringify(result,null,2),{mode:0o600,flag:'wx'});
  console.log(JSON.stringify({section,observedAt:result.observed_at,readOnly:true,aggregateOnly:true}));
 }catch(error){console.error(error.message);process.exitCode=1;}
}
