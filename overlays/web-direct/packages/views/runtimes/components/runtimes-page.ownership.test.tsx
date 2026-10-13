import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { AgentRuntime, RuntimeProfile } from "@multica/core/types";
import en from "../../locales/en/runtimes.json";
import { RuntimesPage } from "./runtimes-page";

const state = vi.hoisted(() => ({
  ws:"ws-a", auth:{isLoading:false,user:{id:"alice"}},
  role: "owner" as "owner" | "admin" | "member" | null,
  runtimes:[] as AgentRuntime[], profiles:[] as RuntimeProfile[], invalidate:vi.fn(),
}));
vi.mock("@multica/core/auth",()=>({useAuthStore:Object.assign((selector:(s:typeof state.auth)=>unknown)=>selector(state.auth),{getState:()=>state.auth})}));
vi.mock("@multica/core/hooks",()=>({useWorkspaceId:()=>state.ws}));
vi.mock("@multica/core/permissions",()=>({useCurrentMember:()=>({role:state.role,isLoading:false})}));
vi.mock("@multica/core/paths",()=>({useRequiredWorkspaceSlug:()=>state.ws,useWorkspacePaths:()=>({runtimeDetail:(id:string)=>`/runtimes/${id}`,chatSession:()=>"/chat"})}));
vi.mock("@multica/core/realtime",()=>({useWSEvent:vi.fn()}));
vi.mock("@multica/core/onboarding",()=>({memberNeedsMikaSetup:()=>false,useBootstrapMika:()=>({})}));
vi.mock("@multica/core/agents",()=>({agentTaskSnapshotOptions:()=>({queryKey:["snapshot"]})}));
vi.mock("@multica/core/chat/queries",()=>({chatSessionsOptions:()=>({queryKey:["sessions"]})}));
vi.mock("@multica/core/workspace/queries",()=>({agentListOptions:()=>({queryKey:["agents"]})}));
vi.mock("@multica/core/runtimes",()=>({runtimeProfileListOptions:()=>({queryKey:["profiles"]}),deriveRuntimeHealth:()=>"online"}));
vi.mock("@multica/core/runtimes/queries",()=>({runtimeListOptions:()=>({queryKey:["runtimes"]}),runtimeKeys:{all:(id:string)=>[id]}}));
vi.mock("@tanstack/react-query",()=>({useQuery:({queryKey}:{queryKey:string[]})=>({data:queryKey[0]==="runtimes"?state.runtimes:queryKey[0]==="profiles"?state.profiles:[],isLoading:false}),useQueryClient:()=>({invalidateQueries:state.invalidate})}));
vi.mock("../../navigation",()=>({AppLink:({children,...props}:React.ComponentProps<"a">)=><a {...props}>{children}</a>,useNavigation:()=>({push:vi.fn()})}));
vi.mock("../../i18n",()=>({useT:()=>({t:(select:(value:typeof en)=>unknown)=>String(select(en)??""),i18n:{language:"en"}}),useTimeAgo:()=>()=>"now"}));
vi.mock("../../layout/collection-page",()=>({
  CollectionPageHeader:({title,count,actions}:{title:string;count:number;actions:React.ReactNode})=><header><h1>{title}</h1><output aria-label="Machine count">{count}</output>{actions}</header>,
  CollectionPageHeaderAction:({label,onClick}:{label:string;onClick:()=>void})=><button onClick={onClick}>{label}</button>,
  CollectionPageState:({title,description,actions}:{title:string;description:string;actions:React.ReactNode})=><section><h2>{title}</h2><p>{description}</p>{actions}</section>,
}));
vi.mock("./shared",()=>({HealthDot:()=>null,HealthIcon:()=>null,useHealthLabel:()=>()=>"online"}));
vi.mock("./provider-logo",()=>({ProviderLogo:()=>null}));
vi.mock("./connect-remote-dialog",()=>({ConnectRemoteDialog:()=>null}));
vi.mock("./cloud-runtime-dialog",()=>({CloudRuntimeDialog:()=>null}));
vi.mock("./delete-machine-button",()=>({DeleteMachineButton:()=>null}));
vi.mock("./mika-runtime-choice",()=>({MikaRuntimeChoice:()=>null}));
vi.mock("../../onboarding/components/mika-intro",()=>({MIKA_PLACEHOLDER_EMOJI:"test"}));
vi.mock("./runtime-list",()=>({buildWorkloadIndex:()=>new Map(),RuntimeList:({runtimes}:{runtimes:AgentRuntime[]})=><div>{runtimes.map(r=><span key={r.id}>{r.name}</span>)}</div>}));

function runtime(id:string,owner:string):AgentRuntime {
  return {id,workspace_id:state.ws,daemon_id:id,name:`Claude (${id})`,runtime_mode:"local",provider:"claude",status:"online",owner_id:owner,visibility:"private",device_info:"",launch_header:"",metadata:{},last_seen_at:new Date().toISOString(),created_at:"",updated_at:""};
}
beforeEach(()=>{state.ws="ws-a";state.role="owner";state.auth.user.id="alice";state.profiles=[];state.runtimes=[runtime("my-computer","alice"),runtime("other-computer","bob")];});

describe("runtime page only-mine filter",()=>{
  it.each(["owner", "admin"] as const)("defaults checked for %s, toggles authorized rows and updates the count",async(role)=>{
    state.role=role;
    render(<RuntimesPage/>);
    const checkbox=screen.getByRole("checkbox",{name:"Only mine"});
    expect(checkbox).toBeChecked();
    expect(screen.getByRole("link",{name:/my-computer/})).toBeInTheDocument();
    expect(screen.queryByRole("link",{name:/other-computer/})).not.toBeInTheDocument();
    expect(screen.getByLabelText("Machine count")).toHaveTextContent("1");
    await userEvent.click(checkbox);
    expect(checkbox).not.toBeChecked();
    expect(screen.getByRole("link",{name:/other-computer/})).toBeInTheDocument();
    expect(screen.getByLabelText("Machine count")).toHaveTextContent("2");
    await userEvent.click(checkbox);
    expect(screen.queryByRole("link",{name:/other-computer/})).not.toBeInTheDocument();
  });
  it("resets after workspace or account changes",async()=>{
    const {rerender}=render(<RuntimesPage/>);
    await userEvent.click(screen.getByRole("checkbox",{name:"Only mine"}));
    state.ws="ws-b";rerender(<RuntimesPage/>);
    expect(screen.getByRole("checkbox",{name:"Only mine"})).toBeChecked();
    await userEvent.click(screen.getByRole("checkbox",{name:"Only mine"}));
    state.auth.user.id="bob";rerender(<RuntimesPage/>);
    expect(screen.getByRole("checkbox",{name:"Only mine"})).toBeChecked();
    expect(screen.queryByRole("link",{name:/my-computer/})).not.toBeInTheDocument();
    expect(screen.getByRole("link",{name:/other-computer/})).toBeInTheDocument();
  });
  it("lets an owner with no own machines expand the empty filtered view",async()=>{
    state.runtimes=[runtime("other-computer","bob")];render(<RuntimesPage/>);
    expect(screen.getByText(en.page.no_matches.title)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("checkbox",{name:"Only mine"}));
    expect(screen.getByRole("link",{name:/other-computer/})).toBeInTheDocument();
  });
  it("does not offer an all-users switch when the API returns no other user's data",()=>{
    state.role="member";state.runtimes=[];render(<RuntimesPage/>);
    expect(screen.queryByRole("checkbox",{name:"Only mine"})).not.toBeInTheDocument();
    expect(screen.getByText(en.page.empty.title)).toBeInTheDocument();
  });
  it("keeps the member's original authorized view, including public shared runtimes",()=>{
    state.role="member";state.runtimes[1]!.visibility="public";render(<RuntimesPage/>);
    expect(screen.queryByRole("checkbox",{name:"Only mine"})).not.toBeInTheDocument();
    expect(screen.getByRole("link",{name:/my-computer/})).toBeInTheDocument();
    expect(screen.getByRole("link",{name:/other-computer/})).toBeInTheDocument();
  });
  it("never defaults to the expanded view before a role is known",()=>{
    state.role=null;render(<RuntimesPage/>);
    expect(screen.queryByRole("checkbox",{name:"Only mine"})).not.toBeInTheDocument();
    expect(screen.queryByRole("link",{name:/other-computer/})).not.toBeInTheDocument();
  });
});
