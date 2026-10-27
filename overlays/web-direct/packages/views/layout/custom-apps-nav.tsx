"use client";

import { useState } from "react";
import { AppWindow, LayoutGrid, Pin, Plus, X } from "lucide-react";
import { useWorkspaceSlug } from "@multica/core/paths";
import {
  SidebarGroup,
  SidebarGroupAction,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuAction,
  SidebarMenuButton,
  SidebarMenuItem,
} from "@multica/ui/components/ui/sidebar";
import { AppLink, useNavigation } from "../navigation";
import { useT } from "../i18n";
import { AddPageDialog } from "./add-page-dialog";
import { MAX_DASHBOARD_CARDS, dashboardPath, pageViewPath, useDashboard, usePopos } from "./custom-pages";

/** "My dashboard", listed in the sidebar's Work group. */
export function CustomDashboardNavItem({ className }: { className?: string }) {
  const { t } = useT("layout");
  const { pathname } = useNavigation();
  const slug = useWorkspaceSlug();
  if (!slug) return null;
  const href = dashboardPath(slug);
  return (
    <SidebarMenuItem>
      <SidebarMenuButton isActive={pathname === href} render={<AppLink href={href} />} className={className}>
        <LayoutGrid aria-hidden="true" />
        <span>{t(($) => $.sidebar.custom_apps.my_dashboard)}</span>
      </SidebarMenuButton>
    </SidebarMenuItem>
  );
}

/**
 * Sidebar group "POPOs": the viewer's own page list (this browser only). It is
 * separate from the dashboard; each page has a pin toggle that puts it on the
 * dashboard (source "popos") and a remove action.
 */
export function CustomAppsNav({ className }: { className?: string }) {
  const { t } = useT("layout");
  const { pathname, searchParams, push } = useNavigation();
  const slug = useWorkspaceSlug();
  const { pages, remove } = usePopos(slug);
  const dashboard = useDashboard(slug);
  const [adding, setAdding] = useState(false);
  if (!slug) return null;
  const addLabel = t(($) => $.sidebar.custom_apps.popos_add);
  const removeLabel = t(($) => $.sidebar.custom_apps.remove_from_sidebar);
  const dashboardFull = dashboard.cards.length >= MAX_DASHBOARD_CARDS;
  return (
    <SidebarGroup>
      <SidebarGroupLabel>{t(($) => $.sidebar.custom_apps.title)}</SidebarGroupLabel>
      <SidebarGroupAction aria-label={addLabel} title={addLabel} onClick={() => setAdding(true)}>
        <Plus />
      </SidebarGroupAction>
      <SidebarGroupContent>
        <SidebarMenu className="gap-0.5">
          {pages.map((page) => {
            const href = pageViewPath(slug, page.url);
            const active = pathname === href.split("?")[0] && searchParams.get("url") === page.url;
            const pinned = dashboard.cards.some((card) => card.url === page.url);
            const pinLabel = pinned
              ? t(($) => $.sidebar.custom_apps.remove_from_dashboard)
              : dashboardFull
                ? t(($) => $.sidebar.custom_apps.full, { count: MAX_DASHBOARD_CARDS })
                : t(($) => $.sidebar.custom_apps.add_to_dashboard);
            return (
              <SidebarMenuItem key={page.id}>
                <SidebarMenuButton isActive={active} render={<AppLink href={href} />} className={`pr-12 ${className ?? ""}`}>
                  <AppWindow aria-hidden="true" />
                  <span title={page.url}>{page.title}</span>
                </SidebarMenuButton>
                {/* Pinned pages keep the pin visible so the state reads at a glance. */}
                <SidebarMenuAction showOnHover={!pinned} aria-pressed={pinned} aria-label={`${pinLabel} ${page.title}`} title={pinLabel}
                  disabled={!pinned && dashboardFull}
                  className={`right-6 ${pinned ? "text-primary" : "text-muted-foreground"}`}
                  onClick={() => (pinned ? dashboard.removeUrl(page.url) : dashboard.add(page.url, page.title, "popos"))}>
                  <Pin className={pinned ? "fill-current" : undefined} />
                </SidebarMenuAction>
                <SidebarMenuAction showOnHover aria-label={`${removeLabel} ${page.title}`} title={removeLabel}
                  onClick={() => remove(page.id)}>
                  <X />
                </SidebarMenuAction>
              </SidebarMenuItem>
            );
          })}
          {pages.length === 0 ? (
            <SidebarMenuItem>
              <SidebarMenuButton className={`text-muted-foreground ${className ?? ""}`} onClick={() => setAdding(true)}>
                <Plus aria-hidden="true" />
                <span>{addLabel}</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
          ) : null}
        </SidebarMenu>
      </SidebarGroupContent>
      <AddPageDialog target="popos" open={adding} onOpenChange={setAdding} onAdded={(url) => push(pageViewPath(slug, url))} />
    </SidebarGroup>
  );
}
