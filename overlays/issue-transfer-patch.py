"""Checked frontend wiring for the basic cross-workspace transfer flow."""
import json


def extend(changes):
    def add(path, before, after):
        changes.setdefault(path, []).append((before, after))

    add("packages/core/package.json", '    "./issues": "./issues/index.ts",',
        '    "./issues": "./issues/index.ts",\n    "./issues/transfer": "./issues/transfer.ts",')

    add("packages/core/api/client.ts", 'import { configStore } from "../config";',
        'import { configStore } from "../config";\nimport { IssueTransferPlanSchema, type IssueTransferPlan, type IssueTransferRequest } from "./issue-transfer-schema";')
    anchor = '  async moveIssue(id: string, data: MoveIssueRequest): Promise<Issue> {'
    add("packages/core/api/client.ts", anchor, '''  async previewIssueTransfer(id: string, data: IssueTransferRequest): Promise<IssueTransferPlan> {
    const result = parseWithFallback<IssueTransferPlan | null>(
      await this.fetch(`/api/issues/${id}/transfer-preview`, { method: "POST", body: JSON.stringify(data) }),
      IssueTransferPlanSchema, null, { endpoint: "POST /api/issues/:id/transfer-preview" },
    );
    if (!result || result.issue_id !== id || result.target_workspace_id !== data.target_workspace_id) throw new Error("Invalid transfer preview");
    return result;
  }

  async transferIssue(id: string, data: IssueTransferRequest): Promise<IssueTransferPlan> {
    const result = parseWithFallback<IssueTransferPlan | null>(
      await this.fetch(`/api/issues/${id}/transfer`, { method: "POST", body: JSON.stringify(data) }),
      IssueTransferPlanSchema, null, { endpoint: "POST /api/issues/:id/transfer" },
    );
    if (!result || !result.identifier || result.issue_id !== id || result.target_workspace_id !== data.target_workspace_id) throw new Error("Invalid transfer result");
    return result;
  }

''' + anchor)
    path = "packages/views/issues/actions/issue-actions-dropdown.tsx"
    add(path, 'import { AssigneePicker } from "../components/pickers";',
        'import { AssigneePicker } from "../components/pickers";\nimport { DropdownMenuItem } from "@multica/ui/components/ui/dropdown-menu";\nimport { useCurrentMember } from "@multica/core/permissions";\nimport { useT } from "../../i18n";\nimport { IssueTransferDialog } from "./issue-transfer-dialog";')
    add(path, '  const [assigneeOpen, setAssigneeOpen] = useState(false);',
        '  const [assigneeOpen, setAssigneeOpen] = useState(false);\n  const [transferOpen, setTransferOpen] = useState(false);\n  const { role } = useCurrentMember(issue.workspace_id);\n  const { t } = useT("issues");')
    add(path, '        <DropdownMenuContent align={align} className="w-auto">',
        '        <DropdownMenuContent align={align} className="w-auto">\n          {(role === "owner" || role === "admin") && <DropdownMenuItem onClick={() => setTransferOpen(true)}>{t(($) => $.transfer.title)}</DropdownMenuItem>}')
    add(path, '      {/* Mount the picker only once',
        '      {transferOpen && <IssueTransferDialog issue={issue} onClose={() => setTransferOpen(false)} />}\n      {/* Mount the picker only once')
    add("packages/core/types/events.ts", '  | "issue:updated"', '  | "issue:updated"\n  | "issue:workspace_changed"')
    add("packages/core/types/events.ts", '  "issue:updated": IssueUpdatedPayload;',
        '  "issue:updated": IssueUpdatedPayload;\n  "issue:workspace_changed": { issue_id: string };')
    path = "packages/core/realtime/use-realtime-sync.ts"
    add(path, '      "issue:updated", "issue:created",', '      "issue:workspace_changed", "issue:updated", "issue:created",')
    add(path, '    const unsubIssueDeleted = ws.on("issue:deleted", (p) => {',
        '''    const unsubIssueTransferred = ws.on("issue:workspace_changed", () => {
      for (const key of ["issues", "inbox", "workspaces", "issue-transfer"]) {
        void qc.invalidateQueries({ queryKey: [key] });
      }
    });

    const unsubIssueDeleted = ws.on("issue:deleted", (p) => {''')
    add(path, '      unsubIssueDeleted();', '      unsubIssueDeleted();\n      unsubIssueTransferred();')
    add("packages/views/issues/components/issue-detail.tsx", '    case "created":\n      return t(($) => $.activity.created);',
        '    case "workspace_changed":\n      return t(($) => $.transfer.activity, { from: details.old_identifier, to: details.new_identifier });\n    case "created":\n      return t(($) => $.activity.created);')
    labels = {
        "en": {
            "title": "Move to workspace", "warning": "The destination members will be able to read this issue and its history. A new issue number will be assigned; the old link will no longer work. No agent run will be started.",
            "target": "Destination workspace", "choose": "Choose a workspace", "checking": "Checking migration requirements...", "status": "Destination status", "choose_status": "Choose a matching status", "summary": "Keep {{comments}} comments, {{runs}} runs and {{messages}} messages. New number prefix: {{prefix}}.",
            "unassign": "Remove the current assignee, who is not available in the destination workspace.", "subscriptions": "Subscriptions unavailable in the destination will be disabled: {{count}}.",
            "confirm": "Confirm move", "cancel": "Cancel", "refresh": "Refresh preview", "failed": "Could not load the migration. Please retry.", "invalid_result": "The result could not be verified. Check the destination before retrying.",
            "activity": "Moved workspace: {{from}} → {{to}}",
            "block_status": "Choose a destination status in the same lifecycle category.", "block_project": "Detach the project, parent or duplicate relationship first.", "block_context": "Custom properties, context references or triage state are not supported yet.", "block_runs": "There are unfinished runs or runs linked to chats/automation. Move after these are resolved.", "block_children": "Child issues or dependencies are not supported in the basic version.", "block_attachments": "Issues with attachments are not supported in the basic version.", "block_automation": "Automation, wakeups, source contexts or supplements must be handled first.", "block_labels": "Labels, pins and pull-request links must be handled first.", "block_recipients": "Some notification recipients are not members of the destination workspace.", "block_unknown": "An unsupported relationship prevents this move."
        },
        "zh-Hans": {
            "title": "移动到工作区", "warning": "目标工作区成员将能查看此任务及其历史。迁移后会生成新编号，旧链接将失效；不会启动智能体执行。",
            "target": "目标工作区", "choose": "选择工作区", "checking": "正在检查迁移条件...", "status": "目标状态", "choose_status": "选择同类状态", "summary": "保留 {{comments}} 条评论、{{runs}} 次运行和 {{messages}} 条运行消息。新编号前缀：{{prefix}}。",
            "unassign": "确认解除当前负责人绑定，该负责人不属于目标工作区。", "subscriptions": "将停用 {{count}} 个不适用于目标工作区的订阅。",
            "confirm": "确认迁移", "cancel": "取消", "refresh": "刷新预览", "failed": "无法获取迁移信息，请重试。", "invalid_result": "无法验证迁移结果，请先检查目标工作区再重试。", "activity": "迁移工作区：{{from}} → {{to}}",
            "block_status": "请先选择相同生命周期类别的目标状态。", "block_project": "请先解除项目、父任务或重复任务关系。", "block_context": "暂不支持带自定义属性、上下文引用或分诊状态的任务。", "block_runs": "存在未结束的运行，或运行关联了聊天、自动化，暂不能迁移。", "block_children": "基础版暂不支持带子任务或依赖关系的任务。", "block_attachments": "基础版暂不支持带附件的任务。", "block_automation": "请先处理自动化、唤醒规则、来源上下文或补充消息关联。", "block_labels": "请先处理标签、固定项或 PR 关联。", "block_recipients": "部分通知接收者不属于目标工作区，暂不能迁移。", "block_unknown": "存在尚未支持的关联，无法迁移。"
        },
    }
    for locale, copy in labels.items():
        add(f"packages/views/locales/{locale}/issues.json", '{\n  "inline_run":', '{\n  "transfer": '+json.dumps(copy, ensure_ascii=False)+',\n  "inline_run":')
