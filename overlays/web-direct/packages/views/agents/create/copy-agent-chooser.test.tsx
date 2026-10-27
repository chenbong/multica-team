import {afterEach,describe,it,expect,vi} from "vitest";
import {render,screen,cleanup,waitFor} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {QueryClient,QueryClientProvider} from "@tanstack/react-query";
import {CopyAgentChooser} from "./copy-agent-chooser";
const state=vi.hoisted(()=>({push:vi.fn(),listCopySources:vi.fn().mockResolvedValue([{workspaceName:"Other workspace",agent:{id:"source",workspace_id:"other",name:"Source Agent"}}])}));
vi.mock("@multica/core/api",()=>({api:state}));
vi.mock("@multica/core/auth",()=>({useAuthStore:(select:(s:{user:{id:string}})=>unknown)=>select({user:{id:"owner"}})}));
vi.mock("../../navigation",()=>({useNavigation:()=>({push:state.push})}));
vi.mock("../../i18n",()=>({useT:()=>({t:(select:(s:{platform_expansion:Record<string,string>,creation_studio:Record<string,string>})=>string)=>select({creation_studio:{continue:"Continue"},platform_expansion:{copy_description:"Copy configuration",copy:"Copy existing",copy_hint:"Choose a source",search:"Search",loading:"Loading",failed:"Failed"}})})}));
describe("copy source chooser",()=>{
 afterEach(cleanup);
 it("loads cross-workspace sources only on demand and navigates with the source workspace",async()=>{
  const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
  render(<QueryClientProvider client={client}><CopyAgentChooser blankHref="/target/agents/new/manual"/></QueryClientProvider>);
  expect(state.listCopySources).not.toHaveBeenCalled();
  const user=userEvent.setup();await user.click(screen.getByRole("button",{name:/Copy existing/}));
  await user.click(await screen.findByText("Source Agent"));
  expect(screen.getByText("Other workspace")).toBeInTheDocument();
  await waitFor(()=>expect(state.push).toHaveBeenCalledWith("/target/agents/new/manual?duplicate=source&duplicate_ws=other"));
 });
});
