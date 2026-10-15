"use client";

import { useEffect, useRef, useState } from "react";
import { ChevronRight, ExternalLink, Link, Smartphone } from "lucide-react";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@multica/ui/components/ui/collapsible";
import { SidebarMenu, SidebarMenuButton, SidebarMenuItem } from "@multica/ui/components/ui/sidebar";
import { copyText } from "@multica/ui/lib/clipboard";
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

function MobileControlLink({ className }: { className?: string }) {
  const { t } = useT("layout");
  const [url, setUrl] = useState<string>();
  const [unavailable, setUnavailable] = useState(false);
  const [copyState, setCopyState] = useState<"idle" | "copied" | "failed">("idle");
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => {
    const controller = new AbortController();
    void fetch("/api/public-ip", { cache: "no-store", signal: controller.signal })
      .then(async response => {
        if (!response.ok) throw new Error("Address unavailable");
        const data: unknown = await response.json();
        if (!data || typeof data !== "object" || !("ip" in data) || typeof data.ip !== "string" ||
            !/^\d{1,3}(\.\d{1,3}){3}$/.test(data.ip) || data.ip.split(".").some(part => Number(part) > 255)) throw new Error("Invalid address");
        if (!controller.signal.aborted) setUrl(`http://${data.ip}:8004`);
      }).catch(() => { if (!controller.signal.aborted) setUnavailable(true); });
    return () => { controller.abort(); if (timer.current) clearTimeout(timer.current); };
  }, []);
  const copy = async () => {
    if (!url) return;
    setCopyState(await copyText(url) ? "copied" : "failed");
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => setCopyState("idle"), 1600);
  };
  const detail = copyState === "copied" ? t($ => $.sidebar.quick_links.copied)
    : copyState === "failed" ? t($ => $.sidebar.quick_links.copy_failed)
      : url || (unavailable ? t($ => $.sidebar.quick_links.unavailable) : t($ => $.sidebar.quick_links.loading));
  return <SidebarMenuItem>
    <SidebarMenuButton type="button" className={`${className || ""} h-auto min-h-8`} disabled={!url}
      onClick={copy} title={url} aria-label={t($ => $.sidebar.quick_links.mobile_control)}>
      <Smartphone aria-hidden="true" />
      <span className="min-w-0 group-data-[collapsible=icon]:hidden">
        <span className="block">{t($ => $.sidebar.quick_links.mobile_control)}</span>
        <span className="block break-all text-micro text-muted-foreground" aria-live="polite">{detail}</span>
      </span>
    </SidebarMenuButton>
  </SidebarMenuItem>;
}

export function SidebarShortcutLinks({ modelTraceUrl, quickstartUrl, className }: SidebarShortcutLinksProps) {
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
        <MobileControlLink className={className} />
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
