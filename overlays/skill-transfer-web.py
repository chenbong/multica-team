"""Ownership handoff and a once-per-browser Enter-send migration."""
import json

def extend(changes):
 def add(path,old,new): changes.setdefault(path,[]).append((old,new))
 add('packages/core/api/client.ts','  async refreshSkill(id: string, options?: {url:string}): Promise<Skill> {','''  async skillTransferRecipients(id:string, query:string):Promise<Array<{user_id:string;name:string;email:string}>> {
    return this.fetch(`/api/skills/${encodeURIComponent(id)}/transfer-recipients?q=${encodeURIComponent(query)}`);
  }

  async transferSkill(id:string, recipientUserId:string):Promise<Skill> {
    return this.fetch(`/api/skills/${encodeURIComponent(id)}/transfer`,{method:"POST",body:JSON.stringify({recipient_user_id:recipientUserId})});
  }

  async refreshSkill(id: string, options?: {url:string}): Promise<Skill> {''')
 for file in ['skill-list-actions.tsx','skill-detail-page.tsx']:
  path='packages/views/skills/components/'+file
  add(path,'import { SkillScopeDialog } from "./skill-scope-dialog";','import { SkillScopeDialog } from "./skill-scope-dialog";\nimport { SkillTransferDialog } from "./skill-transfer-dialog";')
 row='packages/views/skills/components/skill-list-actions.tsx'
 add(row,'  const [scopeOpen, setScopeOpen] = useState(false);','  const [scopeOpen, setScopeOpen] = useState(false);\n  const [transferOpen,setTransferOpen]=useState(false);')
 add(row,'          {row.canChangeScope && (','''          {row.canEdit && (row.skill.scope === "team" || row.skill.scope === "workspace") && (
            <DropdownMenuItem onClick={()=>setTransferOpen(true)}>{t(($)=>$.transfer.action)}</DropdownMenuItem>
          )}
          {row.canChangeScope && (''')
 add(row,'      <DeleteSkillsDialog\n        rows={[row]}','''      {row.canEdit && (row.skill.scope === "team" || row.skill.scope === "workspace") && (
        <SkillTransferDialog skill={row.skill} open={transferOpen} onOpenChange={setTransferOpen}/>
      )}
      <DeleteSkillsDialog
        rows={[row]}''')
 detail='packages/views/skills/components/skill-detail-page.tsx'
 add(detail,'  const [changeScopeOpen, setChangeScopeOpen] = useState(false);','  const [changeScopeOpen, setChangeScopeOpen] = useState(false);\n  const [transferOpen,setTransferOpen]=useState(false);')
 add(detail,'            {skill.created_by === currentUserId && (','''            {canEdit && (skill.scope === "team" || skill.scope === "workspace") && (
              <Button variant="outline" size="xs" onClick={()=>setTransferOpen(true)}>{t(($)=>$.transfer.action)}</Button>
            )}
            {skill.created_by === currentUserId && (''')
 add(detail,'      <AddToAgentDialog\n        skills={[skill]}','''      {canEdit && (skill.scope === "team" || skill.scope === "workspace") && (
        <SkillTransferDialog skill={skill} open={transferOpen} onOpenChange={setTransferOpen} onTransferred={adoptServerVersion}/>
      )}
      <AddToAgentDialog
        skills={[skill]}''')
 labels={
 'action':('转让','Transfer'),'title':('转让 Skill','Transfer skill'),
 'description':('选择新的添加者。转让立即生效，你将失去管理权限，无法自行撤回。','Choose a new creator. Transfer is immediate; you lose management access and cannot reclaim it.'),
 'search':('接收人','Recipient'),'placeholder':('搜索姓名或邮箱，至少输入 2 个字符','Search name or email, at least 2 characters'),
 'empty':('没有找到匹配用户，请细化姓名或邮箱','No matching users. Refine the name or email.'),
 'search_failed':('查询用户失败，请重试','Unable to search users. Please retry.'),
 'review':('下一步','Continue'),'confirm_title':('确认转让','Confirm transfer'),
 'confirm':('确认转让','Transfer now'),'back':('返回','Back'),'cancel':('取消','Cancel'),
 'summary':('将“{{skill}}”转让给 {{name}}（{{email}}）。名称、内容、来源、级别和智能体绑定保持不变；对方将获得完整管理权限并可再次转让。','Transfer “{{skill}}” to {{name}} ({{email}}). Its name, content, source, scope and agent assignments stay unchanged. The recipient gets full management and transfer rights.'),
 'saved':('Skill 已转让','Skill transferred'),'failed':('转让失败，请刷新后重试','Transfer failed. Refresh and retry.'),'saving':('正在转让…','Transferring…')}
 for locale in ['en','zh-Hans','fr','ja','ko']:
  obj={k:v[0 if locale=='zh-Hans' else 1] for k,v in labels.items()}
  add('packages/views/locales/'+locale+'/skills.json','{\n  "page":','{\n  "transfer": '+json.dumps(obj,ensure_ascii=False)+',\n  "page":')
 add('packages/core/shortcuts/definitions.ts','{ id: "send", category: "general", defaultShortcut: primary("Enter"),','{ id: "send", category: "general", defaultShortcut: createShortcutChord("Enter"),')
 add('packages/core/shortcuts/store.ts','return { overrides: sanitizeShortcutOverrides(current?.overrides) };','''const overrides=sanitizeShortcutOverrides(current?.overrides);
    // v3 deliberately resets only Send once. Later manual choices survive.
    if(version<3) delete overrides.send;
    return { overrides };''')
 add('packages/core/shortcuts/store.ts','  return { overrides: sanitizeShortcutOverrides(overrides) };','  delete overrides.send;\n  return { overrides: sanitizeShortcutOverrides(overrides) };')
 add('packages/core/shortcuts/store.ts','      version: 2,','      version: 3,')

 add('packages/views/editor/title-editor.shortcut.test.tsx','it("fires onSubmitShortcut on the default Cmd+Enter chord", async () => {','it("fires onSubmitShortcut on an explicitly configured Cmd+Enter chord", async () => {\n    useShortcutStore.setState({ overrides: { send: createShortcutChord("Enter", { primary: true }) } });')
 add('packages/views/settings/components/keyboard-shortcuts-tab.test.tsx','fireEvent.keyDown(sendRecorder, { key: "Enter", shiftKey: true });\n    expect(getShortcut("send")).toEqual(\n      createShortcutChord("Enter", { primary: true }),','fireEvent.keyDown(sendRecorder, { key: "Enter", shiftKey: true });\n    expect(getShortcut("send")).toEqual(\n      createShortcutChord("Enter"),')
