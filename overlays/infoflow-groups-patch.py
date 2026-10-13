"""Checked UI wiring for authenticated issue-to-InfoFlow group destinations."""
import json


def extend(changes):
    def add(path, before, after):
        changes.setdefault(path, []).append((before, after))

    add("packages/core/package.json", '    "./issues": "./issues/index.ts",',
        '    "./issues": "./issues/index.ts",\n    "./issues/infoflow-groups": "./issues/infoflow-groups.ts",\n    "./issues/infoflow-group-binding": "./issues/infoflow-group-binding.ts",')
    add("packages/core/api/client.ts", 'import { configStore } from "../config";',
        'import { configStore } from "../config";\nimport { InfoFlowGroupsSchema, InfoFlowGroupBindingSchema, InfoFlowGroupTargetSchema, infoFlowGroupURL, infoFlowGroupBindingStatus, type InfoFlowGroup, type InfoFlowGroupBindingWire, type InfoFlowGroupBindingStatus, type InfoFlowGroupTarget } from "./infoflow-groups-schema";')
    anchor = '  async moveIssue(id: string, data: MoveIssueRequest): Promise<Issue> {'
    add("packages/core/api/client.ts", anchor, '''  async getIssueInfoFlowGroups(id: string): Promise<InfoFlowGroup[]> {
    const result = parseWithFallback<{ groups: { group_id: string; name?: string; url: string }[] } | null>(
      await this.fetch(`/api/issues/${encodeURIComponent(id)}/infoflow-groups`),
      InfoFlowGroupsSchema, null, { endpoint: "GET /api/issues/:id/infoflow-groups" },
    );
    if (!result) throw new Error("Invalid InfoFlow group list");
    return result.groups.map((group) => ({
      groupId: group.group_id, name: group.name,
      // Construct the one permitted app action; never navigate to an arbitrary
      // URL from a network response, even one using the infoflow scheme.
      url: infoFlowGroupURL(group.group_id),
    }));
  }

  async getIssueInfoFlowGroupBinding(id: string): Promise<InfoFlowGroupBindingStatus> {
    const result = parseWithFallback<InfoFlowGroupBindingWire | null>(
      await this.fetch(`/api/issues/${encodeURIComponent(id)}/infoflow-group-binding`),
      InfoFlowGroupBindingSchema, null, { endpoint: "GET /api/issues/:id/infoflow-group-binding" },
    );
    if (!result) throw new Error("Invalid InfoFlow group binding status");
    return infoFlowGroupBindingStatus(result);
  }

  async bindIssueInfoFlowGroup(id: string, target: InfoFlowGroupTarget): Promise<InfoFlowGroupBindingStatus> {
    const input = InfoFlowGroupTargetSchema.safeParse(target);
    if (!input.success) throw new Error("Invalid InfoFlow group target");
    const result = parseWithFallback<InfoFlowGroupBindingWire | null>(
      await this.fetch(`/api/issues/${encodeURIComponent(id)}/infoflow-group-binding`, {
        method: "POST", body: JSON.stringify(input.data),
      }), InfoFlowGroupBindingSchema, null, { endpoint: "POST /api/issues/:id/infoflow-group-binding" },
    );
    if (!result) throw new Error("Invalid InfoFlow group binding status");
    return infoFlowGroupBindingStatus(result);
  }

''' + anchor)
    path = "packages/views/issues/components/issue-detail.tsx"
    add(path, 'import { IssueAgentActivityIndicator } from "./issue-agent-activity-indicator";',
        'import { IssueAgentActivityIndicator } from "./issue-agent-activity-indicator";\nimport { IssueInfoFlowGroupBinding } from "./issue-infoflow-group-binding";')
    add(path, '            <IssueWakeupHeaderChip issueId={id} onOpen={openWakeups} />',
        '            <IssueWakeupHeaderChip issueId={id} onOpen={openWakeups} />\n            <IssueInfoFlowGroupBinding key={`${issue.workspace_id}:${issue.id}`} workspaceId={issue.workspace_id} issueId={issue.id} />')
    add("packages/views/issues/components/issue-detail.test.tsx", '  getIssue: vi.fn(),',
        '  getIssue: vi.fn(),\n'
        '  getIssueInfoFlowGroupBinding: vi.fn().mockResolvedValue({ enabled: false, canManage: false, binding: null, request: null, groups: [] }),\n'
        '  getIssueInfoFlowGroups: vi.fn().mockResolvedValue([]),\n'
        '  bindIssueInfoFlowGroup: vi.fn(),')
    labels = {
        "en": {"open": "Open InfoFlow group", "group": "InfoFlow group {{id}}",
            "bind": "Bind InfoFlow group", "existing": "Choose an existing group", "manual": "Enter a group number",
            "choose": "Select a group", "empty": "No verified groups yet. Enter a group number instead.",
            "placeholder": "Enter a group number",
            "warning": "Everyone in the group can read task replies. Missing robots will be invited; a group administrator may need to approve them. If no platform robot is in the group, add one manually first.",
            "submit": "Submit binding", "submitting": "Submitting binding...", "retry": "Retry binding", "close": "Close", "refresh": "Refresh status",
            "invalid": "Enter a positive group number, up to 9223372036854775807. Invite links are not supported.",
            "waiting": "Waiting for confirmation", "approval": "Waiting for administrator approval", "success": "Group bound",
            "failed": "Unable to bind the group", "read_failed": "Unable to check binding permissions. Refresh before retrying.",
            "target": "Group number", "reserved": "This task already reserves this group. Bind another group only after unbinding it in InfoFlow.",
            "membership": "Unable to confirm membership. Ensure you and a platform robot are in the group, then refresh.",
            "actor": "You are not in the group. Join it before retrying.", "inviter": "Add a platform robot to the group before retrying.",
            "leader": "Configure the task leader or assigned agent's robot, then retry.", "unavailable": "The required robot is unavailable. Check its connection before retrying.",
            "invitation_failed": "The robot invitation failed. Check group permissions or add the robot manually, then retry."},
        "zh-Hans": {"open": "打开如流群聊", "group": "如流群 {{id}}",
            "bind": "绑定如流群聊", "existing": "选择已有群", "manual": "输入群号",
            "choose": "选择一个群", "empty": "暂无已确认的群，可输入群号。",
            "placeholder": "输入群号",
            "warning": "群内所有成员都能看到任务回复。缺少的机器人会自动受邀，可能需要群管理员审批。如果群内尚无平台机器人，请先手动拉入一个。",
            "submit": "提交绑定", "submitting": "正在提交绑定...", "retry": "重试绑定", "close": "关闭", "refresh": "刷新状态",
            "invalid": "请输入正整数群号，最大为 9223372036854775807，暂不支持邀请链接。",
            "waiting": "等待确认", "approval": "等待管理员审批", "success": "绑定成功",
            "failed": "无法绑定群聊", "read_failed": "无法确认绑定权限，请刷新后重试。",
            "target": "群号", "reserved": "此任务已关联或预留这个群。如需更换，请先在如流中解绑。",
            "membership": "无法确认群成员，请确认你和平台机器人已入群，再刷新状态。",
            "actor": "你不在群内，请先入群再重试。", "inviter": "请先将一个平台机器人拉进群，再重试。",
            "leader": "请配置队长或任务负责智能体的机器人，再重试。", "unavailable": "所需机器人暂不可用，请检查连接后重试。",
            "invitation_failed": "邀请机器人失败，请检查群权限或手动拉入机器人，再重试。"},
    }
    for locale, copy in labels.items():
        add(f"packages/views/locales/{locale}/issues.json", '  "inline_run":',
            '  "infoflow_groups": ' + json.dumps(copy, ensure_ascii=False) + ',\n  "inline_run":')
