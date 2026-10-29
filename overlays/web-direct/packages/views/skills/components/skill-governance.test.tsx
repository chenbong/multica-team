// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import type { Skill, SkillSummary } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enSkills from "../../locales/en/skills.json";
vi.mock("@multica/core/api",()=>({api:{refreshSkill:vi.fn()}}));
vi.mock("sonner",()=>({toast:{success:vi.fn(),error:vi.fn()}}));
import {api} from "@multica/core/api";
import {RefreshSkillDialog} from "./refresh-skill-dialog";
const skill:SkillSummary={id:"skill",workspace_id:"ws",name:"kept-name",description:"",config:{},created_by:"creator",created_at:"",updated_at:""};
const close=vi.fn();
function show(){return render(<I18nProvider locale="en" resources={{en:{common:enCommon,skills:enSkills}}}><QueryClientProvider client={new QueryClient()}><RefreshSkillDialog skill={skill} origin={null} wsId="ws" open onOpenChange={close}/></QueryClientProvider></I18nProvider>)}
beforeEach(()=>{vi.clearAllMocks()});
describe("creator-selected skill update source",()=>{
 it("accepts a new address for a manual skill and sends it with the update",async()=>{
  vi.mocked(api.refreshSkill).mockResolvedValue({...skill,content:"new body",files:[]} as Skill);
  show();const input=screen.getByLabelText("Download URL");const button=screen.getByRole("button",{name:/^Update$/i});expect(button).toBeDisabled();
  fireEvent.change(input,{target:{value:"https://github.com/acme/skills/tree/main/helper"}});fireEvent.click(button);
  await waitFor(()=>expect(api.refreshSkill).toHaveBeenCalledWith("skill",{url:"https://github.com/acme/skills/tree/main/helper"}));
  await waitFor(()=>expect(close).toHaveBeenCalledWith(false));
 });
 it("keeps the dialog open when downloading the new source fails",async()=>{
  vi.mocked(api.refreshSkill).mockRejectedValue(new Error("download unavailable"));show();
  fireEvent.change(screen.getByLabelText("Download URL"),{target:{value:"https://github.com/acme/missing"}});fireEvent.click(screen.getByRole("button",{name:/^Update$/i}));
  await waitFor(()=>expect(api.refreshSkill).toHaveBeenCalled());
  expect(close).not.toHaveBeenCalled();expect(screen.getByLabelText("Download URL")).toHaveValue("https://github.com/acme/missing");
 });
});
