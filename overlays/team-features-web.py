"""Checked integration for the six collaboration features."""
import json
import os
from urllib.parse import urlsplit


def extend(changes):
 def add(path, old, new): changes.setdefault(path, []).append((old, new))
 add('packages/core/api/client.ts', 'import type { ZodType } from "zod";', 'import type { ZodType } from "zod";\nimport { parseOneAPIQuota } from "./oneapi-quota";')
 add('packages/core/api/client.ts', '  async deleteRuntime(runtimeId: string): Promise<void> {', '  async getOneAPIQuota() { return parseOneAPIQuota(await this.fetch<unknown>("/api/me/oneapi-quota")); }\n\n  async deleteRuntime(runtimeId: string): Promise<void> {')
 add('packages/core/runtimes/index.ts','export * from "./access";', 'export * from "./access";\nexport * from "./oneapi-quota";')
 add('packages/views/layout/app-sidebar.tsx', 'import { SidebarShortcutLinks, SidebarCommunityLink } from "./sidebar-shortcut-links";', 'import { SidebarShortcutLinks, SidebarCommunityLink } from "./sidebar-shortcut-links";\nimport { OneAPIQuota } from "./oneapi-quota";')
 account_url = os.environ.get("MULTICA_ONEAPI_ACCOUNT_URL", "").strip()
 if account_url:
  parsed = urlsplit(account_url)
  if parsed.scheme != "https" or not parsed.hostname or parsed.username or parsed.password:
   raise ValueError("OneAPI account page must be a trusted HTTPS URL")
 account_href = json.dumps(account_url) if account_url else "`${p.settings()}?tab=profile#ducc`"
 add('packages/views/layout/app-sidebar.tsx', '{utilityNav.map((item) => {', '{utilityNav.filter(item => item.key !== "usage").map((item) => {')
 add('packages/views/layout/app-sidebar.tsx', '            })}\n          </SidebarMenu>\n        </SidebarFooter>', '            })}\n            {utilityNav.some(item => item.key === "usage") && <SidebarMenuItem><OneAPIQuota href={' + account_href + '} /></SidebarMenuItem>}\n          </SidebarMenu>\n        </SidebarFooter>')
 add('packages/views/layout/app-sidebar.tsx', '<SidebarShortcutLinks modelTraceUrl=', '<SidebarShortcutLinks usageHref={utilityNav.some(item => item.key === "usage") ? p.usage() : undefined} modelTraceUrl=')
 en={'label':'OneAPI','accessible':'OneAPI: {{value}}','unlimited':'Unlimited','not_linked':'Not linked','invalid':'Re-import required','loading':'Loading...','unavailable':'Unavailable','remaining':'Remaining {{value}}','stale':'Not updated','import_hint':'Import your ducc login in Personal profile.'}
 zh={'label':'OneAPI','accessible':'OneAPI：{{value}}','unlimited':'不限额','not_linked':'未关联','invalid':'需重新导入','loading':'加载中...','unavailable':'暂不可用','remaining':'剩余 {{value}}','stale':'未更新','import_hint':'请在个人资料中导入 ducc 登录凭据。'}
 for lang in ('en','zh-Hans','ja','ko','fr'):
  add(f'packages/views/locales/{lang}/layout.json','{\n  "','{\n  "oneapi_quota": '+json.dumps(zh if lang=='zh-Hans' else en,ensure_ascii=False)+',\n  "')
 project='packages/views/projects/components/project-resources-section.tsx'
 add(project,'import { GithubRefDialog } from "./github-ref-dialog";','import { GithubRefDialog } from "./github-ref-dialog";\nimport { RemoteWorkingDirectory } from "./remote-working-directory";')
 add(project,'          {desktopMode && (','          {!desktopMode && <RemoteWorkingDirectory projectId={projectId} />}\n          {desktopMode && (')
 for lang in ('en','zh-Hans','ja','ko','fr'):
  en={'title': 'Working directory', 'hint': 'Enter a directory path on the execution machine; no machine selection is needed. Each task uses this directory if it exists on its machine, otherwise it keeps the original default working directory. Existing directories must be readable and writable.', 'path': 'Absolute directory path', 'failed': 'Unable to save working directory', 'cancel': 'Cancel', 'save': 'Save', 'remove': 'Remove configuration', 'compatibility': 'This feature requires a compatible Team Mesh daemon. Older clients need an update before using it.', 'legacy': 'Legacy per-machine directory settings are retained. This project-wide path takes priority while configured; removing it restores those settings.'}
  zh={'title': '工作目录', 'hint': '填写执行机器上的目录路径，无需选择机器。每次运行时，机器上有此目录就直接使用，不存在则沿用原来的默认工作目录；已有目录需可读写。', 'path': '目录绝对路径', 'failed': '无法保存工作目录', 'cancel': '取消', 'save': '保存', 'remove': '移除配置', 'compatibility': '此功能需要新版 Team Mesh daemon 支持，旧版客户端需更新后才能使用。', 'legacy': '原有按机器配置的目录会保留。设置此路径后优先使用此路径；移除后恢复原有配置。'}
  add(f'packages/views/locales/{lang}/projects.json','{\n  "','{\n  "remote_directory": '+json.dumps(zh if lang=='zh-Hans' else en,ensure_ascii=False)+',\n  "')
 add('packages/core/api/client.ts','import type { ZodType } from "zod";','import type { ZodType } from "zod";\nimport { parseIssuePermissions } from "./issue-permissions";')
 add('packages/core/api/client.ts','  async deleteRuntime(runtimeId: string): Promise<void> {', '  async getIssuePermissions(id: string) { return parseIssuePermissions(await this.fetch<unknown>(`/api/issues/${encodeURIComponent(id)}/permissions`)); }\n\n  async deleteRuntime(runtimeId: string): Promise<void> {')
 add('packages/core/issues/index.ts','export * from "./store";','export * from "./store";\nexport * from "./issue-permissions";')
 actions='packages/views/issues/actions/issue-actions-menu-items.tsx'
 add(actions,'import { resolveWorkdirCopyTarget } from "@multica/core/issues";','import { resolveWorkdirCopyTarget, useIssuePermissions } from "@multica/core/issues";')
 add(actions,'  const wsId = useWorkspaceId();','  const wsId = useWorkspaceId();\n  const permissions = useIssuePermissions(wsId, issue.id);')
 add(actions,'        variant="destructive"\n        onClick={() => openDeleteConfirm','        variant="destructive"\n        disabled={!permissions.data?.canDelete}\n        title={permissions.data?.canDelete ? undefined : t($ => $.issue_permissions.delete_hint)}\n        onClick={() => openDeleteConfirm')
 for lang in ('en','zh-Hans','ja','ko','fr'):
  en={'delete_hint':'Only the task creator, creator agent owner, or workspace owner/admin can delete this task.'}
  zh={'delete_hint':'只有任务创建者、创建智能体的所有者或工作区 owner/管理员可以删除此任务。'}
  add(f'packages/views/locales/{lang}/issues.json','{\n  "','{\n  "issue_permissions": '+json.dumps(zh if lang=='zh-Hans' else en,ensure_ascii=False)+',\n  "')

 add('packages/core/constants/upload.ts','100 * 1024 * 1024','50 * 1024 * 1024')
 add('apps/web/next.config.ts','  transpilePackages:', '  experimental: { proxyClientMaxBodySize: 52 * 1024 * 1024 },\n  transpilePackages:')
 dropdown='packages/views/issues/actions/issue-actions-dropdown.tsx'
 add(dropdown,'import { IssueTransferDialog } from "./issue-transfer-dialog";','import { IssueTransferDialog, IssueTransferMenuItem } from "./issue-transfer-dialog";')
 add(dropdown,'{(role === "owner" || role === "admin") && <DropdownMenuItem onClick={() => setTransferOpen(true)}>{t(($) => $.transfer.title)}</DropdownMenuItem>}','<IssueTransferMenuItem issue={issue} workspaceAdmin={role === "owner" || role === "admin"} onSelect={() => setTransferOpen(true)} />')
 for lang in ('en','zh-Hans','ja','ko','fr'):
  en={'tree':'Move all descendant sub-tasks together','forbidden':'Task creator, creator agent owner, or source workspace owner/admin required.','count':'Move {{count}} tasks.','status_required':'Choose a valid destination status for every source status.','integrity':'Some linked records have inconsistent workspace or task ownership. Contact an administrator to repair them before moving.','effects':'Active runs will be cancelled and old run tokens revoked. Source project, wakeups, labels, custom properties, pinned items, PR links and conversation bindings will be removed. External task relationships are detached. Unavailable assignees and notification recipients are removed.','private_history':'Task content and execution records are retained. Existing private chats keep their original permissions. No extra stop request is sent to external job platforms.','confirm_cleanup':'I confirm these changes and moving the selected task history to the destination workspace.'}
  zh={'tree':'连同所有下级子任务一起移动','forbidden':'仅任务创建者、创建智能体的所有者或原工作区 owner/管理员可迁移。','count':'本次移动 {{count}} 项任务。','status_required':'请为每种原状态选择有效的目标状态。','integrity':'部分关联记录的工作区或任务归属不一致，请联系管理员修复后再迁移。','effects':'将取消正在执行的运行并撤销旧令牌，清除原项目、唤醒、标签、自定义属性、固定项、PR 关联和如流绑定，解除外部父子、依赖和重复任务关系。无法沿用的负责人和通知接收者会被移除；','private_history':'任务内容和执行记录会保留，原私聊仍遵循原有权限。不额外停止外部平台上的作业。','confirm_cleanup':'我确认上述变更，并将所选任务及其历史移动到目标工作区。'}
  add(f'packages/views/locales/{lang}/issues.json','{\n  "','{\n  "transfer_expanded": '+json.dumps(zh if lang=='zh-Hans' else en,ensure_ascii=False)+',\n  "')

 add('packages/core/types/attachment.ts','  download_url: string;','  download_url: string;\n  legacy_urls?: string[];')
 add('packages/core/api/schemas.ts','  attachment_download_url: z.string().optional(),','  attachment_download_url: z.string().optional(),\n  legacy_urls: z.array(z.string()).optional(),')
 add('packages/core/attachments/image-sequence.ts','      matchesAttachmentURL(url, a.markdown_url),','      matchesAttachmentURL(url, a.markdown_url) || (Array.isArray(a.legacy_urls) && a.legacy_urls.some(alias => typeof alias === "string" && matchesAttachmentURL(url, alias))),')

 add('packages/core/api/schemas.ts','const AttachmentSchema = z.object({\n  id: z.string(),','const AttachmentSchema = z.object({\n  legacy_urls: z.array(z.string()).optional().catch([]),\n  id: z.string(),')

 add('packages/core/hooks/use-file-upload.ts','File exceeds 100 MB limit','File exceeds 50 MB limit')
 add('packages/views/issues/actions/issue-actions-dropdown.tsx','import { useT } from "../../i18n";\n','')
 add('packages/views/issues/actions/issue-actions-dropdown.tsx','  const { t } = useT("issues");\n','')
 add('packages/views/issues/actions/issue-actions-dropdown.tsx','import { DropdownMenuItem } from "@multica/ui/components/ui/dropdown-menu";\n','')

 add('packages/views/projects/components/project-resources-section.tsx',"  // Desktop-only entry points. We hide (not just disable) on web so users\n  // there don't see an action they can never complete — the spec calls for\n  // read-only on web because the daemon-id check can't be performed in the\n  // browser.",'  // Desktop retains its native folder picker; the browser selects a remote\n  // machine and absolute path. The executing daemon validates its filesystem.')

 add("packages/core/types/project.ts", 'export type ProjectResourceType = "github_repo" | "local_directory";', 'export type ProjectResourceType = "github_repo" | "local_directory" | "working_directory";')
 add("packages/views/projects/components/project-resources-section.tsx", "{resources.map((resource) => (", '{resources.filter(resource => resource.resource_type !== "working_directory").map((resource) => (')
