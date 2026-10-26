import {afterEach,beforeEach,describe,it,expect,vi} from "vitest";
import {render,screen,waitFor,cleanup} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {QueryClient,QueryClientProvider} from "@tanstack/react-query";
import {PersonalSecretsSection} from "./personal-secrets-section";

const mocks=vi.hoisted(()=>({listPersonalSecrets:vi.fn(),savePersonalSecret:vi.fn(),readPersonalSecret:vi.fn(),deletePersonalSecret:vi.fn()}));
vi.mock("@multica/core/api",()=>({api:mocks}));
vi.mock("@multica/core/auth",()=>({useAuthStore:(select:(s:{user:{id:string}})=>unknown)=>select({user:{id:"owner"}})}));
vi.mock("../../i18n",()=>({useT:()=>({t:(select:(s:{platform_expansion:Record<string,string>})=>string)=>select({platform_expansion:{secrets:"Personal secrets",add:"Add",name:"Name",description:"Description",value:"Secret content",save:"Save",cancel:"Cancel",edit:"Edit",remove:"Delete",reveal:"Show current value",add_secret:"Add secret",edit_secret:"Edit secret",failed:"Failed",secret_warning:"Shared agents may use these secrets",delete_warning:"Delete?",empty:"No secrets",loading:"Loading"}})})}));
const item={id:"11111111-1111-4111-8111-111111111111",name:"SSH",description:"fixture",updated_at:"today"};
function mount(){const client=new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}});return render(<QueryClientProvider client={client}><PersonalSecretsSection/></QueryClientProvider>);}
describe("personal secret management",()=>{
 beforeEach(()=>{vi.clearAllMocks();mocks.listPersonalSecrets.mockResolvedValue({secrets:[item]});mocks.savePersonalSecret.mockResolvedValue(item);mocks.readPersonalSecret.mockResolvedValue({...item,value:"fixture-private-key"});});
 afterEach(cleanup);
 it("does not read a value until explicitly revealed, and clears it when closed",async()=>{
  const user=userEvent.setup();mount();
  await screen.findByText("SSH");expect(mocks.readPersonalSecret).not.toHaveBeenCalled();
  await user.click(screen.getByRole("button",{name:"Edit"}));
  expect(screen.getByLabelText("Secret content")).toHaveValue("");
  await user.click(screen.getByRole("button",{name:"Show current value"}));
  await screen.findByText("fixture-private-key");
  await user.click(screen.getByRole("button",{name:"Cancel"}));
  await waitFor(()=>expect(screen.queryByText("fixture-private-key")).not.toBeInTheDocument());
 });
 it("creates a named multiline secret and preserves input if saving fails",async()=>{
  mocks.savePersonalSecret.mockRejectedValueOnce(new Error("private implementation error"));
  const user=userEvent.setup();mount();await screen.findByText("SSH");
  await user.click(screen.getByRole("button",{name:"Add"}));
  await user.type(screen.getByLabelText("Name"),"ugate");
  await user.type(screen.getByLabelText("Secret content"),"fixture-value");
  await user.click(screen.getByRole("button",{name:"Save"}));
  await screen.findByRole("alert");expect(screen.getByLabelText("Name")).toHaveValue("ugate");
  expect(screen.queryByText("private implementation error")).not.toBeInTheDocument();
  await user.click(screen.getByRole("button",{name:"Save"}));
  await waitFor(()=>expect(mocks.savePersonalSecret).toHaveBeenLastCalledWith(null,{name:"ugate",description:"",value:"fixture-value"}));
 });
});
