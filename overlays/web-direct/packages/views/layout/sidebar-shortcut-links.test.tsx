import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cloneElement, type ButtonHTMLAttributes, type ReactElement, type ReactNode } from "react";
import { cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import { renderWithI18n } from "../test/i18n";
import { SidebarShortcutLinks, SidebarCommunityLink } from "./sidebar-shortcut-links";

vi.mock("@multica/ui/components/ui/sidebar", () => ({
  SidebarMenu: ({ children }: { children: ReactNode }) => <ul>{children}</ul>,
  SidebarMenuItem: ({ children }: { children: ReactNode }) => <li>{children}</li>,
  SidebarMenuButton: ({ render, children, ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { render?: ReactElement }) =>
    render ? cloneElement(render, props as object, children) : <button {...props}>{children}</button>,
}));

const originalClipboard = Object.getOwnPropertyDescriptor(navigator, "clipboard");
const originalExecCommand = Object.getOwnPropertyDescriptor(document, "execCommand");
const copied: string[] = [];
beforeEach(() => {
  copied.length = 0;
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: true, json: async () => ({ ip: "192.0.2.10" }) }));
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: undefined });
  Object.defineProperty(document, "execCommand", { configurable: true, writable: true, value: vi.fn(() => {
    const input = document.activeElement;
    if (input instanceof HTMLTextAreaElement) copied.push(input.value);
    return true;
  }) });
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  if (originalClipboard) Object.defineProperty(navigator, "clipboard", originalClipboard);
  else Reflect.deleteProperty(navigator, "clipboard");
  if (originalExecCommand) Object.defineProperty(document, "execCommand", originalExecCommand);
  else Reflect.deleteProperty(document, "execCommand");
});

function renderLinks(modelTraceUrl = "https://example.test/trace/", quickstartUrl = "https://docs.example.test/quickstart") {
  renderWithI18n(<SidebarShortcutLinks modelTraceUrl={modelTraceUrl} quickstartUrl={quickstartUrl} />, { locale: "zh-Hans" });
}

describe("common sidebar links", () => {
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
    const mobile = await screen.findByRole("button", { name: "手机控制链接" });
    const links = screen.getAllByRole("link");
    expect(links.map(a => a.textContent)).toEqual(["OneAPI降智探测", "快速上手 & 许愿板", "官方文档"]);
    expect(mobile.compareDocumentPosition(screen.getByRole("link", { name: "OneAPI降智探测" })) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(links.map(a => a.getAttribute("href"))).toEqual(["https://example.test/trace/", "https://docs.example.test/quickstart", "https://multica.ai/docs/zh"]);
    for (const link of links) {
      expect(link).toHaveAttribute("target", "_blank");
      expect(link).toHaveAttribute("rel", "noopener noreferrer");
    }
    fireEvent.click(toggle);
    await waitFor(() => expect(screen.queryByRole("link")).not.toBeInTheDocument());
  });

  it("copies the exact mobile URL on HTTP and reports success", async () => {
    renderLinks();
    fireEvent.click(screen.getByRole("button", { name: "常用链接" }));
    await screen.findByText("http://192.0.2.10:8004");
    const button = screen.getByRole("button", { name: "手机控制链接" });
    button.focus();
    fireEvent.click(button);
    await screen.findByText("已复制");
    expect(copied).toEqual(["http://192.0.2.10:8004"]);
    expect(document.execCommand).toHaveBeenCalledWith("copy");
    expect(document.activeElement).toBe(button);
  });

  it("does not claim successful copying when the browser rejects it", async () => {
    document.execCommand = vi.fn().mockReturnValue(false);
    renderLinks();
    fireEvent.click(screen.getByRole("button", { name: "常用链接" }));
    await screen.findByText("http://192.0.2.10:8004");
    fireEvent.click(screen.getByRole("button", { name: "手机控制链接" }));
    await screen.findByText("复制失败，请重试");
    expect(screen.queryByText("已复制")).not.toBeInTheDocument();
  });

  it("keeps the menu usable when IP lookup fails or external URLs are unsafe", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: false }));
    renderLinks("javascript:alert(1)", "https://user:secret@example.test");
    fireEvent.click(screen.getByRole("button", { name: "常用链接" }));
    await screen.findByText("暂不可用");
    expect(screen.getByRole("button", { name: "手机控制链接" })).toBeDisabled();
    expect(screen.getAllByRole("link").map(a => a.textContent)).toEqual(["官方文档"]);
  });
});
