"use client";

import { MessageSquare } from "lucide-react";
import { useIssueInfoFlowGroups } from "@multica/core/issues/infoflow-groups";
import { buttonVariants } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n";

export function IssueInfoFlowGroups({ workspaceId, issueId }: { workspaceId: string; issueId: string }) {
  const { t } = useT("issues");
  const query = useIssueInfoFlowGroups(workspaceId, issueId);
  // Never keep a stale destination visible after an authorization/read failure.
  if (query.isError || query.data?.length !== 1) return null;
  const label = t(($) => $.infoflow_groups.open);
  const group = query.data[0]!;
  return <a href={group.url} className={buttonVariants({ variant: "outline", size: "sm" })} title={group.name || t(($) => $.infoflow_groups.group, { id: group.groupId })}>
    <MessageSquare aria-hidden="true" />{label}
  </a>;
}
