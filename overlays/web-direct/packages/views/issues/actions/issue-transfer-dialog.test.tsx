import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { Issue } from "@multica/core/types";
import en from "../../locales/en/issues.json";
import { IssueTransferDialog } from "./issue-transfer-dialog";

const state=vi.hoisted(()=>({
  plan:{affected_issues:1,issue_ids:["issue"],include_subissues:false,requires_cleanup:true,status_mappings:[{source:"cancelled",name:"Cancelled",target:"cancelled"}],warnings:[],issue_id:"issue",source_workspace_id:"source",target_workspace_id:"target",source_identifier:"OLD-1",target_name:"Target",target_slug:"target",target_prefix:"NEW",target_status:"cancelled",statuses:[{key:"cancelled",name:"Cancelled"}],revision:1,comments:52,runs:28,messages:1651,unassign:false,retired_subscriptions:0,blockers:[] as string[],fingerprint:"a".repeat(64)},
  pending:false,fetching:false,mutate:vi.fn(),refetch:vi.fn(),push:vi.fn(),close:vi.fn(),
}));
vi.mock("@multica/core/issues/transfer",()=>({useIssueTransferPreview:(_s:string,_i:string,target:string)=>({data:target?state.plan:undefined,isFetching:state.fetching,isError:false,refetch:state.refetch}),useTransferIssue:()=>({mutateAsync:state.mutate,isPending:state.pending})}));
vi.mock("@tanstack/react-query",()=>({useQuery:()=>({data:[{id:"source",name:"Source"},{id:"target",name:"Target"}],isLoading:false,isError:false})}));
vi.mock("@multica/core/workspace/queries",()=>({workspaceListOptions:()=>({})}));
vi.mock("../../navigation",()=>({useNavigation:()=>({push:state.push})}));
vi.mock("../../i18n",()=>({useT:()=>({t:(selector:(value:typeof en)=>string)=>selector(en)})}));
const issue={id:"issue",workspace_id:"source",identifier:"OLD-1",title:"PiCoKV fixture"} as Issue;
beforeEach(()=>{vi.clearAllMocks();state.plan={...state.plan,unassign:false,blockers:[],fingerprint:"a".repeat(64)};state.pending=false;state.fetching=false;state.mutate.mockResolvedValue({...state.plan,identifier:"NEW-3"});});
async function consent(){await userEvent.click(screen.getByRole("checkbox",{name:en.transfer_expanded.confirm_cleanup}));}
async function open(){render(<IssueTransferDialog issue={issue} onClose={state.close}/>);await userEvent.selectOptions(screen.getByLabelText("Destination workspace"),"target");}
describe("issue transfer confirmation",()=>{
  it("requires a target and then navigates only after a successful transfer",async()=>{
    render(<IssueTransferDialog issue={issue} onClose={state.close}/>);
    expect(screen.getByRole("button",{name:"Confirm move"})).toBeDisabled();
    expect(screen.queryByRole("option",{name:"Source"})).not.toBeInTheDocument();
    await userEvent.selectOptions(screen.getByLabelText("Destination workspace"),"target");
    await consent();
    await userEvent.click(screen.getByRole("button",{name:"Confirm move"}));
    await waitFor(()=>expect(state.push).toHaveBeenCalledWith("/target/issues/NEW-3"));
    expect(state.mutate).toHaveBeenCalledWith({target_workspace_id:"target",include_subissues:false,status_mappings:{cancelled:"cancelled"},fingerprint:"a".repeat(64),confirm_unassign:false,confirm_cleanup:true});
  });
  it.each(["status_mapping_required","automation_or_sources","future_guard"])("blocks unresolved validation: %s",async(blocker)=>{
    state.plan.blockers=[blocker];await open();expect(screen.getByRole("button",{name:"Confirm move"})).toBeDisabled();expect(screen.getByRole("alert")).toBeInTheDocument();expect(state.mutate).not.toHaveBeenCalled();
  });
  it("requires explicit assignee removal consent",async()=>{
    state.plan.unassign=true;await open();expect(screen.getByRole("button",{name:"Confirm move"})).toBeDisabled();
    await consent();expect(screen.getByRole("button",{name:"Confirm move"})).toBeEnabled();
    await userEvent.click(screen.getByRole("button",{name:"Refresh preview"}));expect(screen.getByRole("button",{name:"Confirm move"})).toBeDisabled();
  });
  it("keeps the dialog open on errors and asks for a fresh preview",async()=>{
    state.mutate.mockRejectedValue(new Error("Preview changed"));await open();await consent();await userEvent.click(screen.getByRole("button",{name:"Confirm move"}));
    await waitFor(()=>expect(screen.getByRole("alert")).toHaveTextContent("Preview changed"));expect(state.close).not.toHaveBeenCalled();expect(state.push).not.toHaveBeenCalled();expect(state.refetch).toHaveBeenCalled();
  });
  it("disables submission while refreshing or submitting",async()=>{
    state.fetching=true;await open();expect(screen.getByRole("button",{name:"Confirm move"})).toBeDisabled();
  });
});
