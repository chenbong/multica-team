import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cloneElement, type ButtonHTMLAttributes, type ReactElement, type ReactNode } from "react";
import { cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import { renderWithI18n } from "../test/i18n";
import { SidebarShortcutLinks, SidebarCommunityLink } from "./sidebar-shortcut-links";

vi.mock("../navigation", () => ({ AppLink: ({ children, onClick, ...props }: React.ComponentProps<"a">) => <a {...props} onClick={event => {event.preventDefault();onClick?.(event);}}>{children}</a> }));

vi.mock("@multica/ui/components/ui/sidebar", () => ({
  SidebarMenu: ({ children }: { children: ReactNode }) => <ul>{children}</ul>,
  SidebarMenuItem: ({ children }: { children: ReactNode }) => <li>{children}</li>,
  SidebarMenuButton: ({ render, children, ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { render?: ReactElement }) =>
    render ? cloneElement(render, props as object, children) : <button {...props}>{children}</button>,
}));

beforeEach(() => { vi.stubGlobal("fetch", vi.fn()); });
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

function renderLinks(modelTraceUrl = "https://example.test/trace/", quickstartUrl = "https://docs.example.test/quickstart") {
  renderWithI18n(<SidebarShortcutLinks modelTraceUrl={modelTraceUrl} quickstartUrl={quickstartUrl} />, { locale: "zh-Hans" });
}

describe("common sidebar links", () => {
  it("keeps statistics in common links and uses the current workspace in the same tab", async () => {
    renderWithI18n(<SidebarShortcutLinks usageHref="/example/usage" />, { locale: "zh-Hans" });
    expect(screen.queryByRole("link", {name:"统计"})).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "常用链接" }));
    const statistics=await screen.findByRole("link", {name:"统计"});
    expect(statistics).toHaveAttribute("href", "/example/usage");
    expect(statistics).not.toHaveAttribute("target");
    fireEvent.click(statistics);
    await waitFor(()=>expect(screen.queryByRole("link", {name:"统计"})).not.toBeInTheDocument());
  });

  it("keeps the community link outside the collapsible menu", async () => {
    renderWithI18n(<><SidebarShortcutLinks /><SidebarCommunityLink url="https://community.example.test/join" /></>, { locale: "zh-Hans" });
    const link = screen.getByRole("link", { name: "如流交流群" });
    expect(link).toHaveAttribute("href", "https://community.example.test/join");
    expect(link).toHaveAttribute("target", "_blank");
    expect(link).toHaveAttribute("rel", "noopener noreferrer");
    expect(link.closest('[data-slot="collapsible"]')).toBeNull();
    const toggle = screen.getByRole("button", { name: "常用链接" });
    fireEvent.click(toggle);
    expect(link).toBeVisible();
    fireEvent.click(toggle);
    await waitFor(() => expect(screen.queryByRole("link", { name: "官方文档" })).not.toBeInTheDocument());
    expect(link).toBeVisible();
  });

  it("does not render an unsafe community link", () => {
    renderWithI18n(<SidebarCommunityLink url="javascript:alert(1)" />, { locale: "zh-Hans" });
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
  });

  it("starts collapsed, opens the requested entries in order and can collapse again", async () => {
    renderLinks();
    const toggle = screen.getByRole("button", { name: "常用链接" });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    const links = screen.getAllByRole("link");
    expect(links.map(a => a.textContent)).toEqual(["OneAPI降智探测", "快速上手 & 许愿板", "官方文档"]);
    expect(links.map(a => a.getAttribute("href"))).toEqual(["https://example.test/trace/", "https://docs.example.test/quickstart", "https://multica.ai/docs/zh"]);
    for (const link of links) {
      expect(link).toHaveAttribute("target", "_blank");
      expect(link).toHaveAttribute("rel", "noopener noreferrer");
    }
    fireEvent.click(toggle);
    await waitFor(() => expect(screen.queryByRole("link")).not.toBeInTheDocument());
  });

  it("does not render mobile control or query the public IP when opened", () => {
    renderLinks();
    fireEvent.click(screen.getByRole("button", { name: "常用链接" }));
    expect(screen.queryByRole("button", {name:"手机控制链接"})).not.toBeInTheDocument();
    expect(fetch).not.toHaveBeenCalled();
  });

  it("keeps safe external links when other URLs are unsafe", () => {
    renderLinks("javascript:alert(1)", "https://user:secret@example.test");
    fireEvent.click(screen.getByRole("button", { name: "常用链接" }));
    expect(screen.getAllByRole("link").map(a => a.textContent)).toEqual(["官方文档"]);
  });
});
