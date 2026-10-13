"""Checked UI wiring for authenticated issue-to-InfoFlow group destinations."""
import json


def extend(changes):
    def add(path, before, after):
        changes.setdefault(path, []).append((before, after))

    add("packages/core/package.json", '    "./issues": "./issues/index.ts",',
        '    "./issues": "./issues/index.ts",\n    "./issues/infoflow-groups": "./issues/infoflow-groups.ts",')
    add("packages/core/api/client.ts", 'import { configStore } from "../config";',
        'import { configStore } from "../config";\nimport { InfoFlowGroupsSchema, infoFlowGroupURL, type InfoFlowGroup } from "./infoflow-groups-schema";')
    anchor = '  async moveIssue(id: string, data: MoveIssueRequest): Promise<Issue> {'
    add("packages/core/api/client.ts", anchor, '''  async getIssueInfoFlowGroups(id: string): Promise<InfoFlowGroup[]> {
    const result = parseWithFallback<{ groups: { group_id: string; name?: string; url: string }[] } | null>(
      await this.fetch(`/api/issues/${encodeURIComponent(id)}/infoflow-groups`),
      InfoFlowGroupsSchema, null, { endpoint: "GET /api/issues/:id/infoflow-groups" },
    );
    if (!result) throw new Error("Invalid InfoFlow group list");
    const seen = new Set<string>();
    return result.groups.filter((group) => {
      if (seen.has(group.group_id)) return false;
      seen.add(group.group_id);
      return true;
    }).map((group) => ({
      groupId: group.group_id, name: group.name,
      // Construct the one permitted app action; never navigate to an arbitrary
      // URL from a network response, even one using the infoflow scheme.
      url: infoFlowGroupURL(group.group_id),
    }));
  }

''' + anchor)
    path = "packages/views/issues/components/issue-detail.tsx"
    add(path, 'import { IssueAgentActivityIndicator } from "./issue-agent-activity-indicator";',
        'import { IssueAgentActivityIndicator } from "./issue-agent-activity-indicator";\nimport { IssueInfoFlowGroups } from "./issue-infoflow-groups";')
    add(path, '            <IssueWakeupHeaderChip issueId={id} onOpen={openWakeups} />',
        '            <IssueWakeupHeaderChip issueId={id} onOpen={openWakeups} />\n            <IssueInfoFlowGroups workspaceId={issue.workspace_id} issueId={issue.id} />')
    labels = {
        "en": {"open": "Open InfoFlow group", "group": "InfoFlow group {{id}}"},
        "zh-Hans": {"open": "打开如流群聊", "group": "如流群 {{id}}"},
    }
    for locale, copy in labels.items():
        add(f"packages/views/locales/{locale}/issues.json", '  "inline_run":',
            '  "infoflow_groups": ' + json.dumps(copy, ensure_ascii=False) + ',\n  "inline_run":')
