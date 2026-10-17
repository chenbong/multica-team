"use client";

import { paths } from "@multica/core/paths";
import { AppLink } from "../navigation";
import { useT } from "../i18n";
import { useLinkedIssues } from "./custom-pages";

/** The issues bound to one page; each one links back to its issue. */
export function LinkedIssues({ url, workspaceSlug, className }: { url: string; workspaceSlug: string; className?: string }) {
  const { t } = useT("layout");
  const { data: issues, isLoading, isError } = useLinkedIssues(url);
  if (isLoading) return <p className={`text-caption text-muted-foreground ${className ?? ""}`}>{t(($) => $.sidebar.custom_apps.loading)}</p>;
  if (isError) return <p className={`text-caption text-muted-foreground ${className ?? ""}`}>{t(($) => $.sidebar.custom_apps.linked_issues_failed)}</p>;
  if (!issues?.length) {
    return <p className={`text-caption text-muted-foreground ${className ?? ""}`}>{t(($) => $.sidebar.custom_apps.no_linked_issues)}</p>;
  }
  return (
    <ul aria-label={t(($) => $.sidebar.custom_apps.linked_issues)} className={`flex flex-wrap gap-1.5 ${className ?? ""}`}>
      {issues.map((issue) => (
        <li key={issue.id}>
          <AppLink
            href={paths.workspace(workspaceSlug).issueDetail(issue.id)}
            className="inline-flex max-w-64 items-center gap-1 rounded-md border px-1.5 py-0.5 text-caption hover:bg-accent"
            title={issue.title}
          >
            <span className="shrink-0 text-muted-foreground">{issue.identifier}</span>
            <span className="truncate">{issue.title}</span>
          </AppLink>
        </li>
      ))}
    </ul>
  );
}
