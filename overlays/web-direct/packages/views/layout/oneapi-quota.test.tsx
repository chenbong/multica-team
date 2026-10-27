import { beforeEach, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import { OneAPIQuota } from "./oneapi-quota";
import en from "../locales/en/layout.json";
type Quota={status:string;values:{unlimited:boolean;used:number|null|undefined;total:number|null|undefined;remaining:number|null|undefined}|undefined;updatedAt:string|undefined};

const state=vi.hoisted(()=>({data:undefined as Quota|undefined,isError:false,isPending:false}));
vi.mock("@multica/core/auth",()=>({useAuthStore:(selector:(s:{user:{id:string}})=>unknown)=>selector({user:{id:"current-user"}})}));
vi.mock("@multica/core/runtimes",()=>({useOneAPIQuota:()=>state}));
function show(){return render(<I18nProvider locale="en" resources={{en:{layout:en}}}><OneAPIQuota href="https://quota.example.test/mine"/></I18nProvider>);}
beforeEach(()=>{state.data=undefined;state.isError=false;state.isPending=false;});
it("renders used/total, the used fraction, and remaining quota",()=>{
 state.data={status:"ready",values:{unlimited:false,used:3800,total:9000,remaining:5200},updatedAt:"2026-10-27T00:00:00Z"};show();
 expect(screen.getByRole("progressbar")).toHaveAttribute("aria-valuenow",String(3800/9000*100));
 expect(screen.getByRole("link")).toHaveTextContent("3,800 / 9,000");
 expect(screen.getByRole("link").getAttribute("title")).toContain("5,200");
});
it("keeps the configured account link when credentials are not linked",()=>{
 state.data={status:"not_linked",values:undefined,updatedAt:undefined};show();expect(screen.getByText("Not linked")).toBeInTheDocument();
 expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();expect(screen.getByRole("link")).toHaveAttribute("href","https://quota.example.test/mine");
 expect(screen.getByRole("link")).toHaveAttribute("target","_blank");
 expect(screen.getByRole("link")).toHaveAttribute("rel","noopener noreferrer");
});
it("keeps last successful values visibly stale",()=>{
 state.data={status:"stale",values:{unlimited:false,used:2,total:10,remaining:8},updatedAt:"2026-10-27T00:00:00Z"};show();expect(screen.getByRole("link")).toHaveTextContent("2 / 10");expect(screen.getByText("Not updated")).toBeInTheDocument();
});
it("does not invent a zero balance on failure or a finite total for unlimited quota",()=>{
 state.data={status:"unavailable",values:undefined,updatedAt:undefined};const view=show();expect(screen.getByText("Unavailable")).toBeInTheDocument();expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();view.unmount();
 state.data={status:"ready",values:{unlimited:true,used:null,total:null,remaining:null},updatedAt:undefined};show();expect(screen.getByText("Unlimited")).toBeInTheDocument();expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
});
