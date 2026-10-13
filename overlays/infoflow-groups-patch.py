"""Checked UI wiring for authenticated issue-to-InfoFlow conversations."""
import json


def extend(changes):
    def add(path, before, after):
        changes.setdefault(path, []).append((before, after))

    add("packages/core/package.json", '    "./issues": "./issues/index.ts",',
        '    "./issues": "./issues/index.ts",\n    "./issues/infoflow-groups": "./issues/infoflow-groups.ts",\n    "./issues/infoflow-group-binding": "./issues/infoflow-group-binding.ts",\n    "./issues/infoflow-conversations": "./issues/infoflow-conversations.ts",')
    add("packages/core/api/client.ts", 'import { configStore } from "../config";',
        'import { configStore } from "../config";\nimport { InfoFlowGroupsSchema, InfoFlowGroupBindingSchema, InfoFlowGroupTargetSchema, infoFlowGroupURL, infoFlowGroupBindingStatus, type InfoFlowGroup, type InfoFlowGroupBindingWire, type InfoFlowGroupBindingStatus, type InfoFlowGroupTarget } from "./infoflow-groups-schema";\nimport { InfoFlowConversationsSchema, InfoFlowConversationTargetSchema, InfoFlowConversationBindingIdSchema, infoFlowConversationsStatus, type InfoFlowConversationsWire, type InfoFlowConversationsStatus, type InfoFlowConversationTarget } from "./infoflow-conversations-schema";')
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

  async getIssueInfoFlowConversations(id: string): Promise<InfoFlowConversationsStatus> {
    const result = parseWithFallback<InfoFlowConversationsWire | null>(
      await this.fetch(`/api/issues/${encodeURIComponent(id)}/infoflow-conversations`),
      InfoFlowConversationsSchema, null, { endpoint: "GET /api/issues/:id/infoflow-conversations" },
    );
    if (!result) throw new Error("Invalid InfoFlow conversation status");
    return infoFlowConversationsStatus(result);
  }

  async bindIssueInfoFlowConversation(id: string, target: InfoFlowConversationTarget): Promise<InfoFlowConversationsStatus> {
    const input = InfoFlowConversationTargetSchema.safeParse(target);
    if (!input.success) throw new Error("Invalid InfoFlow conversation target");
    const result = parseWithFallback<InfoFlowConversationsWire | null>(
      await this.fetch(`/api/issues/${encodeURIComponent(id)}/infoflow-conversations`, {
        method: "POST", body: JSON.stringify(input.data),
      }), InfoFlowConversationsSchema, null, { endpoint: "POST /api/issues/:id/infoflow-conversations" },
    );
    if (!result) throw new Error("Invalid InfoFlow conversation status");
    return infoFlowConversationsStatus(result);
  }

  async unbindIssueInfoFlowConversation(id: string, bindingId: string): Promise<InfoFlowConversationsStatus> {
    const input = InfoFlowConversationBindingIdSchema.safeParse(bindingId);
    if (!input.success) throw new Error("Invalid InfoFlow conversation binding ID");
    const result = parseWithFallback<InfoFlowConversationsWire | null>(
      await this.fetch(`/api/issues/${encodeURIComponent(id)}/infoflow-conversations/${encodeURIComponent(input.data)}`, {
        method: "DELETE",
      }), InfoFlowConversationsSchema, null, { endpoint: "DELETE /api/issues/:id/infoflow-conversations/:bindingId" },
    );
    if (!result) throw new Error("Invalid InfoFlow conversation status");
    return infoFlowConversationsStatus(result);
  }

''' + anchor)
    path = "packages/views/issues/components/issue-detail.tsx"
    add(path, 'import { IssueAgentActivityIndicator } from "./issue-agent-activity-indicator";',
        'import { IssueAgentActivityIndicator } from "./issue-agent-activity-indicator";\nimport { BindInfoFlowConversation } from "./bind-infoflow-conversation";')
    add(path, '            <IssueWakeupHeaderChip issueId={id} onOpen={openWakeups} />',
        '            <IssueWakeupHeaderChip issueId={id} onOpen={openWakeups} />\n            <BindInfoFlowConversation key={`${issue.workspace_id}:${issue.id}`} workspaceId={issue.workspace_id} issueId={issue.id} />')
    add("packages/views/issues/components/issue-detail.test.tsx", '  getIssue: vi.fn(),',
        '  getIssue: vi.fn(),\n'
        '  getIssueInfoFlowGroupBinding: vi.fn().mockResolvedValue({ enabled: false, canManage: false, binding: null, request: null, groups: [] }),\n'
        '  getIssueInfoFlowGroups: vi.fn().mockResolvedValue([]),\n'
        '  bindIssueInfoFlowGroup: vi.fn(),\n'
        '  getIssueInfoFlowConversations: vi.fn().mockResolvedValue({ enabled: false, group: { canManage: false, binding: null, request: null, groups: [] }, private: { eligible: false, canManage: false, reason: "", bindings: [], request: null } }),\n'
        '  bindIssueInfoFlowConversation: vi.fn(),\n'
        '  unbindIssueInfoFlowConversation: vi.fn(),')
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
    conversation_labels = {
        "en": {"bind": "Bind InfoFlow conversation", "description": "Group and your private conversation can both be linked to this task.",
            "kind": "Conversation type", "group": "Group", "private": "My private conversation", "my_private": "Me and the assigned agent's robot",
            "private_routing": "A private conversation can link to several tasks. Reply to a task message or include its number when the target is ambiguous.",
            "private_restriction": "Private binding requires a single assigned agent. Squad, human-assigned and unassigned tasks cannot bind privately.",
            "private_missing": "Configure one unique robot for the assigned agent, then retry.",
            "private_unavailable": "The assigned agent's robot is unavailable. Check its connection, then retry.",
            "private_disabled": "Enable private chat for the assigned agent's robot, then retry.",
            "private_metadata": "Unable to verify the robot's private conversation. Refresh or retry.",
            "private_waiting": "Verifying the assigned agent's robot", "private_success": "Private conversation bound", "open_private": "Open InfoFlow private conversation",
            "needs_rebind": "Rebinding required", "reassignment": "The task's assignee changed. Bind explicitly to the current assigned agent's robot.",
            "group_pending": "Rechecking the current task leader's group membership", "reserved": "This task reserves this group. Remove the binding before choosing a different group.",
            "rebind": "Rebind", "unbind": "Remove binding", "unbind_group": "Remove group binding", "unbind_private": "Remove private binding",
            "no_manage": "You cannot manage this conversation's binding.", "failed": "Unable to update the conversation binding"},
        "zh-Hans": {"bind": "绑定如流对话", "description": "任务可同时关联群聊和你自己的私聊。",
            "kind": "对话类型", "group": "群聊", "private": "我的私聊", "my_private": "我与任务负责智能体的机器人",
            "private_routing": "同一私聊可关联多个任务。出现多个候选时，请引用任务消息或注明任务编号。",
            "private_restriction": "只有负责人是单个智能体的任务可绑定私聊，小队、人工负责或未分配的任务不支持。",
            "private_missing": "请为任务负责智能体配置唯一的机器人，再重试。",
            "private_unavailable": "任务负责智能体的机器人暂不可用，请检查连接后重试。",
            "private_disabled": "请为任务负责智能体的机器人开启私聊，再重试。",
            "private_metadata": "无法确认机器人的私聊信息，请刷新或重试。",
            "private_waiting": "正在确认任务负责智能体的机器人", "private_success": "私聊绑定成功", "open_private": "打开如流私聊",
            "needs_rebind": "需要重新绑定", "reassignment": "任务负责人已变化，请重新绑定当前负责智能体的机器人。",
            "group_pending": "正在重新确认当前队长或负责智能体的入群状态", "reserved": "此任务已预留这个群。如需更换，请先解除绑定。",
            "rebind": "重新绑定", "unbind": "解除绑定", "unbind_group": "解除群聊绑定", "unbind_private": "解除私聊绑定",
            "no_manage": "你没有管理此对话绑定的权限。", "failed": "无法更新对话绑定"},
    }
    for locale, copy in conversation_labels.items():
        add(f"packages/views/locales/{locale}/issues.json", '  "inline_run":',
            '  "infoflow_conversations": ' + json.dumps(copy, ensure_ascii=False) + ',\n  "inline_run":')
