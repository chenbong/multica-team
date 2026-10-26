"""Checked integration patches for platform expansion."""
import json
from pathlib import Path

COPY = {
 "copy":"Copy an existing agent","copy_hint":"Choose a visible user agent from any workspace. Select a new runtime before creating.",
 "search":"Search agents or workspaces","loading":"Loading...","failed":"Unable to complete this operation. Please retry.",
 "runtime":"Change runtime","preview":"Preview changes","confirm":"Confirm changes","cancel":"Cancel","save":"Save","add":"Add",
 "edit":"Edit","remove":"Delete","name":"Name","description":"Description","value":"Secret content",
 "secrets":"Personal secrets","add_secret":"Add secret","edit_secret":"Edit secret","empty":"No secrets",
 "keep_value":"Leave empty to keep the existing value","reveal":"Show current value",
 "secret_warning":"Your agents can read these secrets, including runs triggered by other users. Sharing an agent grants use of its owner's credentials.",
 "delete_warning":"Delete this secret? Future reads will be blocked; values already retrieved cannot be recalled.",
 "copy_skipped":"Unavailable skills or access targets were skipped. Review the configuration before creating.",
 "runtime_hint":"Only future runs change. Unsupported model and thinking overrides use the target defaults.",
 "skipped":"Skipped: no permission or archived","success":"Completed","changed":"Configuration changed; preview again",
 "defaults":"Runtime default","close":"Close","choose_runtime":"Select runtime",
}
ZH = {
 "copy":"从已有智能体复制","copy_hint":"可选择其他工作区中有权查看的用户智能体。创建前需要重新选择运行时。",
 "search":"搜索智能体或工作区","loading":"加载中...","failed":"无法完成操作，请重试。",
 "runtime":"修改运行时","preview":"预览修改","confirm":"确认修改","cancel":"取消","save":"保存","add":"添加",
 "edit":"编辑","remove":"删除","name":"名称","description":"说明","value":"密钥内容",
 "secrets":"个人密钥","add_secret":"新增密钥","edit_secret":"编辑密钥","empty":"暂无密钥",
 "keep_value":"留空则保留原值","reveal":"显示当前值",
 "secret_warning":"你的智能体可以读取这些密钥，包括其他用户触发的运行。共享智能体也会授予其所有者的凭证使用能力。",
 "delete_warning":"确认删除？后续读取将被禁止，已经读取的值无法收回。",
 "copy_skipped":"已跳过不可用的 Skill 或访问对象，请在创建前核对配置。",
 "runtime_hint":"仅影响后续运行。不兼容的模型和思考配置将使用目标运行时默认值。",
 "skipped":"跳过：无修改权限或已归档","success":"已完成","changed":"配置已变化，请重新预览",
 "defaults":"运行时默认值","close":"关闭","choose_runtime":"选择运行时",
}

def extend(changes):
 def add(path,old,new):changes.setdefault(path,[]).append((old,new))
 picker="packages/views/agents/components/runtime-picker.tsx"
 add(picker,"  disabled = false,","  disabled = false,\n  autoSelect = true,")
 add(picker,"  disabled?: boolean;","  disabled?: boolean;\n  autoSelect?: boolean;")
 add(picker,'    if (selectedRuntimeId !== "") return;','    if (!autoSelect || selectedRuntimeId !== "") return;')
 add(picker,"  }, [filteredRuntimes, selectedRuntimeId, currentUserId, onSelect]);","  }, [autoSelect, filteredRuntimes, selectedRuntimeId, currentUserId, onSelect]);")
 add(picker,"    setFilter(next);","    setFilter(next);\n    if (!autoSelect) return;")
 add(picker,": t(($) => $.create_dialog.runtime_none)}",": !autoSelect && runtimes.length > 0 ? t(($) => $.platform_expansion.choose_runtime) : t(($) => $.create_dialog.runtime_none)}")
 panel="packages/views/agents/create/agent-configuration-panel.tsx"
 add(panel,"  compact = false,\n  onRuntimeSelect,","  compact = false,\n  autoSelectRuntime = true,\n  onRuntimeSelect,")
 add(panel,"  compact?: boolean;\n  /** Builder sessions","  compact?: boolean;\n  autoSelectRuntime?: boolean;\n  /** Builder sessions")
 add(panel,"                selectedRuntimeId={draft.runtimeId}","                selectedRuntimeId={draft.runtimeId}\n                autoSelect={autoSelectRuntime}")
 add("packages/views/agents/create/manual-create-agent-page.tsx","            draft={form.draft}","            draft={form.draft}\n            autoSelectRuntime={!duplicateId}")
 picker_test="packages/views/agents/components/runtime-picker.test.tsx"
 add(picker_test,'describe("RuntimePicker (creation studio)", () => {','describe("RuntimePicker (creation studio)", () => {\n  it("requires an explicit selection when autoSelect is disabled", () => {\n    const runtimes=[...RUNTIMES,makeRuntime({id:"other",owner_id:"other-user",visibility:"public"})];\n    const { onSelect, getByRole } = renderPicker({runtimes, selectedRuntimeId: "", autoSelect: false });\n    expect(onSelect).not.toHaveBeenCalled();\n    fireEvent.click(getByRole("button", { name: "All" }));\n    expect(onSelect).not.toHaveBeenCalled();\n  });')
 for lang in ("en","zh-Hans","ja","ko","fr"):
  for ns in ("settings","agents"):
   add(f"packages/views/locales/{lang}/{ns}.json","{\n  \"","{\n  \"platform_expansion\": "+json.dumps(ZH if lang=="zh-Hans" else COPY,ensure_ascii=False)+",\n  \"")
  path=f"packages/views/locales/{lang}/agents.json"
  original=json.loads((Path(__file__).resolve().parent.parent/"multica"/path).read_text()).get("creation_studio",{}).get("duplicate_env_notice")
  if original:
   notice="复制指令、模型、思考配置及可用 Skill；头像、运行时、环境变量、MCP 配置和如流绑定不会沿用。" if lang=="zh-Hans" else "Copies instructions, model, thinking settings and available skills. Avatar, runtime, environment values, MCP configuration and robot bindings are not inherited."
   add(path,'"duplicate_env_notice": '+json.dumps(original,ensure_ascii=False),'"duplicate_env_notice": '+json.dumps(notice,ensure_ascii=False))
 add("packages/core/package.json",'\"./api/schema\": \"./api/schema.ts\",','\"./api/schema\": \"./api/schema.ts\",\n    \"./api/platform-expansion\": \"./api/platform-expansion.ts\",\n    \"./agents/safe-copy\": \"./agents/safe-copy.ts\",')
 add("packages/core/api/client.ts",'import type { ZodType } from "zod";','import type { ZodType } from "zod";\nimport {parseSecretMetadata,parseSecretList,parsePersonalSecretValue,parseCopySource,type PersonalSecretMetadata} from "./platform-expansion";')
 methods = '''
  async listPersonalSecrets():Promise<{secrets:PersonalSecretMetadata[]}> {
    const raw=await this.fetch<unknown>("/api/me/secrets");
    return parseSecretList(raw);
  }
  async readPersonalSecret(id:string) {
    return parsePersonalSecretValue(await this.fetch<unknown>(`/api/me/secrets/${encodeURIComponent(id)}`));
  }
  async savePersonalSecret(id:string|null,data:{name:string;description:string;value?:string}) {
    const raw=await this.fetch<unknown>(`/api/me/secrets${id?"/"+encodeURIComponent(id):""}`,{method:id?"PUT":"POST",body:JSON.stringify(data)});
    const item=parseSecretMetadata(raw);
    if(!item)throw new Error("Invalid secret metadata response");
    return item;
  }
  async deletePersonalSecret(id:string) {
    await this.fetchRaw(`/api/me/secrets/${encodeURIComponent(id)}`,{method:"DELETE"});
  }
  async getCopySource(workspaceId:string,id:string):Promise<Agent> {
    const raw=await this.fetch<unknown>(`/api/agents/${encodeURIComponent(id)}`,{headers:{"X-Workspace-ID":workspaceId,"X-Workspace-Slug":""}});
    const source=parseCopySource(raw);
    if(!source||source.system_key)throw new Error("This agent cannot be copied");
    return source;
  }
  async listCopySources():Promise<{workspaceName:string;agent:Agent}[]> {
    const workspaces=await this.listWorkspaces();
    const result:{workspaceName:string;agent:Agent}[]=[];
    for(const ws of workspaces) {
      const raw=await this.fetch<unknown>("/api/agents",{headers:{"X-Workspace-ID":ws.id,"X-Workspace-Slug":""}});
      if(!Array.isArray(raw))throw new Error("Invalid agent list response");
      for(const item of raw){const agent=parseCopySource(item);if(agent&&!agent.system_key)result.push({workspaceName:ws.name,agent});}
    }
    return result;
  }
'''
 add("packages/core/api/client.ts","  // Agents\n",methods+"\n  // Agents\n")
 add("packages/views/settings/components/account-tab.tsx",'import { useAutoSave } from "./use-auto-save";','import { useAutoSave } from "./use-auto-save";\nimport { PersonalSecretsSection } from "./personal-secrets-section";')
 add("packages/views/settings/components/account-tab.tsx","    </SettingsTab>","      <PersonalSecretsSection key={user?.id} />\n    </SettingsTab>")
 add("packages/views/agents/create/choose-create-method-page.tsx",'import { withSquadParam } from "./squad-param";','import { withSquadParam } from "./squad-param";\nimport {CopyAgentChooser} from "./copy-agent-chooser";')
 add("packages/views/agents/create/choose-create-method-page.tsx","      </div>\n    </div>\n  );","      </div>\n      <CopyAgentChooser blankHref={blankHref}/>\n    </div>\n  );")
 add("packages/views/agents/create/use-duplicate-draft-seed.ts",'import { useEffect, useRef } from "react";','import { useEffect, useRef } from "react";\nimport {safeCopyDraft} from "@multica/core/agents/safe-copy";')
 add("packages/views/agents/create/use-duplicate-draft-seed.ts",'import { buildDuplicateDraft, type AgentDraft }','import { type AgentDraft }')
 add("packages/views/agents/create/use-duplicate-draft-seed.ts","    onSeedRef.current(draft, draft.runtimeId !== source.runtime_id);","    onSeedRef.current(draft, false);")
 seed_test="packages/views/agents/create/use-duplicate-draft-seed.test.tsx"
 add(seed_test,'runtimeId: "runtime-1",\n      model:', 'runtimeId: "",\n      model:')
 add(seed_test,'runtimeId: "runtime-2",\n      model: "",\n      thinkingLevel: "",\n      serviceTier: "",','runtimeId: "",\n      model: "gpt-5.6-sol",\n      thinkingLevel: "high",\n      serviceTier: "priority",')
 add(seed_test,'    expect(runtimeReset).toBe(true);','    expect(runtimeReset).toBe(false);')
 add(seed_test,'    expect(onSeed.mock.calls[0]![1]).toBe(true);','    expect(onSeed.mock.calls[0]![1]).toBe(false);')
 add("packages/views/agents/create/use-duplicate-draft-seed.ts",'''    const draft = buildDuplicateDraft(source, {
      runtimes,
      currentUserId,
      fallbackRuntimeId,
      nameSuffix,
    });''','''    const draft = safeCopyDraft(source,nameSuffix);''')
 manual="packages/views/agents/create/manual-create-agent-page.tsx"
 add(manual,'import { useState } from "react";','import { useState } from "react";\nimport {api} from "@multica/core/api";')
 add(manual,'import { agentListOptions } from "@multica/core/workspace/queries";','import { agentListOptions,skillListOptions,memberListOptions } from "@multica/core/workspace/queries";')
 add(manual,'  const { data: agents = [] } = useQuery(agentListOptions(wsId));','  const { data: agents = [] } = useQuery(agentListOptions(wsId));\n  const copySkillsQuery=useQuery(skillListOptions(wsId));\n  const copyMembersQuery=useQuery(memberListOptions(wsId));')
 add(manual,'    runtimesSettled: form.runtimesSettled,','    runtimesSettled: form.runtimesSettled && (!duplicateId || (copySkillsQuery.isSuccess && copyMembersQuery.isSuccess)),')
 add(manual,'  const form = useCreateAgentForm();','  const form = useCreateAgentForm(duplicateId?{runtimeSeed:{ready:false,runtimeId:""}}:undefined);')
 add(manual,'''  const duplicateAgent = duplicateId
    ? agents.find((agent) => agent.id === duplicateId) ?? null
    : null;''','''  const sourceWorkspace=navigation.searchParams.get("duplicate_ws")??wsId;
  const sourceQuery=useQuery({queryKey:["agent-copy-source",sourceWorkspace,duplicateId],queryFn:()=>api.getCopySource(sourceWorkspace,duplicateId!),enabled:!!duplicateId});
  const duplicateAgent=sourceQuery.data??(duplicateId?agents.find(agent=>agent.id===duplicateId&&!agent.system_key)??null:null);''')
 add(manual,"    duplicateSource: duplicateAgent,","    duplicateSource: duplicateAgent?{...duplicateAgent,custom_args:[]}:null,")
 add(manual,'form.draft.name.trim().length > 0 && form.draftReady && !submit.creating;','form.draft.name.trim().length > 0 && form.draftReady && !submit.creating && (!duplicateId || (!!duplicateAgent && !sourceQuery.isError));')
 add(manual,"      form.setDraft(duplicated);",'''      const skills=new Set(form.workspaceSkills.map(s=>s.id));
      const members=new Set(form.members.map(m=>m.user_id));
      form.setDraft({...duplicated,skillIds:new Set([...duplicated.skillIds].filter(id=>skills.has(id))),
        memberIds:new Set([...duplicated.memberIds].filter(id=>members.has(id))),
        teamIds:sourceWorkspace===wsId?duplicated.teamIds:new Set()});''')
 add(manual,"            draft={form.draft}","            draft={form.draft}\n            onRuntimeSelect={duplicateId?(id)=>form.setDraft(current=>({...current,runtimeId:id})):undefined}")
 add(manual,"          {duplicateRuntimeReset && (","          {sourceQuery.isError && <p role=\"alert\">{t($=>$.platform_expansion.failed)}</p>}\n          {duplicateId && <p className=\"mb-4 text-caption text-muted-foreground\">{t($=>$.platform_expansion.copy_skipped)}</p>}\n          {duplicateRuntimeReset && (")
 add("packages/views/agents/components/agent-batch-toolbar.tsx",'import type { AgentListRow } from "./agents-page";','import type { AgentListRow } from "./agents-page";\nimport {BulkRuntimeChange} from "./bulk-runtime-change";')
 add("packages/views/agents/components/agent-batch-toolbar.tsx","        {/* Archive sits last:",'        <BulkRuntimeChange rows={rows} members={members} currentUserId={currentUserId} onDone={()=>{void invalidate();onClear();}} />\n        {/* Archive sits last:')
