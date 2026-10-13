"use client";

import { MessageSquare, ChevronDown } from "lucide-react";
import { useIssueInfoFlowGroups } from "@multica/core/issues/infoflow-groups";
import { Button, buttonVariants } from "@multica/ui/components/ui/button";
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem } from "@multica/ui/components/ui/dropdown-menu";
import { useT } from "../../i18n";

export function IssueInfoFlowGroups({ workspaceId, issueId }: { workspaceId: string; issueId: string }) {
  const { t } = useT("issues");
  const query = useIssueInfoFlowGroups(workspaceId, issueId);
  // Never keep a stale destination visible after an authorization/read failure.
  if (query.isError || !query.data?.length) return null;
  const label = t(($) => $.infoflow_groups.open);
  const groups = query.data;
  if (groups.length === 1) {
    return <a href={groups[0]!.url} className={buttonVariants({ variant: "outline", size: "sm" })} title={groups[0]!.name || t(($) => $.infoflow_groups.group, { id: groups[0]!.groupId })}>
      <MessageSquare aria-hidden="true" />{label}
    </a>;
  }
  return <DropdownMenu>
    <DropdownMenuTrigger render={<Button variant="outline" size="sm" />}>
      <MessageSquare aria-hidden="true" />{label}<ChevronDown aria-hidden="true" />
    </DropdownMenuTrigger>
    <DropdownMenuContent align="end" className="max-w-[min(24rem,90vw)]">
      {groups.map((group) => <DropdownMenuItem key={group.groupId} render={<a href={group.url} />}>
        <span className="truncate">{group.name || t(($) => $.infoflow_groups.group, { id: group.groupId })}</span>
      </DropdownMenuItem>)}
    </DropdownMenuContent>
  </DropdownMenu>;
}
