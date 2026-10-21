"use client";

import { useState, type FormEvent } from "react";
import { ChevronRight, Pin, Plus, X } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { useWorkspaceSlug } from "@multica/core/paths";
import type { Issue } from "@multica/core/types";
import { AppLink } from "../../navigation";
import { useT } from "../../i18n";
import {
  frameableUrl,
  pageViewPath,
  pagesFromMetadata,
  useDashboard,
  usePageBinding,
} from "../../layout/custom-pages";

/**
 * Pages bound to this issue (issue metadata `page.*`). Each opens inside
 * Multica, can be pinned to the viewer's own dashboard, or be unbound.
 */
export function IssuePagesSection({ issue }: { issue: Pick<Issue, "id" | "workspace_id" | "metadata"> }) {
  const { t } = useT("layout");
  const slug = useWorkspaceSlug();
  const { cards, add, removeUrl } = useDashboard(slug);
  const { bind, unbind } = usePageBinding(issue);
  const pages = pagesFromMetadata(issue.metadata as Record<string, unknown> | undefined);
  const [open, setOpen] = useState(true);
  const [adding, setAdding] = useState(false);
  const [url, setUrl] = useState("");
  const [error, setError] = useState<string | null>(null);
  if (!slug) return null;

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (!frameableUrl(url)) {
      setError(t(($) => $.sidebar.custom_apps.invalid_url));
      return;
    }
    setError(null);
    bind.mutate(url, {
      onSuccess: () => { setUrl(""); setAdding(false); },
      onError: () => setError(t(($) => $.sidebar.custom_apps.bind_failed)),
    });
  };

  return (
    <div>
      <div className="mb-2 flex w-full items-center gap-0.5">
        <button
          type="button"
          aria-expanded={open}
          className={`flex min-w-0 flex-1 items-center gap-1 rounded-md px-2 py-1 text-caption font-medium transition-colors hover:bg-accent/70 ${open ? "" : "text-muted-foreground hover:text-foreground"}`}
          onClick={() => setOpen(!open)}
        >
          <span className="truncate">{t(($) => $.sidebar.custom_apps.section_title)}</span>
          {pages.length > 0 ? (
            <span className="shrink-0 rounded-xs bg-muted px-1 text-micro font-medium tabular-nums text-muted-foreground">{pages.length}</span>
          ) : null}
          <ChevronRight className={`!size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform ${open ? "rotate-90" : ""}`} />
        </button>
        <Button type="button" size="icon-xs" variant="ghost" aria-label={t(($) => $.sidebar.custom_apps.bind)}
          title={t(($) => $.sidebar.custom_apps.bind)} onClick={() => { setOpen(true); setAdding((v) => !v); }}>
          <Plus />
        </Button>
      </div>
      {open ? (
        <div className="flex flex-col gap-1 pl-2">
          {adding ? (
            <form onSubmit={submit} className="flex flex-col gap-1.5">
              <div className="flex items-center gap-1">
                <Input type="text" inputMode="url" autoComplete="off" spellCheck={false} required autoFocus value={url}
                  placeholder={t(($) => $.sidebar.custom_apps.url_placeholder)}
                  aria-label={t(($) => $.sidebar.custom_apps.bind_placeholder)} aria-invalid={error ? true : undefined}
                  onChange={(event) => setUrl(event.target.value)} className="h-7 text-caption" />
                <Button type="submit" size="xs" disabled={bind.isPending}>{t(($) => $.sidebar.custom_apps.bind)}</Button>
              </div>
              {error ? <p role="alert" className="text-caption text-destructive">{error}</p> : null}
            </form>
          ) : null}
          {pages.length === 0 && !adding ? (
            <p className="px-2 text-caption text-muted-foreground">{t(($) => $.sidebar.custom_apps.section_empty)}</p>
          ) : null}
          <ul className="flex flex-col gap-0.5">
            {pages.map((page) => {
              const pinned = cards.some((card) => card.url === page.url);
              // Toggle: a filled, highlighted pin means the page is on my dashboard.
              const pinLabel = pinned
                ? t(($) => $.sidebar.custom_apps.remove_from_dashboard)
                : t(($) => $.sidebar.custom_apps.add_to_dashboard);
              return (
                <li key={page.key} className="group/page flex items-center gap-1 rounded-md px-2 py-0.5 hover:bg-accent/50">
                  <AppLink href={pageViewPath(slug, page.url)} className="min-w-0 flex-1 truncate text-caption hover:underline" title={page.url}>
                    {page.title}
                  </AppLink>
                  <Button type="button" size="icon-xs" variant="ghost" aria-pressed={pinned}
                    aria-label={`${pinLabel} ${page.title}`} title={pinLabel}
                    className={pinned ? "text-primary" : "text-muted-foreground"}
                    onClick={() => (pinned ? removeUrl(page.url) : add(page.url, undefined, "issue"))}>
                    <Pin className={pinned ? "fill-current" : undefined} />
                  </Button>
                  <Button type="button" size="icon-xs" variant="ghost" disabled={unbind.isPending}
                    aria-label={`${t(($) => $.sidebar.custom_apps.unbind)} ${page.title}`} title={t(($) => $.sidebar.custom_apps.unbind)}
                    onClick={() => unbind.mutate(page.key)}>
                    <X />
                  </Button>
                </li>
              );
            })}
          </ul>
        </div>
      ) : null}
    </div>
  );
}
