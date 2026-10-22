import {useState} from 'react';
import {createRoot} from 'react-dom/client';
import {QueryClient,QueryClientProvider} from '@tanstack/react-query';
import {RemoteWorkspace,RemoteWorkspaceChips,useRemoteWorkspace} from '../chat/components/remote-workspace';
import '../../../apps/web/app/globals.css';
function Composer(){const context=useRemoteWorkspace();return <div className="m-4 rounded-lg border p-3"><RemoteWorkspaceChips/><textarea aria-label="聊天输入" className="w-full p-2" placeholder="描述你的需求，或从文件树添加上下文…"/><button onClick={()=>{if(context?.draft){document.querySelector('#sent')!.textContent=JSON.stringify(context.draft);context.clearSent(context.draft)}}}>发送 / Send</button></div>}
function App(){const [session,setSession]=useState('one');return <QueryClientProvider client={new QueryClient()}><div className="flex h-screen flex-col bg-background text-foreground"><header className="flex items-center justify-between border-b px-5 py-3"><strong>Multica · Remote coding workspace</strong><button onClick={()=>setSession(session==='one'?'two':'one')}>切换聊天 / Switch chat</button></header><RemoteWorkspace sessionId={session}><div className="flex-1 overflow-auto p-6"><h2 className="mb-3 text-xl font-semibold">检查登录错误处理</h2><p className="text-muted-foreground">打开左侧文件，选中代码并添加到当前会话。</p><pre id="sent" className="mt-6 whitespace-pre-wrap text-xs"/></div><Composer/></RemoteWorkspace></div></QueryClientProvider>}
createRoot(document.getElementById('root')!).render(<App/>);
