"use client";

import { ExternalLink } from "lucide-react";
import { SidebarMenuButton, SidebarMenuItem } from "@multica/ui/components/ui/sidebar";
import { useT } from "../i18n";

export interface SidebarShortcutLinksProps {
  modelTraceUrl?: string;
  quickstartUrl?: string;
  className?: string;
}

function safeShortcutURL(value?: string): string | null {
  if (!value) return null;
  try {
    const url = new URL(value);
    return url.protocol === "https:" && !url.username && !url.password ? url.href : null;
  } catch { return null; }
}

export function SidebarShortcutLinks({ modelTraceUrl, quickstartUrl, className }: SidebarShortcutLinksProps) {
  const { t } = useT("layout");
  const links = [
    { key: "model_trace", url: safeShortcutURL(modelTraceUrl) },
    { key: "quickstart_wishlist", url: safeShortcutURL(quickstartUrl) },
  ] as const;
  return <>{links.map(link => link.url && <SidebarMenuItem key={link.key}>
    <SidebarMenuButton className={className} render={<a href={link.url} target="_blank" rel="noopener noreferrer" />}>
      <ExternalLink aria-hidden="true" />
      <span>{t(($) => $.sidebar.quick_links[link.key])}</span>
    </SidebarMenuButton>
  </SidebarMenuItem>)}</>;
}
