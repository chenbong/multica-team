import {afterEach, describe, expect, it} from "vitest";
import {cleanup, render as rawRender, screen} from "@testing-library/react";
import {QueryClient,QueryClientProvider} from "@tanstack/react-query";
import type {ReactNode} from "react";
import userEvent from "@testing-library/user-event";
import {LiveDashboardPreview} from "./preview";
const render=(ui:ReactNode)=>rawRender(<QueryClientProvider client={new QueryClient()}>{ui}</QueryClientProvider>);

afterEach(cleanup);
describe("public live dashboard interaction preview",()=>{
  it("uses the supplied Team Mesh artwork while keeping one accessible brand link",()=>{
    const {container}=render(<LiveDashboardPreview/>);
    const brand=screen.getByRole("link",{name:"Team Mesh 平台实时看板"});
    expect(brand).toHaveAttribute("href","/live");
    expect(brand.querySelector('img[src="/team-mesh/teammesh-lockup.svg"]')).toHaveAttribute("alt","");
    expect(brand.querySelector('img[src="/team-mesh/teammesh-lockup-dark.svg"]')).toHaveAttribute("alt","");
    expect(container.querySelector('.tm-live-mascot[aria-hidden="true"] img')).toHaveAttribute("src","/team-mesh/teammesh-icon.svg");
  });
  it("can preview real change animations with explicitly labelled demo controls",async()=>{
    const user=userEvent.setup();const {container}=render(<LiveDashboardPreview/>);
    expect(container.querySelector(".tm-live-numberChanging")).toBeNull();
    await user.click(screen.getByRole("button",{name:"模拟数字刷新"}));
    const card=screen.getByText("正在工作的智能体").closest(".tm-live-card");
    expect(card?.querySelector(".tm-live-numberNew")).toHaveTextContent("18");
    expect(card?.querySelector(".tm-live-numberChanging")).toHaveAttribute("data-direction","up");
    await user.click(screen.getByRole("button",{name:"模拟数字刷新"}));
    expect(card?.querySelector(".tm-live-numberNew")).toHaveTextContent("16");
    expect(card?.querySelector(".tm-live-numberChanging")).toHaveAttribute("data-direction","down");
    await user.click(screen.getByRole("button",{name:"模拟更新延迟"}));
    expect(screen.getByRole("button",{name:"模拟数字刷新"})).toBeDisabled();
  });
  it("keeps the main dashboard compact and opens entry links in new tabs",()=>{
    render(<LiveDashboardPreview quickStartHref="https://docs.example.test/start"/>);
    for(const text of ["网页实际操作与如流有效消息，按账号去重","同一智能体同时处理多项工作只计一次","同一任务或聊天去重计算","同一接入身份跨工作区、运行时去重"]){
      expect(screen.queryByText(text)).not.toBeInTheDocument();
    }
    expect(screen.queryByText(/以上为 10.15—10.21 的演示序列/)).not.toBeInTheDocument();
    expect(screen.queryByRole("link",{name:"了解平台"})).not.toBeInTheDocument();
    const guide=screen.getByRole("link",{name:"快速上手"});
    expect(guide).toHaveAttribute("href","https://docs.example.test/start");
    for(const name of ["开始使用","开始使用 Team Mesh"]){
      const link=screen.getByRole("link",{name});
      expect(link).toHaveAttribute("target","_blank");
      expect(link).toHaveAttribute("rel","noopener noreferrer");
    }
  });
  it("shows only masked, unlinked example usernames on all four activity kinds",()=>{
    render(<LiveDashboardPreview/>);
    expect(screen.getByRole("heading",{name:"协作动态"})).toBeInTheDocument();
    expect(screen.queryByText("脱敏展示")).not.toBeInTheDocument();
    const items=screen.getAllByRole("listitem");
    expect(items).toHaveLength(4);
    for(const item of items){
      expect(item.textContent).toMatch(/：@\w{2}\*{3}\w/);
      expect(item.querySelector("a")).toBeNull();
    }
    expect(items[0]).toHaveTextContent("5 项任务已完成：@ch***g @li***e");
    expect(items[1]).toHaveTextContent("3 个智能体开始工作：@ch***g");
    expect(items[2]).toHaveTextContent("2 项新任务已创建：@li***e");
    expect(items[3]).toHaveTextContent("1 台机器接入平台：@ch***g");
  });
  it("marks every view as demo, exposes no identity details and links to onboarding",()=>{
    render(<LiveDashboardPreview quickStartHref="https://docs.example.test/start"/>);
    expect(screen.getByRole("region",{name:"平台实时看板"})).toHaveAttribute("tabindex","0");
    expect(screen.getByRole("note")).toHaveTextContent("不代表平台实际使用情况");
    expect(screen.getByRole("heading",{name:"今天，一起推进了这些事"})).toBeInTheDocument();
    expect(screen.getByText("页面浏览量",{selector:"span"})).toBeInTheDocument();
    expect(screen.getByRole("link",{name:"开始使用 Team Mesh"})).toHaveAttribute("href","/login");
    expect(screen.getByRole("link",{name:"查看快速上手"})).toHaveAttribute("href","https://docs.example.test/start");
  });
  it("switches complete-day trend metrics without navigation",async()=>{
    const user=userEvent.setup();render(<LiveDashboardPreview/>);
    await user.click(screen.getByRole("tab",{name:"页面浏览量"}));
    expect(screen.getByRole("tab",{name:"页面浏览量"})).toHaveAttribute("aria-selected","true");
    expect(screen.getByRole("table",{name:"页面浏览量，演示数据"})).toHaveTextContent("1,324");
    expect(screen.getByRole("rowheader",{name:"10.15（周四）"})).toBeInTheDocument();
    expect(screen.getByRole("rowheader",{name:"10.21（周三）"})).toBeInTheDocument();
    await user.click(screen.getByRole("tab",{name:"新建 / 完成任务"}));
    expect(screen.getByRole("columnheader",{name:"完成任务"})).toBeInTheDocument();
  });
  it("keeps demo data on delay and distinguishes missing values from zero",async()=>{
    const user=userEvent.setup();render(<LiveDashboardPreview/>);
    await user.click(screen.getByRole("button",{name:"模拟更新延迟"}));
    expect(screen.getByRole("status")).toHaveTextContent("不代表当前状态");
    expect(screen.getByText("1,268")).toBeInTheDocument();
    expect(screen.getByText("已暂停")).toBeInTheDocument();
    await user.click(screen.getByRole("button",{name:"模拟数据缺失"}));
    expect(screen.queryByText("1,268")).not.toBeInTheDocument();
    expect(screen.getAllByText("—").length).toBeGreaterThan(5);
  });
  it("removes the data-description module and never invents a guide destination",()=>{
    render(<LiveDashboardPreview/>);
    expect(screen.queryByRole("link",{name:"查看快速上手"})).not.toBeInTheDocument();
    expect(screen.queryByRole("button",{name:"数据说明"})).not.toBeInTheDocument();
    expect(screen.queryByRole("region",{name:"数据说明"})).not.toBeInTheDocument();
  });
  it("keeps the original heading, labels the rolling 24-hour scope, and annotates every data module",()=>{
    render(<LiveDashboardPreview/>);
    expect(screen.getByRole("heading",{name:"今天，一起推进了这些事"})).toBeInTheDocument();
    expect(screen.getByText("最近 24 小时 · 每 10 秒更新")).toBeInTheDocument();
    expect(screen.getByText("正在活跃的用户")).toBeInTheDocument();
    expect(screen.getByText("最近 1 小时 每 10 秒更新")).toBeInTheDocument();
    expect(screen.queryByText(/每 15 秒更新/)).not.toBeInTheDocument();
    for(const name of ["此刻的 Team Mesh 实时活动","今天，一起推进了这些事","近 7 天，协作的足迹","协作动态","不止能对话，更能一起做事","平台已有的规模"]){
      expect(screen.getByRole("region",{name})).toHaveTextContent(/每\s*(10 秒|5 分钟)更新/);
    }
    for(const name of ["此刻的 Team Mesh 实时活动","今天，一起推进了这些事","协作动态","平台已有的规模"])expect(screen.getByRole("region",{name})).toHaveTextContent("每 10 秒更新");
    for(const name of ["近 7 天，协作的足迹","不止能对话，更能一起做事"])expect(screen.getByRole("region",{name})).toHaveTextContent("每 5 分钟更新");
  });
  it("offers explicitly labelled new-event previews without changing real data",async()=>{
    const user=userEvent.setup();render(<LiveDashboardPreview/>);await user.click(screen.getByRole("button",{name:"模拟新增动态"}));
    expect(screen.getAllByRole("listitem")).toHaveLength(5);expect(screen.getByText("演示 · 刚刚")).toBeInTheDocument();
  });
});
