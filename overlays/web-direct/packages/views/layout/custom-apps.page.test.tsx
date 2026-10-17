import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cloneElement, type ReactElement, type ReactNode } from "react";
import { cleanup, fireEvent, screen } from "@testing-library/react";
import { renderWithI18n } from "../test/i18n";

const nav = vi.hoisted(() => ({ push: vi.fn() }));
vi.mock("@multica/core/paths", () => ({ useWorkspaceSlug: () => "acme" }));
vi.mock("../navigation", () => ({
  useNavigation: () => ({ pathname: "/acme/apps", push: nav.push }),
  AppLink: ({ href, children, ...props }: { href: string; children?: ReactNode }) => <a href={href} {...props}>{children}</a>,
}));
vi.mock("@multica/ui/components/ui/sidebar", () => ({
  SidebarGroup: ({ children }: { children: ReactNode }) => <section>{children}</section>,
  SidebarGroupLabel: ({ children }: { children: ReactNode }) => <h2>{children}</h2>,
  SidebarGroupAction: ({ children, ...props }: { children: ReactNode }) => <button type="button" {...props}>{children}</button>,
  SidebarGroupContent: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  SidebarMenu: ({ children }: { children: ReactNode }) => <ul>{children}</ul>,
  SidebarMenuItem: ({ children }: { children: ReactNode }) => <li>{children}</li>,
  SidebarMenuAction: ({ children, showOnHover: _hover, ...props }: { children: ReactNode; showOnHover?: boolean }) =>
    <button type="button" {...props}>{children}</button>,
  SidebarMenuButton: ({ render, children, isActive, onClick }: {
    render?: ReactElement; children: ReactNode; isActive?: boolean; onClick?: () => void;
  }) =>
    render
      ? cloneElement(render, { "aria-current": isActive ? "page" : undefined } as object, children)
      : <button type="button" onClick={onClick}>{children}</button>,
}));

import { CustomAppsNav, CustomDashboardNavItem } from "./custom-apps-nav";
import { addDashboardCard, readDashboard } from "./custom-pages";

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
    expect(screen.getAllByRole("button", { name: "添加页面" })).toHaveLength(2);
    expect(screen.queryAllByRole("link")).toHaveLength(0);
    expect(fetch).not.toHaveBeenCalled();
  });

  it("lists only my own pages and deletes them from my dashboard", () => {
    addDashboardCard("acme", "https://charts.example.test/live", "实时图表");
    render(<CustomAppsNav />);
    expect(screen.getByRole("link", { name: "实时图表" })).toHaveAttribute(
      "href", `/acme/apps/view?url=${encodeURIComponent("https://charts.example.test/live")}`);
    expect(screen.getAllByRole("button", { name: "添加页面" })).toHaveLength(1);
    fireEvent.click(screen.getByRole("button", { name: "从 POPOs 移除 实时图表" }));
    expect(screen.queryByRole("link", { name: "实时图表" })).toBeNull();
    expect(readDashboard("acme")).toHaveLength(0);
  });

  it("links my dashboard from the Work group item", () => {
    render(<ul><CustomDashboardNavItem /></ul>);
    const link = screen.getByRole("link", { name: /我的看板/ });
    expect(link).toHaveAttribute("href", "/acme/apps");
    expect(link).toHaveAttribute("aria-current", "page");
  });
});
