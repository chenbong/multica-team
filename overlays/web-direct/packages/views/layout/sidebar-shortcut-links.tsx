"use client";

import { useState } from "react";
import { BarChart3, ChevronRight, ExternalLink, Link } from "lucide-react";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@multica/ui/components/ui/collapsible";
import { SidebarMenu, SidebarMenuButton, SidebarMenuItem } from "@multica/ui/components/ui/sidebar";
import { useT } from "../i18n";
import { AppLink } from "../navigation";

export interface SidebarShortcutLinksProps {
  usageHref?: string;
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

export function SidebarCommunityLink({ url, className }: { url?: string; className?: string }) {
  const { t } = useT("layout");
  const href = safeShortcutURL(url);
  if (!href) return null;
  return <SidebarMenu>
    <SidebarMenuItem>
      <SidebarMenuButton className={className} render={<a href={href} target="_blank" rel="noopener noreferrer" />}>
        <ExternalLink aria-hidden="true" />
        <span>{t($ => $.sidebar.infoflow_community)}</span>
      </SidebarMenuButton>
    </SidebarMenuItem>
  </SidebarMenu>;
}

export function SidebarShortcutLinks({ usageHref, modelTraceUrl, quickstartUrl, className }: SidebarShortcutLinksProps) {
  const { t } = useT("layout");
  const [open, setOpen] = useState(false);
  const links = [
    { key: "model_trace", url: safeShortcutURL(modelTraceUrl) },
    { key: "quickstart_wishlist", url: safeShortcutURL(quickstartUrl) },
    { key: "official_docs", url: "https://multica.ai/docs/zh" },
  ] as const;
  return <Collapsible className="relative" open={open} onOpenChange={setOpen}>
    <SidebarMenu>
      <SidebarMenuItem>
        <SidebarMenuButton className={className} render={<CollapsibleTrigger />}>
          <Link aria-hidden="true" />
          <span>{t($ => $.sidebar.quick_links.title)}</span>
          <ChevronRight aria-hidden="true" className={`ml-auto transition-transform ${open ? "-rotate-90" : ""}`} />
        </SidebarMenuButton>
      </SidebarMenuItem>
    </SidebarMenu>
    <CollapsibleContent className="absolute inset-x-0 bottom-full z-50 mb-1 max-h-[60vh] overflow-y-auto rounded-lg border border-sidebar-border bg-sidebar p-1 shadow-lg">
      <SidebarMenu className="gap-0.5">
        {usageHref && /^\/(?!\/)[^\\\s]*$/.test(usageHref) && <SidebarMenuItem>
          <SidebarMenuButton className={className} onClick={() => setOpen(false)} render={<AppLink href={usageHref} />}>
            <BarChart3 aria-hidden="true" />
            <span>{t($ => $.nav.usage)}</span>
          </SidebarMenuButton>
        </SidebarMenuItem>}
        {links.map(link => link.url && <SidebarMenuItem key={link.key}>
          <SidebarMenuButton className={className} render={<a href={link.url} target="_blank" rel="noopener noreferrer" />}>
            <ExternalLink aria-hidden="true" />
            <span>{t($ => $.sidebar.quick_links[link.key])}</span>
          </SidebarMenuButton>
        </SidebarMenuItem>)}
      </SidebarMenu>
    </CollapsibleContent>
  </Collapsible>;
}
