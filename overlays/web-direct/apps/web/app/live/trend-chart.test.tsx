import {afterEach,describe,expect,it} from "vitest";
import {cleanup,render,screen} from "@testing-library/react";
import {chartScale,TrendChart} from "./trend-chart";

afterEach(cleanup);
describe("trend chart readout",()=>{
  it("marks only observed partial dates and keeps unknown dates distinct from real zero",()=>{
    render(<TrendChart dates={["2026-10-21","2026-10-22","2026-10-23"]} values={[null,23,0]} partial={[false,true,false]} title="活跃用户" unit="人" demo={false}/>);
    expect(screen.getByRole("img",{name:/10.21.*暂无数据/})).toBeInTheDocument();
    expect(screen.getByRole("img",{name:/10.22.*23 人，采集中/})).toBeInTheDocument();
    expect(screen.getByRole("img",{name:"10.23（周五）活跃用户 0 人"})).toBeInTheDocument();
    expect(screen.getAllByText("采集中")).toHaveLength(1);
  });
  it("uses finite integer tick scales including all real values",()=>{
    for(const values of [[0,0],[1],[32,68],[526,1324],[682,2186]]){
      const scale=chartScale(values);
      expect(scale.max).toBeGreaterThanOrEqual(Math.max(...values));
      expect(scale.ticks[0]).toBe(0);
      expect(scale.ticks.every(Number.isInteger)).toBe(true);
      expect(scale.ticks.at(-1)).toBe(scale.max);
    }
  });
  it("shows exact values, Beijing weekdays and linked focus/hover tooltip content",()=>{
    render(<TrendChart dates={["2026-10-15","2026-10-16"]} values={[32,43]} completed={[18,21]} title="新建 / 完成任务" unit="项"/>);
    const column=screen.getByRole("img",{name:"10.15（周四）新建任务 32 项，完成任务 18 项"});
    expect(column).toHaveAttribute("tabindex","0");
    const tooltip=document.getElementById(column.getAttribute("aria-describedby")!);
    expect(tooltip).toHaveAttribute("role","tooltip");
    expect(tooltip).toHaveTextContent("10.15 · 周四");
    expect(tooltip).toHaveTextContent("新建任务：32 项");
    expect(tooltip).toHaveTextContent("完成任务：18 项");
    expect(column.querySelectorAll(".tm-live-barValue")).toHaveLength(2);
    expect(screen.getByRole("rowheader",{name:"10.16（周五）"})).toBeInTheDocument();
  });
  it("does not convert missing observations into a zero-height counted value",()=>{
    render(<TrendChart dates={["2026-10-15"]} values={[null]} title="活跃用户" unit="人"/>);
    expect(screen.getByRole("img",{name:/暂无数据/})).toBeInTheDocument();
    expect(document.querySelector(".tm-live-barValue")).toBeNull();
    expect(screen.getByRole("cell",{name:"暂无数据"})).toBeInTheDocument();
  });
});
