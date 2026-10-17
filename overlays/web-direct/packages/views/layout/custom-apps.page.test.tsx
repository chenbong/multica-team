import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cloneElement, type ReactElement, type ReactNode } from "react";
import { cleanup, fireEvent, screen } from "@testing-library/react";
import { renderWithI18n } from "../test/i18n";

const nav = vi.hoisted(() => ({ push: vi.fn() }));
vi.mock("@multica/core/paths", () => ({ useWorkspaceSlug: () => "acme" }));
vi.mock("../navigation", () => ({
  useNavigation: () => ({ pathname: "/acme/apps", searchParams: new URLSearchParams(), push: nav.push }),
  AppLink: ({ href, children, ...props }: { href: string; children?: ReactNode }) => <a href={href} {...props}>{children}</a>,
}));
vi.mock("@multica/ui/components/ui/sidebar", () => ({
  SidebarGroup: ({ children }: { children: ReactNode }) => <section>{children}</section>,
  SidebarGroupLabel: ({ children }: { children: ReactNode }) => <h2>{children}</h2>,
  SidebarGroupAction: ({ children, ...props }: { children: ReactNode }) => <button type="button" {...props}>{children}</button>,
  SidebarGroupContent: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  SidebarMenu: ({ children }: { children: ReactNode }) => <ul>{children}</ul>,
  SidebarMenuItem: ({ children }: { children: ReactNode }) => <li>{children}</li>,
  SidebarMenuAction: ({ children, showOnHover: _hover, ...props }: { children: ReactNode; showOnHover?: boolean; className?: string }) =>
    <button type="button" {...props}>{children}</button>,
  SidebarMenuButton: ({ render, children, isActive, onClick }: {
    render?: ReactElement; children: ReactNode; isActive?: boolean; onClick?: () => void;
  }) =>
    render
      ? cloneElement(render, { "aria-current": isActive ? "page" : undefined } as object, children)
      : <button type="button" onClick={onClick}>{children}</button>,
}));

import { CustomAppsNav, CustomDashboardNavItem } from "./custom-apps-nav";
import { addDashboardCard, addPoposPage, readDashboard, readPopos } from "./custom-pages";

const render = (ui: ReactElement) => renderWithI18n(ui, { locale: "zh-Hans" });

beforeEach(() => {
  window.localStorage.clear();
  nav.push.mockClear();
  vi.stubGlobal("fetch", vi.fn());
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("POPOs sidebar group", () => {
  it("offers adding a page when I have none, and fetches no shared list", () => {
    render(<CustomAppsNav />);
    expect(screen.getByRole("heading", { name: "POPOs" })).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "添加到 POPOs" })).toHaveLength(2);
    expect(screen.queryAllByRole("link")).toHaveLength(0);
    expect(fetch).not.toHaveBeenCalled();
  });

  it("lists my POPOs pages, not my dashboard cards", () => {
    addDashboardCard("acme", "https://docs.example.test/", "只在看板");
    addPoposPage("acme", "https://charts.example.test/live", "实时图表");
    render(<CustomAppsNav />);
    expect(screen.queryByRole("link", { name: "只在看板" })).toBeNull();
    expect(screen.getByRole("link", { name: "实时图表" })).toHaveAttribute(
      "href", `/acme/apps/view?url=${encodeURIComponent("https://charts.example.test/live")}`);
    expect(screen.getAllByRole("button", { name: "添加到 POPOs" })).toHaveLength(1);
  });

  it("pins a POPOs page to the dashboard only on request, and removes it from POPOs only", () => {
    addPoposPage("acme", "https://charts.example.test/live", "实时图表");
    render(<CustomAppsNav />);
    expect(readDashboard("acme")).toHaveLength(0);
    const pin = screen.getByRole("button", { name: "加到我的看板 实时图表" });
    expect(pin).toHaveAttribute("aria-pressed", "false");
    fireEvent.click(pin);
    expect(readDashboard("acme").map((card) => [card.title, card.source])).toEqual([["实时图表", "popos"]]);
    expect(screen.getByRole("button", { name: "已在我的看板，点击移出 实时图表" })).toHaveAttribute("aria-pressed", "true");

    fireEvent.click(screen.getByRole("button", { name: "从 POPOs 移除 实时图表" }));
    expect(readPopos("acme")).toHaveLength(0);
    expect(readDashboard("acme")).toHaveLength(1);
  });

  it("adds to POPOs from the dialog without touching the dashboard", () => {
    render(<CustomAppsNav />);
    fireEvent.click(screen.getAllByRole("button", { name: "添加到 POPOs" })[0]!);
    fireEvent.change(screen.getByLabelText("页面链接"), { target: { value: "charts.example.test/live" } });
    fireEvent.submit(screen.getByLabelText("页面链接").closest("form")!);
    expect(readPopos("acme").map((page) => page.url)).toEqual(["https://charts.example.test/live"]);
    expect(readDashboard("acme")).toHaveLength(0);
    expect(nav.push).toHaveBeenCalledWith(`/acme/apps/view?url=${encodeURIComponent("https://charts.example.test/live")}`);
  });

  it("links my dashboard from the Work group item", () => {
    render(<ul><CustomDashboardNavItem /></ul>);
    const link = screen.getByRole("link", { name: /我的看板/ });
    expect(link).toHaveAttribute("href", "/acme/apps");
    expect(link).toHaveAttribute("aria-current", "page");
  });
});
