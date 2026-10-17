"use client";

import { useState } from "react";
import { ExternalLink, Pin } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { useWorkspaceSlug } from "@multica/core/paths";
import { useT } from "../i18n";
import { PageHeader } from "./page-header";
import { LinkedIssues } from "./linked-issues";
import { PageFrame } from "./page-frame";
import { MAX_DASHBOARD_CARDS, frameableUrl, pageTitle, useDashboard, useLinkedIssues, usePopos } from "./custom-pages";

/** One bound page, full size, with the issues it is bound to above it. */
export function CustomPageView({ url }: { url: string }) {
  const { t } = useT("layout");
  const slug = useWorkspaceSlug();
  const { cards, add, removeUrl } = useDashboard(slug);
  const { pages } = usePopos(slug);
  const [notice, setNotice] = useState<string | null>(null);
  const src = frameableUrl(url);
  const { data: linked } = useLinkedIssues(src);

  if (!src || !slug) {
    return <p role="status" className="p-6 text-sm text-muted-foreground">{t(($) => $.sidebar.custom_apps.not_frameable)}</p>;
  }
  const pinned = cards.some((card) => card.url === src);
  const pin = () => {
    if (pinned) {
      removeUrl(src);
      setNotice(null);
      return;
    }
    // Source for the dashboard filter: a POPOs page, else an issue-bound one.
    const poposPage = pages.find((page) => page.url === src);
    const result = add(src, poposPage?.title, poposPage ? "popos" : linked?.length ? "issue" : "manual");
    setNotice(result === "full" ? t(($) => $.sidebar.custom_apps.full, { count: MAX_DASHBOARD_CARDS }) : null);
  };

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader>
        <h1 className="min-w-0 flex-1 truncate text-sm font-medium" title={src}>{pageTitle(src)}</h1>
        {notice ? <span role="alert" className="text-caption text-destructive">{notice}</span> : null}
        <Button type="button" size="xs" variant="outline" aria-pressed={pinned} onClick={pin}
          title={pinned ? t(($) => $.sidebar.custom_apps.remove_from_dashboard) : undefined}
          className={pinned ? "text-primary" : undefined}>
          <Pin className={pinned ? "fill-current" : undefined} />
          {pinned ? t(($) => $.sidebar.custom_apps.added_to_dashboard) : t(($) => $.sidebar.custom_apps.add_to_dashboard)}
        </Button>
        <a href={src} target="_blank" rel="noopener noreferrer"
          className="inline-flex shrink-0 items-center gap-1 text-caption text-muted-foreground hover:text-foreground">
          <ExternalLink aria-hidden="true" className="size-3.5" />
          <span>{t(($) => $.sidebar.custom_apps.open_in_new_tab)}</span>
        </a>
      </PageHeader>
      <div className="flex shrink-0 items-center gap-2 border-b px-4 py-1.5">
        <span className="shrink-0 text-caption text-muted-foreground">{t(($) => $.sidebar.custom_apps.linked_issues)}</span>
        <LinkedIssues url={src} workspaceSlug={slug} className="min-w-0" />
      </div>
      <PageFrame key={src} src={src} title={pageTitle(src)} className="min-h-0 w-full flex-1" />
    </div>
  );
}
