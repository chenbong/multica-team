"use client";

import { useState } from "react";
import Link from "next/link";
import Image from "next/image";
import { Activity, ArrowRight, Bot, CheckCheck, ChevronRight, Clock3, Layers3, MessageSquare, Monitor, MousePointer2, RefreshCw, Users, Zap } from "lucide-react";
import { TrendChart } from "./trend-chart";
import { usePublicLive } from "./public-data";
import { AnimatedNumber } from "./animated-number";
import { ActivityFeed } from "./activity-feed";
import "./preview.css";

const styleNames = ["page","demo","nav","logo","brandLight","brandDark","navBadge","guide","primary","secondary","main","hero","heroAside","mascot","heroAccent","eyebrow","greenDot","amberDot","status","sectionHead","schedule","liveGrid","card","cardTitle","value","splitValue","section","todayGrid","todayCard","caption","middleGrid","chartPanel","feedPanel","tabs","chartMeta","legend","chart","chartColumn","barTrack","bar","completedBar","axis","empty","feedBadge","feedIntro","eventIcon","green","blue","violet","amber","capabilityGrid","capability","capabilityIcon","scale","scaleNumbers","cta","footer","notes","previewControls","warning","srOnly"];
const styles: Record<string, string> = Object.fromEntries([...styleNames,"eventMembers"].map(name=>[name,`tm-live-${name}`]));

const demoSeries = {
  users: { title: "活跃用户", unit: "人", values: [32, 38, 26, 45, 52, 61, 68] },
  pv: { title: "页面浏览量", unit: "次", values: [526, 681, 432, 815, 962, 1186, 1324] },
  messages: { title: "人机消息", unit: "条", values: [682, 864, 573, 1235, 1462, 1854, 2186] },
  tasks: { title: "新建 / 完成任务", unit: "项", values: [32, 43, 27, 58, 66, 82, 91], completed: [18, 21, 16, 32, 39, 47, 53] },
};
type Metric = keyof typeof demoSeries;
const demoDays = ["2026-10-15", "2026-10-16", "2026-10-17", "2026-10-18", "2026-10-19", "2026-10-20", "2026-10-21"];
const demoFeed = [
  { icon: CheckCheck, text: "5 项任务已完成", members: ["@ch***g","@li***e"], time: "演示 · 2 分钟前", color: "green" },
  { icon: Bot, text: "3 个智能体开始工作", members: ["@ch***g"], time: "演示 · 4 分钟前", color: "blue" },
  { icon: Layers3, text: "2 项新任务已创建", members: ["@li***e"], time: "演示 · 6 分钟前", color: "violet" },
  { icon: Monitor, text: "1 台机器接入平台", members: ["@ch***g"], time: "演示 · 9 分钟前", color: "amber" },
];

export function LiveDashboardPreview({quickStartHref,mode="demo",readOnlyPreview=false}:{quickStartHref?:string;mode?:"demo"|"live";readOnlyPreview?:boolean}) {
  const [metric, setMetric] = useState<Metric>(mode==="live"?"messages":"users");
  const [simulatedDelayed, setDelayed] = useState(false);
  const [unavailable, setUnavailable] = useState(false);
  const [demoRefresh,setDemoRefresh]=useState(0);
  const [demoActivity,setDemoActivity]=useState(0);
  const demoValue=(base:number,change=1)=>base+(demoRefresh%2?change:0);
  const live=usePublicLive(mode==="live");
  const realtime=live.realtime.data,recent=live.recent.data,history=live.history.data;
  const delayed=mode==="demo"?simulatedDelayed:Object.values(live).some(section=>section.stale);
  const hasData=Object.values(live).some(section=>section.data!==null);
  const days=mode==="demo"?demoDays:history?.days.map(day=>day.date)??[];
  const series:Record<Metric,{title:string;unit:string;values:(number|null)[];completed?:(number|null)[]}>=mode==="demo"?demoSeries:{
    users:{title:"活跃用户",unit:"人",values:history?.days.map(day=>day.active_users)??[]},
    pv:{title:"页面浏览量",unit:"次",values:history?.days.map(day=>day.page_views)??[]},
    messages:{title:"人机消息",unit:"条",values:history?.days.map(day=>day.messages)??[]},
    tasks:{title:"新建 / 完成任务",unit:"项",values:history?.days.map(day=>day.created_issues)??[],completed:history?.days.map(day=>day.completed_issues)??[]},
  };
  const dateRange=days.length?`${days[0]!.slice(5).replace("-",".")}—${days.at(-1)!.slice(5).replace("-",".")}`:"等待统计";
  const feed=mode==="demo"?[...Array.from({length:Math.min(demoActivity,12)},(_,i)=>({key:`demo-event-${demoActivity-i}`,icon:Bot,text:"1 个智能体开始工作",members:["@ch***g"],time:"演示 · 刚刚",color:"blue"})),...demoFeed.map(item=>({...item,key:item.text}))]:(realtime?.events??[]).map(event=>({
    key:event.kind+event.at,
    icon:{completed:CheckCheck,agent_started:Bot,created:Layers3,machine_connected:Monitor}[event.kind],
    text:{completed:`${event.count} 项任务已完成`,agent_started:`${event.count} 个智能体开始工作`,created:`${event.count} 项新任务已创建`,machine_connected:`${event.count} 台机器接入平台`}[event.kind],
    color:{completed:"green",agent_started:"blue",created:"violet",machine_connected:"amber"}[event.kind],
    members:event.members,time:relativeTime(event.at),
  }));
  const scaleRows=[{label:"用户",value:mode==="demo"?demoValue(218):realtime?.scale.users??null,unit:"位"},{label:"任务",value:mode==="demo"?demoValue(684,3):realtime?.scale.issues??null,unit:"项"},{label:"智能体",value:mode==="demo"?demoValue(326,2):realtime?.scale.agents??null,unit:"个"},{label:"接入机器",value:mode==="demo"?demoValue(86):realtime?.scale.machines??null,unit:"台"}];
  const updatedAt=live.realtime.updated_at??live.recent.updated_at??live.history.updated_at;
  const data = series[metric];
  return <div className={styles.page} role="region" aria-label="平台实时看板" tabIndex={0}>
    {(mode==="demo"||readOnlyPreview)&&<aside className={styles.demo} role="note">{mode==="demo"?<><strong>本地交互预览 · 演示数据</strong><span>所有数字与动态均为示例，不代表平台实际使用情况，未接入线上统计。</span></>:<><strong>本地只读预览 · 真实聚合数据</strong><span>仅查询线上数据，不修改业务记录。新口径活跃人数和 PV 尚未采集，以“—”显示。</span></>}</aside>}
    <header className={styles.nav}>
      <Link className={styles.logo} href="/live" aria-label="Team Mesh 平台实时看板"><Image className={styles.brandLight} src="/team-mesh/teammesh-lockup.svg" alt="" width={580} height={132} unoptimized/><Image className={styles.brandDark} src="/team-mesh/teammesh-lockup-dark.svg" alt="" width={580} height={132} unoptimized/><span className={styles.navBadge} aria-hidden="true">平台实时看板</span></Link>
      <nav aria-label="看板导航">{quickStartHref && <a href={quickStartHref} className={styles.guide} target="_blank" rel="noopener noreferrer">快速上手</a>}<Link className={styles.primary} href="/login" target="_blank" rel="noopener noreferrer">开始使用 <ArrowRight size={15}/></Link></nav>
    </header>
    <main className={styles.main}>
      <section className={styles.hero}>
        <div><div className={styles.eyebrow}><span/> 团队与 AI 小队，一起把事情做成</div><h1>协作，<span className={styles.heroAccent}>正在发生。</span></h1><p>从一个想法到一项任务，让智能体、开发机器和团队对话连接起来。</p></div>
        <div className={styles.heroAside}><div className={styles.mascot} aria-hidden="true"><Image src="/team-mesh/teammesh-icon.svg" alt="" width={264} height={264} unoptimized/></div><div className={styles.status}><span className={delayed ? styles.amberDot : styles.greenDot}/>{mode==="demo"?(delayed ? "演示：数据更新延迟" : "演示状态 · 非实时数据"):(delayed?"数据更新延迟":hasData?"数据正常更新":"数据加载中")}{mode==="live"&&updatedAt&&<small>最近成功更新：{new Intl.DateTimeFormat("zh-CN",{timeZone:"Asia/Shanghai",hour:"2-digit",minute:"2-digit",second:"2-digit",hourCycle:"h23"}).format(new Date(updatedAt))}</small>}</div></div>
      </section>

      <section aria-labelledby="now-title">
        <div className={styles.sectionHead}><h2 id="now-title">此刻的 Team Mesh <span>实时活动</span></h2><span className={styles.schedule}>最近 1 小时 每 10 秒更新</span></div>
        <div className={styles.liveGrid}>
          <MetricCard icon={Users} label="正在活跃的用户" value={unavailable ? null : mode==="demo"?demoValue(28,2):realtime?.active_users??null} unit="人" partial={mode!=="demo"&&!!realtime?.partial_metrics.includes("active_users")}/>
          <MetricCard icon={Bot} label="正在工作的智能体" value={unavailable ? null : mode==="demo"?demoValue(16,2):realtime?.working_agents??null} unit="个"/>
          <div className={styles.card}><div className={styles.cardTitle}><span>正在处理</span><MessageSquare size={18}/></div><div className={styles.splitValue}><span><AnimatedNumber value={unavailable?null:mode==="demo"?demoValue(11):realtime?.working_issues??null}/><small>项任务</small></span><i/><span><AnimatedNumber value={unavailable?null:mode==="demo"?demoValue(5):realtime?.working_chats??null}/><small>个聊天</small></span></div></div>
          <MetricCard icon={Monitor} label="在线机器" value={unavailable ? null : mode==="demo"?demoValue(32):realtime?.online_machines??null} unit="台"/>
        </div>
        {unavailable && <p className={styles.warning} role="status">演示：尚未获取到数据的指标显示“—”，不使用 0 代替。</p>}
        {delayed && <p className={styles.warning} role="status">{mode==="demo"?"演示：最近一次成功更新于 10 月 22 日 14:20，当前展示保留数据，不代表当前状态。":"部分数据暂时无法更新，保留最后成功结果，不代表当前状态。"}</p>}
      </section>

      <section className={styles.section} aria-labelledby="today-title">
        <div className={styles.sectionHead}><h2 id="today-title">今天，一起推进了这些事</h2><span className={styles.schedule}>最近 24 小时 · 每 10 秒更新</span></div>
        <div className={styles.todayGrid}>
          {[
            { label: "活跃用户", value: mode==="demo"?demoValue(74,2):recent?.active_users??null, unit: "人", icon: Users },
            { label: "页面浏览量", value: mode==="demo"?demoValue(1268,37):recent?.page_views??null, unit: "次", icon: MousePointer2 },
            { label: "人机消息", value: mode==="demo"?demoValue(2376,56):recent?.messages??null, unit: "条", icon: MessageSquare },
            { label: "新建任务", value: mode==="demo"?demoValue(96,3):recent?.created_issues??null, unit: "项", icon: Layers3 },
            { label: "完成任务", value: mode==="demo"?demoValue(38):recent?.completed_issues??null, unit: "项", icon: CheckCheck },
          ].map(({label,value,unit,icon:Icon})=><div className={styles.todayCard} key={label}><Icon size={17}/><span>{label}{mode!=="demo"&&value!==null&&((label==="活跃用户"&&recent?.partial_metrics.includes("active_users"))||(label==="页面浏览量"&&recent?.partial_metrics.includes("page_views")))&&<CollectionBadge/>}</span><strong><AnimatedNumber value={unavailable?null:value}/><small>{unit}</small></strong></div>)}
        </div>
      </section>

      <div className={styles.middleGrid}>
        <section className={styles.chartPanel} aria-labelledby="trends-title">
          <div className={styles.sectionHead}><h2 id="trends-title">近 7 天，协作的足迹</h2><span className={styles.schedule}>{dateRange} · 每 5 分钟更新</span></div>
          <div className={styles.tabs} role="tablist" aria-label="趋势指标">{(Object.keys(series) as Metric[]).map(key=><button key={key} role="tab" id={`tab-${key}`} aria-selected={metric===key} aria-controls="live-trend-panel" onClick={()=>setMetric(key)}>{series[key].title}</button>)}</div>
          <div id="live-trend-panel" role="tabpanel" aria-labelledby={`tab-${metric}`}>
            <div className={styles.chartMeta}><span>{data.title}<small>单位：{data.unit}</small></span><span className={styles.legend}><i/>{metric === "tasks" ? "新建" : "每日记录"}{metric === "tasks" && <><b/>完成</>}</span></div>
            {unavailable ? <div className={styles.empty}>数据暂不可用</div> :data.values.some(value=>value!==null)?<TrendChart dates={days} values={data.values} completed={data.completed} title={data.title} unit={data.unit} demo={mode==="demo"} partial={mode!=="demo"&&(metric==="users"||metric==="pv")?history?.days.map(day=>day.partial):undefined}/>:<div className={styles.empty}>{history?"该指标尚未开始采集":delayed?"数据暂不可用":"统计加载中"}</div>}
          </div>
        </section>
        <section className={styles.feedPanel} aria-labelledby="feed-title"><div className={styles.sectionHead}><h2 id="feed-title">协作动态</h2><span className={styles.schedule}>{delayed && <span className={styles.feedBadge}>已暂停</span>} 每 10 秒更新</span></div><ActivityFeed items={feed}/>{mode==="live"&&feed.length===0&&<p className={styles.caption}>{realtime?"暂时没有新的动态":"动态加载中"}</p>}</section>
      </div>

      <section className={styles.section} id="capabilities" aria-labelledby="why-title"><div className={styles.sectionHead}><h2 id="why-title">不止能对话，更能一起做事</h2><span className={styles.schedule}>近 7 天 · 每 5 分钟更新</span></div><div className={styles.capabilityGrid}>
        <Capability icon={Layers3} title="多个智能体，共同推进任务" text="把目标拆成任务，让不同能力的智能体参与执行，过程和结果留在同一处。" number={mode==="demo"?demoValue(46,2):history?.capabilities.multi_agent_issues??null} unit="项任务有多个智能体参与"/>
        <Capability icon={MessageSquare} title="留在如流，也能随时派活" text="从群聊或私聊发起工作，接收带图片的回复，让讨论与任务保持连接。" number={mode==="demo"?demoValue(128,3):history?.capabilities.infoflow_inputs??null} unit="条如流指令被接受"/>
        <Capability icon={Zap} title="自动唤醒，让跟进持续发生" text="周期检查、生成日报或继续推进任务，不必每次都手动提醒。" number={mode==="demo"?demoValue(215):history?.capabilities.wakeup_triggers??null} unit="次自动唤醒触发"/>
      </div></section>

      <section className={styles.scale} aria-labelledby="scale-title"><div><span className={styles.eyebrow}>一起构建 AI 协作网络</span><h2 id="scale-title">平台已有的规模</h2><span className={styles.schedule}>每 10 秒更新</span></div><div className={styles.scaleNumbers}>{scaleRows.map(({label,value,unit})=><div key={label}><strong><AnimatedNumber value={unavailable?null:value}/><small>{unit}</small></strong><span>{label}</span></div>)}</div></section>

      <section className={styles.cta}><div><h2>下一项工作，让 AI小队和团队一起完成。</h2><p>接入开发机器，创建智能体，从一个真实任务开始。</p></div><Link className={styles.primary} href="/login" target="_blank" rel="noopener noreferrer">开始使用 Team Mesh <ArrowRight size={17}/></Link>{quickStartHref && <a className={styles.secondary} href={quickStartHref} target="_blank" rel="noopener noreferrer">查看快速上手 <ChevronRight size={15}/></a>}</section>
      <footer className={styles.footer}><span>Team Mesh · 组织 AI 协作平台</span></footer>
      {mode==="demo"&&<section className={styles.previewControls} aria-label="本地预览控制"><span><Activity size={15}/> 预览状态</span><button disabled={delayed||unavailable} onClick={()=>setDemoRefresh(v=>v+1)}><RefreshCw size={14}/>模拟数字刷新</button><button disabled={delayed||unavailable} onClick={()=>setDemoActivity(v=>v+1)}><Activity size={14}/>模拟新增动态</button><button aria-pressed={delayed} onClick={()=>setDelayed(v=>!v)}><Clock3 size={14}/>{delayed ? "恢复状态展示" : "模拟更新延迟"}</button><button aria-pressed={unavailable} onClick={()=>setUnavailable(v=>!v)}><RefreshCw size={14}/>{unavailable ? "恢复示例数值" : "模拟数据缺失"}</button></section>}
    </main>
  </div>;
}

function CollectionBadge(){return <small className="tm-live-collecting" title="统计窗口尚未覆盖完整，仅显示已采集时段的累计值">采集中</small>;}
function MetricCard({icon:Icon,label,value,unit,partial=false}:{icon:typeof Users;label:string;value:number|null;unit:string;partial?:boolean}) {
  return <div className={styles.card}><div className={styles.cardTitle}><span>{label}{partial&&value!==null&&<CollectionBadge/>}</span><Icon size={18}/></div><div className={styles.value}><AnimatedNumber value={value}/><small>{unit}</small></div></div>;
}
function Capability({icon:Icon,title,text,number,unit}:{icon:typeof Users;title:string;text:string;number:number|null;unit:string}) {
  return <article className={styles.capability}><span className={styles.capabilityIcon}><Icon size={21}/></span><h3>{title}</h3><p>{text}</p><div><strong><AnimatedNumber value={number}/></strong><span>{unit}</span></div></article>;
}

function relativeTime(at:string){const minutes=Math.max(0,Math.floor((Date.now()-Date.parse(at))/60000));return minutes<1?"刚刚":minutes<60?`${minutes} 分钟前`:`${Math.floor(minutes/60)} 小时前`;}
