// @vitest-environment jsdom
import {beforeEach,describe,expect,it,vi} from "vitest";
import {fireEvent,render,screen,waitFor} from "@testing-library/react";
import {QueryClient,QueryClientProvider} from "@tanstack/react-query";
import {I18nProvider} from "@multica/core/i18n/react";
import type {Skill,SkillSummary} from "@multica/core/types";
import enSkills from "../../locales/en/skills.json";
vi.mock("@multica/core/api",()=>({api:{skillTransferRecipients:vi.fn(),transferSkill:vi.fn()}}));
vi.mock("sonner",()=>({toast:{success:vi.fn(),error:vi.fn()}}));
import {api} from "@multica/core/api";
import {SkillTransferDialog} from "./skill-transfer-dialog";
const skill={id:"skill",workspace_id:"ws",scope:"team",name:"my-skill",description:"",config:{},created_by:"old",created_at:"",updated_at:""} as SkillSummary;
const user={user_id:"new",name:"Same name",email:"new@example.test"};const close=vi.fn();
function show(){const qc=new QueryClient({defaultOptions:{queries:{retry:false}}});qc.setQueryData(["workspaces","ws","skills","skill"],skill);render(<I18nProvider locale="en" resources={{en:{skills:enSkills}}}><QueryClientProvider client={qc}><SkillTransferDialog skill={skill} open onOpenChange={close}/></QueryClientProvider></I18nProvider>);return qc}
beforeEach(()=>{vi.clearAllMocks();vi.mocked(api.skillTransferRecipients).mockResolvedValue([user])});
async function select(){fireEvent.change(screen.getByLabelText("Recipient",{selector:"input"}),{target:{value:"new@example.test"}});fireEvent.click(await screen.findByRole("radio"));fireEvent.click(screen.getByRole("button",{name:"Continue"}));}
describe("skill transfer",()=>{
 it("requires explicit recipient selection and second confirmation, replaces cached creator",async()=>{
  vi.mocked(api.transferSkill).mockResolvedValue({...skill,content:"kept",created_by:"new"} as Skill);const qc=show();
  expect(screen.getByRole("button",{name:"Continue"})).toBeDisabled();await select();expect(api.transferSkill).not.toHaveBeenCalled();expect(screen.getByText(/new@example.test.*Its name/)).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button",{name:"Transfer now"}));await waitFor(()=>expect(close).toHaveBeenCalledWith(false));expect(api.transferSkill).toHaveBeenCalledWith("skill","new");expect((qc.getQueryData(["workspaces","ws","skills","skill"]) as Skill).created_by).toBe("new");
 });
 it("keeps the dialog and old creator when transfer is denied",async()=>{
  vi.mocked(api.transferSkill).mockRejectedValue(new Error("Permission changed"));const qc=show();await select();fireEvent.click(screen.getByRole("button",{name:"Transfer now"}));await waitFor(()=>expect(api.transferSkill).toHaveBeenCalled());expect(close).not.toHaveBeenCalled();expect((qc.getQueryData(["workspaces","ws","skills","skill"]) as Skill).created_by).toBe("old");
 });
 it("does not allow an old search selection after the query changes",async()=>{
  show();fireEvent.change(screen.getByLabelText("Recipient",{selector:"input"}),{target:{value:"new"}});fireEvent.click(await screen.findByRole("radio"));fireEvent.change(screen.getByLabelText("Recipient",{selector:"input"}),{target:{value:"someone else"}});expect(screen.getByRole("button",{name:"Continue"})).toBeDisabled();
 });
});
