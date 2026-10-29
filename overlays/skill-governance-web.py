"""Skill names, creator identity, and source updates in an isolated Web tree."""
import json

def extend(changes):
 def add(path,old,new):changes.setdefault(path,[]).append((old,new))
 add('packages/core/types/agent.ts','export interface SkillSummary {','export interface SkillSummary {\n  creator?: {user_id:string;name:string;avatar_url:string|null}|null;')
 add('packages/core/api/schemas.ts','export const SkillSummarySchema = z.object({','export const SkillCreatorSchema = z.object({user_id:z.string(),name:z.string(),avatar_url:z.string().nullable().catch(null)});\n\nexport const SkillSummarySchema = z.object({\n  creator: SkillCreatorSchema.nullable().optional().catch(null),')
 add('packages/core/api/schemas.ts','export const SkillImportExistingSkillSchema = z.object({','export const SkillImportExistingSkillSchema = z.object({\n  scope:z.string().optional(),workspace_name:z.string().optional(),creator:SkillCreatorSchema.nullable().optional().catch(null),')
 add('packages/core/types/agent.ts','  existing_skill?: {','  existing_skill?: {\n    scope?:string;workspace_name?:string;creator?:{user_id:string;name:string;avatar_url:string|null}|null;')
 add('packages/core/api/client.ts','async refreshSkill(id: string): Promise<Skill> {','async refreshSkill(id: string, options?: {url:string}): Promise<Skill> {')
 add('packages/core/api/client.ts','`/api/skills/${id}/refresh`, {\n      method: "POST",','`/api/skills/${id}/refresh`, {\n      body: options ? JSON.stringify(options) : undefined,\n      method: "POST",')
 add('packages/views/skills/components/skill-list-filter.ts','  creator: MemberWithUser | null;','  creator: Pick<MemberWithUser,"user_id"|"name"|"avatar_url"> | null;')
 add('packages/views/skills/components/skills-page.tsx','creator: MemberWithUser | null','creator: SkillRow["creator"]')
 add('packages/views/skills/components/skills-page.tsx','creator: skill.created_by\n          ? membersById.get(skill.created_by) ?? null\n          : null,','creator: skill.creator ?? (skill.created_by\n          ? membersById.get(skill.created_by) ?? null\n          : null),')
 add('packages/views/skills/components/skill-detail-page.tsx','  MemberWithUser,\n','')
 add('packages/views/skills/components/skill-detail-page.tsx','  isRefreshableOrigin,\n','')
 add('packages/views/skills/components/skill-list-toolbar.tsx','Agent, Label, MemberWithUser','Agent, Label')
 add('packages/views/skills/components/skill-list-toolbar.tsx','member: MemberWithUser','member: NonNullable<SkillRow["creator"]>')
 add('packages/views/skills/components/skill-detail-page.tsx','  SkillFile,','  SkillFile,\n  SkillSummary,')
 add('packages/views/skills/components/skill-detail-page.tsx','  creator: MemberWithUser | null;','  creator: NonNullable<SkillSummary["creator"]> | null;')
 add('packages/views/skills/components/skill-detail-page.tsx','const creator = useMemo<MemberWithUser | null>(\n    () =>\n      skill?.created_by\n        ? members.find((m) => m.user_id === skill.created_by) ?? null\n        : null,\n    [members, skill?.created_by],','const creator = useMemo<NonNullable<SkillSummary["creator"]> | null>(\n    () => skill?.creator ?? (skill?.created_by ? members.find((m) => m.user_id === skill.created_by) ?? null : null),\n    [members, skill?.created_by, skill?.creator],')
 add('packages/views/skills/components/skill-list-actions.tsx','const canRefresh = row.canEdit && isRefreshableOrigin(origin);','const canRefresh = row.canEdit;')
 add('packages/views/skills/components/skill-detail-page.tsx','{canEdit && origin && isRefreshableOrigin(origin) && (','{canEdit && (')
 add('packages/views/skills/components/skills-page.tsx','import { useT, useTimeAgo } from "../../i18n";','import { useT, useTimeAgo } from "../../i18n";\nimport { readSkillCreator } from "../lib/skill-creator";')
 add('packages/views/skills/components/skills-page.tsx','creator: skill.creator ?? (skill.created_by\n          ? membersById.get(skill.created_by) ?? null\n          : null),','creator: readSkillCreator(skill,members),')
 add('packages/views/skills/components/skill-detail-page.tsx','import { useT } from "../../i18n";','import { useT } from "../../i18n";\nimport { readSkillCreator } from "../lib/skill-creator";')
 add('packages/views/skills/components/skill-detail-page.tsx','() => skill?.creator ?? (skill?.created_by ? members.find((m) => m.user_id === skill.created_by) ?? null : null),','() => readSkillCreator(skill,members),')
 add('packages/views/skills/components/skills-page.tsx','  const membersById = useMemo(() => {\n    const map = new Map<string, MemberWithUser>();\n    for (const m of members) map.set(m.user_id, m);\n    return map;\n  }, [members]);','')
 add('packages/views/skills/components/skills-page.tsx','[skills, assignments, membersById, runtimesById, currentUserId, myRole]','[skills, assignments, members, runtimesById, currentUserId, myRole]')
 add('packages/core/types/agent.ts','export interface RuntimeLocalSkillImportConflict {','export interface RuntimeLocalSkillImportConflict {\n  existing_name?:string;existing_scope?:string;existing_creator_name?:string;')
 add('packages/views/skills/components/runtime-local-skill-import-panel.tsx','const creatorName = creatorId\n            ? members.find((m) => m.user_id === creatorId)?.name\n            : undefined;','const creatorName = r.conflict?.existing_creator_name ?? (creatorId ? members.find((m) => m.user_id === creatorId)?.name : undefined);')
 add('packages/views/skills/components/runtime-local-skill-import-panel.tsx','<div className="truncate text-body font-medium">{r.name}</div>','<div className="truncate text-body font-medium">{r.name}</div>\n                  {r.conflict?.existing_scope && <p className="mt-1 text-caption text-muted-foreground">{r.conflict.existing_scope} · {creatorName}</p>}')
 add('packages/core/api/schemas.ts','export const SkillCreatorSchema =','export const RuntimeLocalSkillConflictSchema=z.object({existing_skill_id:z.string().catch(""),can_overwrite:z.boolean().catch(false),existing_created_by:z.string().optional().catch(undefined),existing_name:z.string().optional().catch(undefined),existing_scope:z.string().optional().catch(undefined),existing_creator_name:z.string().optional().catch(undefined)});\n\nexport const SkillCreatorSchema =')
 add('packages/core/api/client.ts','  SkillSummaryListSchema,','  SkillSummaryListSchema,\n  RuntimeLocalSkillConflictSchema,')
 add('packages/core/api/client.ts','    return this.fetch(`/api/runtimes/${runtimeId}/local-skills/import/${requestId}`);','    const result=await this.fetch<RuntimeLocalSkillImportRequest>(`/api/runtimes/${runtimeId}/local-skills/import/${requestId}`);\n    if(result.conflict) result.conflict=parseWithFallback(result.conflict,RuntimeLocalSkillConflictSchema,{existing_skill_id:"",can_overwrite:false},{endpoint:"runtime local skill conflict"});\n    return result;')
 for locale in ['en','zh-Hans','ja','ko','fr']:
  zh=locale=='zh-Hans';file=f'packages/views/locales/{locale}/skills.json'
  title='更新这个 Skill？' if zh else 'Update this skill?'
  description='更新“{{name}}”的说明、SKILL.md 和文件，保留平台名称及绑定。' if zh else 'Update the description, SKILL.md and files for “{{name}}”, keeping its platform name and assignments.'
  # Transform the locale after earlier checked changes by matching its exact upstream text.
  from pathlib import Path
  data=json.loads((Path(__file__).parent.parent/'multica'/file).read_text())
  old=data['detail']['refresh']['dialog']
  for key,value in [('title',title),('description',description)]:
   add(file,json.dumps(key)+': '+json.dumps(old[key],ensure_ascii=False),json.dumps(key)+': '+json.dumps(value,ensure_ascii=False))
  add(file,'"refresh": {','"refresh": {\n      "source_saved": '+json.dumps('Skill 已更新，来源地址已保存' if zh else 'Skill updated; source URL saved',ensure_ascii=False)+',\n      "download_url": '+json.dumps('下载地址' if zh else 'Download URL',ensure_ascii=False)+',\n      "url_placeholder": '+json.dumps('粘贴 Skill 下载地址或安装文本' if zh else 'Paste a skill URL or copied install text',ensure_ascii=False)+',')
