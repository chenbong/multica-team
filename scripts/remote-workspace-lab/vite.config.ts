import {defineConfig,type Plugin} from 'vite';
import react from '@vitejs/plugin-react';
import {fileURLToPath} from 'node:url';
import {readFileSync} from 'node:fs';
const here=fileURLToPath(new URL('.',import.meta.url));
const tokenFile=process.env.TEAMMESH_LAB_TOKEN_FILE;
if(!tokenFile)throw new Error('Start with scripts/remote-workspace-lab.sh');
const token=readFileSync(tokenFile,'utf8').trim();
const relay:Plugin={name:'local-lab-relay',configureServer(server){server.middlewares.use('/lab-api',async(req,res)=>{
 const origin=req.headers.origin;const host=req.headers.host;
 if(host!=='127.0.0.1:4180' && host!=='localhost:4180'){res.statusCode=403;res.end('loopback host required');return}
 if(origin && origin!==`http://${host}`){res.statusCode=403;res.end('cross-origin request denied');return}
 res.setHeader('Content-Type','application/json');res.setHeader('Cache-Control','no-store');
 if(req.url==='/info' && req.method==='GET'){res.end(JSON.stringify({mode:'two-process live filesystem lab; no model or DB auth',server:'127.0.0.1:4181',root:process.env.TEAMMESH_LAB_ROOT,serverPid:process.env.TEAMMESH_LAB_SERVER_PID,daemonPid:process.env.TEAMMESH_LAB_DAEMON_PID},null,2));return}
 if(req.url!=='/workspace'||req.method!=='POST'){res.statusCode=404;res.end('{}');return}
 try{
 let body='';for await(const chunk of req){body+=chunk;if(Buffer.byteLength(body)>32768){res.statusCode=413;res.end('{}');return}}
 const upstream=await fetch('http://127.0.0.1:4181/lab/workspace',{method:'POST',headers:{Authorization:`Bearer ${token}`,'Content-Type':'application/json'},body,signal:AbortSignal.timeout(10000)});
 res.statusCode=upstream.status;res.end(await upstream.text());
 }catch{res.statusCode=503;res.end(JSON.stringify({error:'lab_server_unavailable'}))}
 });}};
export default defineConfig({root:here,plugins:[react(),relay],resolve:{alias:[
 {find:/^\.\/components\/use-chat-controller$/,replacement:here+'controller.tsx'},
]},css:{postcss:here},server:{host:'127.0.0.1',port:4180,strictPort:true,fs:{allow:[fileURLToPath(new URL('../../..',import.meta.url))]}}});
