"use client";

import { useState } from "react";
import { AppWindow, LayoutGrid, Plus, X } from "lucide-react";
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
import { dashboardPath, pageViewPath, useDashboard } from "./custom-pages";

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
 * Sidebar group "POPOs": the pages this viewer added (their dashboard, this
 * browser only), an add button, and a remove action per page.
 */
export function CustomAppsNav({ className }: { className?: string }) {
  const { t } = useT("layout");
  const { push } = useNavigation();
  const slug = useWorkspaceSlug();
  const { cards, remove } = useDashboard(slug);
  const [adding, setAdding] = useState(false);
  if (!slug) return null;
  const addLabel = t(($) => $.sidebar.custom_apps.add_page);
  const removeLabel = t(($) => $.sidebar.custom_apps.remove_from_sidebar);
  return (
    <SidebarGroup>
      <SidebarGroupLabel>{t(($) => $.sidebar.custom_apps.title)}</SidebarGroupLabel>
      <SidebarGroupAction aria-label={addLabel} title={addLabel} onClick={() => setAdding(true)}>
        <Plus />
      </SidebarGroupAction>
      <SidebarGroupContent>
        <SidebarMenu className="gap-0.5">
          {cards.map((card) => (
            <SidebarMenuItem key={card.id}>
              <SidebarMenuButton render={<AppLink href={pageViewPath(slug, card.url)} />} className={className}>
                <AppWindow aria-hidden="true" />
                <span title={card.url}>{card.title}</span>
              </SidebarMenuButton>
              <SidebarMenuAction showOnHover aria-label={`${removeLabel} ${card.title}`} title={removeLabel}
                onClick={() => remove(card.id)}>
                <X />
              </SidebarMenuAction>
            </SidebarMenuItem>
          ))}
          {cards.length === 0 ? (
            <SidebarMenuItem>
              <SidebarMenuButton className={`text-muted-foreground ${className ?? ""}`} onClick={() => setAdding(true)}>
                <Plus aria-hidden="true" />
                <span>{addLabel}</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
          ) : null}
        </SidebarMenu>
      </SidebarGroupContent>
      <AddPageDialog open={adding} onOpenChange={setAdding} onAdded={() => push(dashboardPath(slug))} />
    </SidebarGroup>
  );
}
