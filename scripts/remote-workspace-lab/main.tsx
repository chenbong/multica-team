/// <reference types="vite/client" />

import {useState} from 'react';
import {createRoot} from 'react-dom/client';
import {QueryClient,QueryClientProvider} from '@tanstack/react-query';
import {createAuthStore,registerAuthStore} from '@multica/core/auth';
import {createChatStore,registerChatStore} from '@multica/core/chat';
import {setCurrentWorkspace} from '@multica/core/platform';
import {WorkspaceSlugProvider} from '@multica/core/paths';
import {I18nProvider} from '@multica/core/i18n/react';
import type {LocaleResources} from '@multica/core/i18n';
import type {User} from '@multica/core/types';
import {NavigationProvider} from '../navigation';
import {ChatPage} from '../chat/chat-page';
import {Toaster} from 'sonner';
import {api,workspace,agent} from './api';
import {setApiInstance} from '@multica/core/api';
import '../../../apps/web/app/globals.css';
import './lab.css';
setApiInstance(api);
const data=new Map<string,string>();const storage={getItem:(k:string)=>data.get(k)??null,setItem:(k:string,v:string)=>{data.set(k,v)},removeItem:(k:string)=>{data.delete(k)}};
const auth=createAuthStore({api,storage});auth.setState({user:{id:'lab-owner',name:'Local tester'} as User,status:'authenticated',isLoading:false});registerAuthStore(auth);
const chat=createChatStore({storage});registerChatStore(chat);chat.getState().setActiveSession('lab-chat-one');
setCurrentWorkspace('local-lab','lab-workspace');
const qc=new QueryClient({defaultOptions:{queries:{retry:false,staleTime:Infinity}}});
qc.setQueryData(['workspaces','list'],[workspace]);qc.setQueryData(['workspaces','lab-workspace','agents'],[agent]);
const modules=import.meta.glob('../locales/zh-Hans/*.json',{eager:true,import:'default'});
const resources={'zh-Hans':Object.fromEntries(Object.entries(modules).map(([path,value])=>[path.split('/').at(-1)!.replace('.json',''),value])) as LocaleResources};
function App(){
 const [url,setUrl]=useState('/local-lab/chat?session=lab-chat-one');const [collapsed,setCollapsed]=useState(false);
 const parsed=new URL(url,location.origin);
 return <I18nProvider locale="zh-Hans" resources={resources}><QueryClientProvider client={qc}><WorkspaceSlugProvider slug="local-lab"><NavigationProvider value={{pathname:parsed.pathname,searchParams:parsed.searchParams,hash:'',replace:setUrl,push:setUrl,back:()=>{},getShareableUrl:(p)=>location.origin+p}}>
 <div className="lab-app bg-background text-foreground"><header className="lab-banner"><strong>本地隔离验证</strong><span>真实 ChatPage · 独立服务端 + daemon · 临时文件目录 · 不调用模型</span><a href="/lab-api/info" target="_blank" rel="noreferrer">查看进程和测试目录</a></header>
 <div className="lab-body"><aside aria-label="模拟全局导航" className={`lab-navigation ${collapsed?'is-collapsed':''}`}>
 <button className="lab-brand" onClick={()=>setCollapsed(!collapsed)}>{collapsed?'☰':'Team Mesh · Local'}</button>
 {!collapsed&&<><p className="lab-muted">导航外壳为本地示意，右侧为实际聊天页</p>{['搜索…','新建任务','收件箱','我的任务','聊天','工作','任务','项目','自动化','AI 团队','智能体','小队','Skills','运行时'].map(label=><div key={label} className={`lab-nav-item ${label==='聊天'?'selected':''}`}>{label}</div>)}<div className="lab-nav-footer">设置 · 本地验证，不连接线上</div></>}
 </aside><main className="lab-main"><ChatPage/></main></div></div><Toaster richColors/>
 </NavigationProvider></WorkspaceSlugProvider></QueryClientProvider></I18nProvider>;
}
createRoot(document.getElementById('root')!).render(<App/>);
