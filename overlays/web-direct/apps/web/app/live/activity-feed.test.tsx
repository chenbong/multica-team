import {afterEach,beforeEach,expect,it,vi} from "vitest";
import {act,cleanup,fireEvent,render,screen} from "@testing-library/react";
import {Bot} from "lucide-react";
import {ActivityFeed,type ActivityFeedItem} from "./activity-feed";

const original=Object.getOwnPropertyDescriptor(HTMLElement.prototype,"animate");
const animate=vi.fn();const cancel=vi.fn();
const item=(key:string,text=key):ActivityFeedItem=>({key,text,icon:Bot,members:["@ex***e"],time:"刚刚",color:"blue"});
beforeEach(()=>{
 animate.mockReset();cancel.mockReset();animate.mockImplementation(()=>({cancel,finished:new Promise<Animation>(()=>{})}));
 Object.defineProperty(HTMLElement.prototype,"animate",{configurable:true,value:animate});
 vi.stubGlobal("matchMedia",vi.fn(()=>({matches:false,addEventListener:vi.fn(),removeEventListener:vi.fn()})));
 vi.spyOn(HTMLElement.prototype,"getBoundingClientRect").mockImplementation(function(this:HTMLElement){
  const index=this.tagName==="LI"?Array.from(this.parentElement!.children).indexOf(this):0;
  const top=this.tagName==="LI"?index*50-this.parentElement!.scrollTop:0;
  return {top,bottom:top+(this.tagName==="LI"?50:100),left:0,right:300,width:300,height:50,x:0,y:top,toJSON:()=>({})};
 });
});
afterEach(()=>{cleanup();vi.restoreAllMocks();vi.unstubAllGlobals();if(original)Object.defineProperty(HTMLElement.prototype,"animate",original);else Reflect.deleteProperty(HTMLElement.prototype,"animate");});
it("does not animate initial or initially loaded history",()=>{
 const {rerender}=render(<ActivityFeed items={[]}/>);rerender(<ActivityFeed items={[item("a"),item("b")]}/>);
 expect(animate).not.toHaveBeenCalled();expect(screen.getAllByRole("listitem")).toHaveLength(2);
});
it("slides in new rows and smoothly repositions existing rows",()=>{
 const {rerender}=render(<ActivityFeed items={[item("a"),item("b")]}/>);rerender(<ActivityFeed items={[item("c"),item("a"),item("b")]}/>);
 expect(animate).toHaveBeenCalledTimes(3);
 expect(animate.mock.calls.some(([frames])=>frames[0].opacity===0)).toBe(true);
 expect(animate.mock.calls.some(([frames])=>frames[0].transform==="translate(0px, -50px)")).toBe(true);
});
it("does not replay unchanged rows or relative-time updates",()=>{
 const {rerender}=render(<ActivityFeed items={[item("a")]}/>);rerender(<ActivityFeed items={[{...item("a"),time:"1 分钟前"}]}/>);
 expect(animate).not.toHaveBeenCalled();expect(screen.getByText("1 分钟前")).toBeInTheDocument();
});
it("highlights a changed minute bucket without inventing a new event",()=>{
 const {rerender}=render(<ActivityFeed items={[item("a","1 个智能体开始工作")]}/>);rerender(<ActivityFeed items={[item("a","2 个智能体开始工作")]}/>);
 expect(animate).toHaveBeenCalledTimes(1);expect(animate.mock.calls[0]![0][0].backgroundColor).toBeDefined();expect(screen.getAllByRole("listitem")).toHaveLength(1);
});
it("keeps a manually scrolled reader on the same row when new data arrives",()=>{
 const {rerender}=render(<ActivityFeed items={[item("a"),item("b"),item("c")]}/>);
 const list=screen.getByRole("list",{name:"最近协作动态"});list.scrollTop=55;fireEvent.scroll(list);
 rerender(<ActivityFeed items={[item("new"),item("a"),item("b"),item("c")]}/>);expect(list.scrollTop).toBe(105);expect(animate).not.toHaveBeenCalled();
});
it("respects reduced motion and cancels animations when unmounted",()=>{
 vi.stubGlobal("matchMedia",vi.fn(()=>({matches:true,addEventListener:vi.fn(),removeEventListener:vi.fn()})));
 const {rerender}=render(<ActivityFeed items={[item("a")]}/>);rerender(<ActivityFeed items={[item("b"),item("a")]}/>);expect(animate).not.toHaveBeenCalled();
 cleanup();vi.stubGlobal("matchMedia",vi.fn(()=>({matches:false,addEventListener:vi.fn(),removeEventListener:vi.fn()})));
 const fresh=render(<ActivityFeed items={[item("a")]}/>);fresh.rerender(<ActivityFeed items={[item("b"),item("a")]}/>);fresh.unmount();expect(cancel).toHaveBeenCalled();
});
it("stops in-flight movement when the system enables reduced motion",()=>{
 let changed=()=>{};const media={matches:false,addEventListener:(_name:string,fn:()=>void)=>{changed=fn},removeEventListener:vi.fn()};vi.stubGlobal("matchMedia",()=>media);
 const {rerender}=render(<ActivityFeed items={[item("a")]}/>);rerender(<ActivityFeed items={[item("b"),item("a")]}/>);
 act(()=>{media.matches=true;changed();});expect(cancel).toHaveBeenCalled();
});
