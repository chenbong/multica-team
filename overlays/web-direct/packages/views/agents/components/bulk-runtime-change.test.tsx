import {afterEach,beforeEach,describe,it,expect,vi} from "vitest";
import {render,screen,waitFor,cleanup} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {QueryClient,QueryClientProvider} from "@tanstack/react-query";
import type {AgentListRow} from "./agents-page";
import {BulkRuntimeChange} from "./bulk-runtime-change";
const state=vi.hoisted(()=>({getAgent:vi.fn(),updateAgent:vi.fn(),done:vi.fn()}));
vi.mock("@multica/core/api",()=>({api:state}));
vi.mock("@multica/core/hooks",()=>({useWorkspaceId:()=>"ws"}));
vi.mock("@multica/core/runtimes",()=>({runtimeListOptions:()=>({queryKey:["runtimes-test"],queryFn:async()=>[{id:"old",provider:"claude",status:"online"},{id:"new",provider:"codex",status:"online"}]}),runtimeModelsOptions:(id:string|null)=>({queryKey:["models-test",id],enabled:!!id,queryFn:async()=>({supported:true,models:[{id:"target",label:"Target",default:true}]})})}));
vi.mock("./inspector/runtime-picker",()=>({RuntimePicker:({onChange}:{onChange:(id:string)=>void})=><button onClick={()=>onChange("new")}>Target runtime</button>}));
vi.mock("../../i18n",()=>({useT:()=>({t:(select:(s:{platform_expansion:Record<string,string>})=>string)=>select({platform_expansion:{runtime:"Change runtime",preview:"Preview",confirm:"Confirm",cancel:"Cancel",close:"Close",skipped:"Skipped",success:"Completed",failed:"Failed",changed:"Preview again",defaults:"Default",runtime_hint:"Future runs only"}})})}));
const agent={id:"a",name:"Agent A",runtime_id:"old",model:"source",thinking_level:"high",service_tier:"",updated_at:"one"};
const rows=[{agent,canManage:true},{agent:{...agent,id:"b",name:"Agent B"},canManage:false}] as unknown as AgentListRow[];
function mount(){const client=new QueryClient({defaultOptions:{queries:{retry:false}}});render(<QueryClientProvider client={client}><BulkRuntimeChange rows={rows} members={[]} currentUserId="owner" onDone={state.done}/></QueryClientProvider>);}
describe("bulk runtime confirmation",()=>{
 beforeEach(()=>{vi.clearAllMocks();state.getAgent.mockResolvedValue(agent);state.updateAgent.mockResolvedValue(agent);});
 afterEach(cleanup);
 it("previews changes, skips unauthorized selections, and applies only on confirmation",async()=>{
  const user=userEvent.setup();mount();
  await user.click(screen.getByRole("button",{name:"Change runtime"}));await user.click(screen.getByRole("button",{name:"Target runtime"}));
  await waitFor(()=>expect(screen.getByRole("button",{name:"Preview"})).toBeEnabled());
  await user.click(screen.getByRole("button",{name:"Preview"}));
  await screen.findByRole("button",{name:"Confirm"});expect(state.updateAgent).not.toHaveBeenCalled();
  await screen.findByText("Skipped");await user.click(screen.getByRole("button",{name:"Confirm"}));
  await waitFor(()=>expect(state.updateAgent).toHaveBeenCalledTimes(1));
  expect(state.updateAgent).toHaveBeenCalledWith("a",{runtime_id:"new",model:"target",thinking_level:"",service_tier:""});
  await user.click(await screen.findByText("Close",{selector:"button"}));expect(state.done).toHaveBeenCalledOnce();
 });
 it("does not overwrite an agent changed after preview",async()=>{
  const user=userEvent.setup();mount();await user.click(screen.getByRole("button",{name:"Change runtime"}));await user.click(screen.getByRole("button",{name:"Target runtime"}));
  await waitFor(()=>expect(screen.getByRole("button",{name:"Preview"})).toBeEnabled());await user.click(screen.getByRole("button",{name:"Preview"}));await screen.findByRole("button",{name:"Confirm"});
  state.getAgent.mockResolvedValue({...agent,model:"concurrent-change"});await user.click(screen.getByRole("button",{name:"Confirm"}));
  await screen.findByText(/Agent A: Preview again/);expect(state.updateAgent).not.toHaveBeenCalled();
 });
});
