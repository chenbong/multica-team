"use client";

import { useRef, useState, type KeyboardEvent, type PointerEvent as ReactPointerEvent } from "react";
import { ArrowLeft, ArrowRight, ExternalLink, Link2, Maximize2, Plus, RotateCw, X } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { useWorkspaceSlug } from "@multica/core/paths";
import { AppLink, useNavigation } from "../navigation";
import { useT } from "../i18n";
import { PageHeader } from "./page-header";
import { AddPageDialog } from "./add-page-dialog";
import { LinkedIssues } from "./linked-issues";
import { PageFrame } from "./page-frame";
import {
  CARD_SOURCES,
  MAX_DASHBOARD_CARDS,
  clampCardSize,
  dashboardFilterPath,
  filterCards,
  parseDashboardFilter,
  useCardsLinkedIssues,
  type DashboardFilter,
  frameableUrl,
  pageViewPath,
  useDashboard,
  type DashboardCard,
} from "./custom-pages";

const GAP_PX = 12;
// Arrow keys on the corner handle: width in % steps, height in px steps.
const KEY_DELTA: Record<string, [number, number]> = {
  ArrowLeft: [-5, 0], ArrowRight: [5, 0], ArrowUp: [0, -40], ArrowDown: [0, 40],
};
type Axis = "x" | "y" | "xy";
const AXIS_CURSOR: Record<Axis, string> = { x: "cursor-ew-resize", y: "cursor-ns-resize", xy: "cursor-nwse-resize" };

function IconButton({ label, onClick, disabled, pressed, children }: {
  label: string; onClick?: () => void; disabled?: boolean; pressed?: boolean; children: React.ReactNode;
}) {
  return (
    <Button type="button" size="icon-xs" variant="ghost" aria-label={label} title={label}
      aria-pressed={pressed} disabled={disabled} onClick={onClick}>
      {children}
    </Button>
  );
}

function Card({ card, index, total, slug, reorderable }: {
  card: DashboardCard; index: number; total: number; slug: string; reorderable: boolean;
}) {
  const { t } = useT("layout");
  const { update, remove, move } = useDashboard(slug);
  const [reloads, setReloads] = useState(0);
  const [showIssues, setShowIssues] = useState(false);
  // Size while dragging; committed to the dashboard on release.
  const [draft, setDraft] = useState<{ width: number; height: number } | null>(null);
  const [dragAxis, setDragAxis] = useState<Axis | null>(null);
  const sectionRef = useRef<HTMLElement>(null);
  const src = frameableUrl(card.url);
  const size = draft ?? { width: card.width, height: card.height };

  const startResize = (axis: Axis) => (event: ReactPointerEvent<HTMLElement>) => {
    const section = sectionRef.current;
    const row = section?.parentElement;
    if (!section || !row || event.button !== 0) return;
    event.preventDefault();
    const handle = event.currentTarget;
    handle.setPointerCapture(event.pointerId);
    const origin = { x: event.clientX, y: event.clientY, width: section.getBoundingClientRect().width, height: size.height };
    const rowWidth = row.clientWidth || 1;
    let latest = size;
    const onMove = (move: PointerEvent) => {
      const widthPx = axis === "y" ? origin.width : origin.width + move.clientX - origin.x;
      latest = clampCardSize(
        ((widthPx + GAP_PX) / rowWidth) * 100,
        axis === "x" ? origin.height : origin.height + move.clientY - origin.y,
      );
      if (axis === "y") latest = { ...latest, width: size.width };
      setDraft(latest);
    };
    const onUp = () => {
      handle.removeEventListener("pointermove", onMove);
      handle.removeEventListener("pointerup", onUp);
      handle.removeEventListener("pointercancel", onUp);
      setDragAxis(null);
      setDraft(null);
      update(card.id, latest);
    };
    handle.addEventListener("pointermove", onMove);
    handle.addEventListener("pointerup", onUp);
    handle.addEventListener("pointercancel", onUp);
    setDragAxis(axis);
  };

  const onResizeKey = (event: KeyboardEvent) => {
    const delta = KEY_DELTA[event.key];
    if (!delta) return;
    event.preventDefault();
    update(card.id, { width: card.width + delta[0], height: card.height + delta[1] });
  };

  const resizeLabel = t(($) => $.sidebar.custom_apps.resize);
  return (
    <section
      ref={sectionRef}
      aria-label={card.title}
      data-width={size.width}
      data-height={size.height}
      style={{ width: `calc(${size.width}% - ${GAP_PX}px)`, minWidth: "min(100%, 240px)" }}
      className="relative flex shrink-0 flex-col overflow-hidden rounded-lg border bg-card"
    >
      <header className="flex h-9 shrink-0 items-center gap-1 border-b px-2">
        <h2 className="min-w-0 flex-1 truncate text-sm font-medium" title={card.url}>{card.title}</h2>
        <IconButton label={t(($) => $.sidebar.custom_apps.linked_issues)} pressed={showIssues} onClick={() => setShowIssues((v) => !v)}>
          <Link2 />
        </IconButton>
        <IconButton label={t(($) => $.sidebar.custom_apps.refresh)} onClick={() => setReloads((n) => n + 1)}><RotateCw /></IconButton>
        <IconButton label={t(($) => $.sidebar.custom_apps.move_left)} disabled={!reorderable || index === 0} onClick={() => move(card.id, -1)}><ArrowLeft /></IconButton>
        <IconButton label={t(($) => $.sidebar.custom_apps.move_right)} disabled={!reorderable || index === total - 1} onClick={() => move(card.id, 1)}><ArrowRight /></IconButton>
        <Button size="icon-xs" variant="ghost" nativeButton={false}
          aria-label={t(($) => $.sidebar.custom_apps.fullscreen)} title={t(($) => $.sidebar.custom_apps.fullscreen)}
          render={<AppLink href={pageViewPath(slug, card.url)} />}>
          <Maximize2 />
        </Button>
        <Button size="icon-xs" variant="ghost" nativeButton={false}
          aria-label={t(($) => $.sidebar.custom_apps.open_in_new_tab)} title={t(($) => $.sidebar.custom_apps.open_in_new_tab)}
          render={<a href={card.url} target="_blank" rel="noopener noreferrer" />}>
          <ExternalLink />
        </Button>
        <IconButton label={t(($) => $.sidebar.custom_apps.remove)} onClick={() => remove(card.id)}><X /></IconButton>
      </header>
      {showIssues ? <LinkedIssues url={card.url} workspaceSlug={slug} className="border-b px-2 py-1.5" /> : null}
      {src ? (
        <PageFrame key={reloads} src={src} title={card.title} lazy style={{ height: size.height }} className="w-full" />
      ) : (
        <p style={{ height: size.height }} className="p-4 text-sm text-muted-foreground">{t(($) => $.sidebar.custom_apps.not_frameable)}</p>
      )}
      {/* Edge and corner handles; the corner also resizes with the arrow keys. */}
      <div aria-hidden="true" onPointerDown={startResize("x")}
        className="absolute top-9 right-0 bottom-4 w-1.5 cursor-ew-resize touch-none hover:bg-primary/30" />
      <div aria-hidden="true" onPointerDown={startResize("y")}
        className="absolute right-4 bottom-0 left-0 h-1.5 cursor-ns-resize touch-none hover:bg-primary/30" />
      <button
        type="button"
        aria-label={`${resizeLabel} ${Math.round(size.width)}% × ${size.height}px`}
        title={resizeLabel}
        onPointerDown={startResize("xy")}
        onKeyDown={onResizeKey}
        className="absolute right-0 bottom-0 size-4 cursor-nwse-resize touch-none rounded-tl-sm text-muted-foreground hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring"
      >
        <svg viewBox="0 0 10 10" aria-hidden="true" className="size-full p-0.5">
          <path d="M9 3L3 9M9 6L6 9" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" />
        </svg>
      </button>
      {dragAxis ? (
        // Keeps every framed page from swallowing the pointer while resizing.
        <div className={`fixed inset-0 z-50 ${AXIS_CURSOR[dragAxis]}`} />
      ) : null}
    </section>
  );
}

function Chip({ pressed, onClick, title, children }: { pressed: boolean; onClick: () => void; title?: string; children: React.ReactNode }) {
  return (
    <button type="button" aria-pressed={pressed} title={title} onClick={onClick}
      className={`inline-flex max-w-64 items-center gap-1 rounded-full border px-2 py-0.5 text-caption transition-colors ${
        pressed ? "border-primary bg-primary/10 text-primary" : "text-muted-foreground hover:bg-accent hover:text-foreground"}`}>
      {children}
    </button>
  );
}

const toggle = <T,>(list: T[], value: T) => (list.includes(value) ? list.filter((v) => v !== value) : [...list, value]);

/**
 * This browser's personal dashboard: several pages side by side, each sized by
 * the viewer. Filterable by where a card was pinned from and by the issues its
 * page is bound to; the filter lives in the URL so it survives navigation.
 */
export function CustomDashboardPage() {
  const { t } = useT("layout");
  const slug = useWorkspaceSlug();
  const { searchParams, replace } = useNavigation();
  const { cards } = useDashboard(slug);
  const [adding, setAdding] = useState(false);
  const linked = useCardsLinkedIssues(cards.map((card) => card.url));
  const filter = parseDashboardFilter(searchParams);
  if (!slug) return null;

  const filtering = filter.sources.length > 0 || filter.issueIds.length > 0;
  const shown = filterCards(cards, filter, linked.byUrl);
  const setFilter = (next: DashboardFilter) => replace(dashboardFilterPath(slug, next));
  const sources = CARD_SOURCES.filter((source) => cards.some((card) => card.source === source));
  // Selected issues stay listed even if no card links to them any more, so they can be cleared.
  const issueOptions = [
    ...linked.issues,
    ...filter.issueIds.filter((id) => !linked.issues.some((issue) => issue.id === id))
      .map((id) => ({ id, identifier: id.slice(0, 8), title: "" })),
  ];

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader>
        <h1 className="min-w-0 flex-1 truncate text-sm font-medium">{t(($) => $.sidebar.custom_apps.my_dashboard)}</h1>
        <span className="text-caption text-muted-foreground">
          {filtering ? `${shown.length}/${cards.length}` : `${cards.length}/${MAX_DASHBOARD_CARDS}`}
        </span>
        <Button type="button" size="sm" variant="outline" disabled={cards.length >= MAX_DASHBOARD_CARDS} onClick={() => setAdding(true)}>
          <Plus />
          {t(($) => $.sidebar.custom_apps.add_page)}
        </Button>
      </PageHeader>
      {cards.length > 0 ? (
        <div role="group" aria-label={t(($) => $.sidebar.custom_apps.filter)}
          className="flex shrink-0 flex-col gap-1.5 border-b px-4 py-2">
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="w-14 shrink-0 text-caption text-muted-foreground">{t(($) => $.sidebar.custom_apps.filter_source)}</span>
            {sources.map((source) => (
              <Chip key={source} pressed={filter.sources.includes(source)}
                onClick={() => setFilter({ ...filter, sources: toggle(filter.sources, source) })}>
                {source === "popos"
                  ? t(($) => $.sidebar.custom_apps.source_popos)
                  : source === "issue"
                    ? t(($) => $.sidebar.custom_apps.source_issue)
                    : t(($) => $.sidebar.custom_apps.source_manual)}
              </Chip>
            ))}
            {filtering ? (
              <Button type="button" size="xs" variant="ghost" className="ml-auto" onClick={() => setFilter({ sources: [], issueIds: [] })}>
                <X />
                {t(($) => $.sidebar.custom_apps.filter_clear)}
              </Button>
            ) : null}
          </div>
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="w-14 shrink-0 text-caption text-muted-foreground">{t(($) => $.sidebar.custom_apps.filter_issue)}</span>
            {issueOptions.length === 0 ? (
              <span className="text-caption text-muted-foreground">
                {linked.isLoading ? t(($) => $.sidebar.custom_apps.loading) : t(($) => $.sidebar.custom_apps.filter_no_issues)}
              </span>
            ) : issueOptions.map((issue) => (
              <Chip key={issue.id} pressed={filter.issueIds.includes(issue.id)} title={issue.title || undefined}
                onClick={() => setFilter({ ...filter, issueIds: toggle(filter.issueIds, issue.id) })}>
                <span className="shrink-0">{issue.identifier}</span>
                {issue.title ? <span className="truncate">{issue.title}</span> : null}
              </Chip>
            ))}
          </div>
        </div>
      ) : null}
      <div className="min-h-0 flex-1 overflow-y-auto p-4">
        {cards.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t(($) => $.sidebar.custom_apps.dashboard_empty)}</p>
        ) : shown.length === 0 && filter.issueIds.length > 0 && linked.isLoading ? (
          <p className="text-sm text-muted-foreground">{t(($) => $.sidebar.custom_apps.loading)}</p>
        ) : shown.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t(($) => $.sidebar.custom_apps.filter_empty)}</p>
        ) : (
          <div className="flex flex-wrap items-start" style={{ gap: GAP_PX, marginRight: -GAP_PX }}>
            {shown.map((card) => {
              const index = cards.indexOf(card);
              // Reordering moves a card within the full list, so it is off while filtering.
              return <Card key={card.id} card={card} index={index} total={cards.length} slug={slug} reorderable={!filtering} />;
            })}
          </div>
        )}
      </div>
      <AddPageDialog target="dashboard" open={adding} onOpenChange={setAdding} />
    </div>
  );
}
