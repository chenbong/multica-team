"use client";

import { useEffect, useMemo, useRef, useState, type ReactNode, type PointerEvent as ReactPointerEvent } from "react";
import { useQueries, useQueryClient } from "@tanstack/react-query";
import { ExternalLink, RefreshCw, X } from "lucide-react";
import { useAuthStore } from "@multica/core/auth";
import { ApiError } from "@multica/core/api";
import { useCurrentWorkspace } from "@multica/core/paths";
import { useWS } from "@multica/core/realtime";
import {
  ResourceWebPanelStateProvider, canEmbedWebPanel, hasKnownWebPanelRestriction, clampWebPanelWidth, useResourceWebPanelContext,
  useResourceWebPanelState, webPanelKeys, webPanelListOptions, webPanelReferenceKey,
  WebPanelChangedSchema, type ResourceWebPanel, type WebPanelQueryContext, type WebPanelResource,
} from "@multica/core/web-panels";
import { Button, buttonVariants } from "@multica/ui/components/ui/button";
import { Select, SelectTrigger, SelectValue, SelectContent, SelectItem } from "@multica/ui/components/ui/select";
import { useT } from "../i18n";
import { useWebPanelLoadGuard } from "./use-web-panel-load-guard";

// No platform credentials are appended to frame URLs or messages. Browser
// cookie rules still govern the external page. Native alert/confirm modals are not
// enabled because opening a web panel must not lock the parent task page.
export const RESOURCE_WEB_PANEL_SANDBOX = "allow-scripts allow-same-origin allow-forms allow-popups allow-popups-to-escape-sandbox allow-downloads";

export function ResourceWebPanelShellProvider({ children }: { children: ReactNode }) {
  const workspace = useCurrentWorkspace();
  const userId = useAuthStore((state) => state.user?.id ?? null);
  if (!workspace || !userId) return children;
  return (
    <ResourceWebPanelStateProvider key={`${userId}:${workspace.id}`} context={{ userId, workspaceId: workspace.id, workspaceSlug: workspace.slug }}>
      <WebPanelSubscriptions />
      {children}
    </ResourceWebPanelStateProvider>
  );
}

function WebPanelSubscriptions() {
  const context = useResourceWebPanelContext()!;
  const client = useQueryClient();
  const { subscribe, onReconnect } = useWS();
  const { userId, workspaceId, workspaceSlug } = context.queryContext;
  useEffect(() => {
    const current: WebPanelQueryContext = { userId, workspaceId, workspaceSlug };
    const refresh = () => { void client.invalidateQueries({ queryKey: webPanelKeys.workspace(current) }); };
    const unsubscribe = subscribe("resource_web_panel:changed", (raw) => {
      const parsed = WebPanelChangedSchema.safeParse(raw);
      if (parsed.success && parsed.data.workspaceId === workspaceId) {
        void client.invalidateQueries({ queryKey: webPanelKeys.resource(current, parsed.data) });
      }
    });
    const cleanups = [unsubscribe, onReconnect(refresh), subscribe("member:updated", refresh), subscribe("member:removed", refresh), subscribe("issue:updated", refresh), subscribe("issue:deleted", refresh)];
    return () => cleanups.forEach((cleanup) => cleanup());
  }, [client, onReconnect, subscribe, userId, workspaceId, workspaceSlug]);
  return null;
}

export function ResourceWebPanelLayout({ children }: { children: ReactNode }) {
  return (
    <div className="relative flex h-full min-h-0 min-w-0 flex-1">
      <div className="relative flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden" data-resource-web-panel-main="">
        {children}
      </div>
      <ResourceWebPanelDock />
    </div>
  );
}

function PanelFrame({ panel, active }: { panel: ResourceWebPanel; active: boolean }) {
  const { t } = useT("layout");
  const allowed = typeof window !== "undefined" && canEmbedWebPanel(panel.url, window.location.origin);
  const restricted = typeof window !== "undefined" && hasKnownWebPanelRestriction(panel.url, window.location.origin);
  const { frameRef, loading, failure, onLoad, stop } = useWebPanelLoadGuard(panel.url, allowed && !restricted);
  const message = restricted || failure === "restricted" ? t(($) => $.sidebar.web_panels.site_restricted)
    : failure === "timeout" ? t(($) => $.sidebar.web_panels.embed_timeout)
    : failure === "loop" ? t(($) => $.sidebar.web_panels.embed_loop)
    : failure === "error" ? t(($) => $.sidebar.web_panels.embed_failed)
    : t(($) => $.sidebar.web_panels.not_embeddable);
  return (
    <div style={{ display: active ? "flex" : "none" }} aria-hidden={!active} className="relative min-h-0 flex-1 flex-col" data-web-panel-frame-id={panel.id}>
      {allowed && !restricted && !failure ? (
        <>
          <div className="relative flex min-h-0 flex-1 flex-col">
            {loading ? <p role="status" className="absolute top-2 left-3 z-10 rounded bg-background/90 px-2 py-1 text-caption text-muted-foreground">{t(($) => $.sidebar.web_panels.loading)}</p> : null}
            <iframe
              ref={frameRef}
              src={panel.url}
              title={panel.name}
              sandbox={RESOURCE_WEB_PANEL_SANDBOX}
              referrerPolicy="no-referrer"
              allow="clipboard-write; fullscreen"
              tabIndex={active ? 0 : -1}
              onLoad={onLoad}
              className="min-h-0 w-full flex-1 border-0 bg-background"
            />
          </div>
          <div className="shrink-0 border-t px-3 py-2">
            <Button type="button" variant="link" size="xs" className="h-auto px-0 text-muted-foreground" onClick={() => stop("manual")}>{t(($) => $.sidebar.web_panels.cannot_display)}</Button>
          </div>
        </>
      ) : <div role="alert" className="flex min-h-0 flex-1 flex-col items-start justify-center gap-4 overflow-auto p-6">
        <p className="text-sm text-muted-foreground">{message}</p>
        <a className={buttonVariants({ size: "sm", variant: "default" })} href={panel.url} target="_blank" rel="noopener noreferrer"><ExternalLink />{t(($) => $.sidebar.web_panels.open_new_tab)}</a>
      </div>}
    </div>
  );
}

/** Stable keys retain browsing contexts until URL change or explicit refresh. */
export function ResourceWebPanelDock() {
  const context = useResourceWebPanelContext();
  if (!context) return null;
  return <WebPanelDockContent />;
}

function WebPanelDockContent() {
  const { t } = useT("layout");
  const context = useResourceWebPanelContext()!;
  const { store, queryContext } = context;
  const state = useResourceWebPanelState((value) => value);
  const issueIds = useMemo(() => [...new Set(state.issueContexts.map((item) => item.issueId))], [state.issueContexts]);
  const resources = useMemo<WebPanelResource[]>(() => [
    { scope: "workspace", resourceId: queryContext.workspaceId },
    ...issueIds.map((resourceId) => ({ scope: "issue" as const, resourceId })),
  ], [queryContext.workspaceId, issueIds]);
  const queries = useQueries({ queries: resources.map((resource) => webPanelListOptions(queryContext, resource)) });
  const panels = queries.flatMap((result) => {
    if (result.isError && result.error instanceof ApiError && [401, 403, 404].includes(result.error.status)) return [];
    return result.data?.panels ?? [];
  });
  const byId = new Map(panels.map((panel) => [panel.id, panel]));
  const currentIssue = state.issueContexts.at(-1)?.issueId;
  const choices = panels.filter((panel) => panel.scope === "workspace" || panel.resourceId === currentIssue);
  const activeRef = state.visited.find((ref) => webPanelReferenceKey(ref) === state.activeKey);
  const active = activeRef ? byId.get(activeRef.panelId) : null;
  const [frameRevisions, setFrameRevisions] = useState<Record<string, number>>({});
  const [draftWidth, setDraftWidth] = useState<number | null>(null);
  const [dragging, setDragging] = useState(false);
  const cleanupDrag = useRef<(() => void) | null>(null);
  useEffect(() => () => cleanupDrag.current?.(), []);

  // Authorization errors or a confirmed deletion discard cached frames, not
  // only their buttons. Network/5xx errors do not erase a still-valid form.
  useEffect(() => {
    for (const ref of state.visited) {
      const index = resources.findIndex((resource) => resource.scope === ref.scope && resource.resourceId === ref.resourceId);
      const result = queries[index];
      const denied = result?.isError && result.error instanceof ApiError && [401, 403, 404].includes(result.error.status);
      if (index < 0 || denied || (result?.data && !result.data.panels.some((panel) => panel.id === ref.panelId))) store.getState().forget(ref.panelId);
    }
  }, [store, state.visited, resources, queries]);

  const resize = (event: ReactPointerEvent<HTMLButtonElement>) => {
    if (event.button !== 0) return;
    event.preventDefault();
    cleanupDrag.current?.();
    const initialX = event.clientX;
    const initialWidth = event.currentTarget.parentElement?.getBoundingClientRect().width || state.width;
    let latest = initialWidth;
    const move = (next: PointerEvent) => {
      latest = clampWebPanelWidth(initialWidth + initialX - next.clientX);
      setDraftWidth(latest);
    };
    const finish = () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", finish);
      window.removeEventListener("pointercancel", finish);
      cleanupDrag.current = null;
      store.getState().setWidth(latest);
      setDraftWidth(null);
      setDragging(false);
    };
    cleanupDrag.current = finish;
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", finish);
    window.addEventListener("pointercancel", finish);
    setDragging(true);
  };

  const width = draftWidth ?? state.width;
  return (
    <aside aria-label={t(($) => $.sidebar.web_panels.title)} data-resource-web-panel-dock=""
      style={{ display: active ? "flex" : "none", width, maxWidth: "min(70vw, calc(100% - 240px))" }}
      className="relative min-h-0 shrink-0 flex-col border-l bg-background">
      <button type="button" role="separator" aria-orientation="vertical"
        aria-label={t(($) => $.sidebar.web_panels.resize)} aria-valuemin={280} aria-valuemax={1000} aria-valuenow={Math.round(width)}
        onPointerDown={resize}
        onKeyDown={(event) => {
          if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
          event.preventDefault();
          store.getState().setWidth(event.key === "Home" ? 280 : event.key === "End" ? 1000 : state.width + (event.key === "ArrowLeft" ? 32 : -32));
        }}
        className="absolute inset-y-0 -left-1 z-20 w-2 cursor-col-resize touch-none hover:bg-primary/20 focus-visible:bg-primary/20 focus-visible:outline-none" />
      <header className="flex h-12 shrink-0 items-center gap-1 border-b px-2">
        {active ? (
          <Select value={state.activeKey} items={Object.fromEntries(choices.map((panel) => [webPanelReferenceKey({ scope: panel.scope, resourceId: panel.resourceId, panelId: panel.id }), panel.name]))}
            onValueChange={(key) => {
              const panel = choices.find((item) => webPanelReferenceKey({ scope: item.scope, resourceId: item.resourceId, panelId: item.id }) === key);
              if (panel) store.getState().open({ scope: panel.scope, resourceId: panel.resourceId, panelId: panel.id });
            }}>
            <SelectTrigger size="sm" aria-label={t(($) => $.sidebar.web_panels.choose)} className="min-w-0 flex-1"><SelectValue /></SelectTrigger>
            <SelectContent>
              {choices.map((panel) => (
                <SelectItem key={panel.id} value={webPanelReferenceKey({ scope: panel.scope, resourceId: panel.resourceId, panelId: panel.id })}>
                  {panel.scope === "issue" ? t(($) => $.sidebar.web_panels.issue_title) : t(($) => $.sidebar.web_panels.workspace_title)} · {panel.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : null}
        {active ? <Button type="button" size="icon-sm" variant="ghost" aria-label={t(($) => $.sidebar.web_panels.refresh)} title={t(($) => $.sidebar.web_panels.refresh_hint)}
          onClick={() => {
            const key = state.activeKey;
            if (key) setFrameRevisions((current) => ({ ...current, [key]: (current[key] ?? 0) + 1 }));
          }}><RefreshCw /></Button> : null}
        {active ? <a className={buttonVariants({ size: "icon-sm", variant: "ghost" })} aria-label={t(($) => $.sidebar.web_panels.open_new_tab)} title={t(($) => $.sidebar.web_panels.open_new_tab)} href={active.url} target="_blank" rel="noopener noreferrer"><ExternalLink /></a> : null}
        <Button type="button" size="icon-sm" variant="ghost" aria-label={t(($) => $.sidebar.web_panels.close)} title={t(($) => $.sidebar.web_panels.close)} onClick={() => store.getState().close()}><X /></Button>
      </header>
      <div className="flex min-h-0 flex-1 flex-col">
        {state.visited.map((ref) => {
          const panel = byId.get(ref.panelId);
          const key = webPanelReferenceKey(ref);
          // Remount only this frame. This also retries failed/stalled cross-
          // origin navigations without reading its protected window.location.
          return panel ? <PanelFrame key={`${key}:${panel.url}:${frameRevisions[key] ?? 0}`} panel={panel} active={key === state.activeKey} /> : null;
        })}
      </div>
      {dragging ? <div className="fixed inset-0 z-50 cursor-col-resize" /> : null}
    </aside>
  );
}
