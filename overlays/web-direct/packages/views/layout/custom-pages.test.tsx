import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactElement, ReactNode } from "react";
import { act, cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderWithI18n } from "../test/i18n";

const api = vi.hoisted(() => ({
  listIssues: vi.fn(),
  setIssueMetadataKey: vi.fn(),
  deleteIssueMetadataKey: vi.fn(),
}));
vi.mock("@multica/core/api", () => ({ api }));
vi.mock("@multica/core/paths", () => ({
  useWorkspaceSlug: () => "acme",
  paths: { workspace: (slug: string) => ({ issueDetail: (id: string) => `/${slug}/issues/${id}` }) },
}));
vi.mock("./page-header", () => ({ PageHeader: ({ children }: { children: ReactNode }) => <header>{children}</header> }));
vi.mock("../navigation", () => ({
  AppLink: ({ href, children, ...props }: { href: string; children?: ReactNode }) => <a href={href} {...props}>{children}</a>,
}));

import {
  CARD_HEIGHT,
  CARD_WIDTH,
  addDashboardCard,
  canonicalPageUrl,
  pageIdForUrl,
  pageRef,
  pageViewPath,
  pagesFromMetadata,
  readDashboard,
} from "./custom-pages";
import { CustomDashboardPage } from "./custom-dashboard-page";
import { CustomPageView } from "./custom-page-view";
import { IssuePagesSection } from "../issues/components/issue-pages-section";
import { RELOAD_LOOP_LOADS, RELOAD_LOOP_WINDOW_MS } from "./page-frame";

const POPO = "https://task-schedule-calendar.popo.baidu-int.com/";

function renderWithQuery(ui: ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return renderWithI18n(<QueryClientProvider client={client}>{ui}</QueryClientProvider>, { locale: "zh-Hans" });
}

beforeEach(() => {
  window.localStorage.clear();
  api.listIssues.mockResolvedValue({ issues: [{ id: "i-1", identifier: "ZEPH-1", title: "日历需求" }], total: 1 });
  api.setIssueMetadataKey.mockResolvedValue(undefined);
  api.deleteIssueMetadataKey.mockResolvedValue(undefined);
});
afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("page identity", () => {
  it("names a popo work by its link and anything else by a stable hash", () => {
    expect(canonicalPageUrl("https://task-schedule-calendar.popo.baidu-int.com")).toBe(POPO);
    expect(pageRef("https://task-schedule-calendar.popo.baidu-int.com#x")).toMatchObject({
      id: "popo.task-schedule-calendar", key: "page.popo.task-schedule-calendar", url: POPO, title: "task-schedule-calendar",
    });
    const chart = pageIdForUrl("https://charts.example.test/live?id=7");
    expect(chart).toMatch(/^url\.[0-9a-z]{11}$/);
    expect(pageIdForUrl("https://charts.example.test/live?id=7")).toBe(chart);
    expect(pageIdForUrl("https://charts.example.test/live?id=8")).not.toBe(chart);
    expect(pageIdForUrl("https://task-schedule-calendar.popo.baidu-int.com/about")).toMatch(/^url\./);
    for (const key of [pageRef(POPO)!.key, `page.${chart}`]) expect(key).toMatch(/^[a-zA-Z_][a-zA-Z0-9_.-]{0,63}$/);
  });

  it("reads a pasted link without a scheme as https", () => {
    expect(canonicalPageUrl("ku.baidu-int.com/knowledge/HFVrC7hq1Q/")).toBe("https://ku.baidu-int.com/knowledge/HFVrC7hq1Q/");
    expect(canonicalPageUrl("  //charts.example.test/live ")).toBe("https://charts.example.test/live");
    expect(canonicalPageUrl("http://charts.example.test/live")).toBe("http://charts.example.test/live");
  });

  it("rejects links that must never be framed", () => {
    for (const bad of ["javascript:alert(1)", "data:text/html,x", "https://user:pw@example.test/", "file:///etc/passwd", "", 42]) {
      expect(canonicalPageUrl(bad)).toBeNull();
    }
  });

  it("reads only valid page bindings from issue metadata", () => {
    const pages = pagesFromMetadata({
      "page.popo.task-schedule-calendar": POPO,
      "page.url.bad": "javascript:alert(1)",
      pr_number: 42,
      pipeline_status: "green",
    });
    expect(pages.map((page) => page.key)).toEqual(["page.popo.task-schedule-calendar"]);
    expect(pageViewPath("acme", POPO)).toBe(`/acme/apps/view?url=${encodeURIComponent(POPO)}`);
  });
});

describe("personal dashboard", () => {
  it("keeps cards in this browser, without duplicates and up to twelve", () => {
    expect(addDashboardCard("acme", POPO)).toBe("added");
    expect(addDashboardCard("acme", "https://task-schedule-calendar.popo.baidu-int.com")).toBe("exists");
    expect(addDashboardCard("acme", "javascript:alert(1)")).toBe("invalid");
    for (let i = 0; i < 11; i++) expect(addDashboardCard("acme", `https://charts.example.test/${i}`)).toBe("added");
    expect(addDashboardCard("acme", "https://charts.example.test/overflow")).toBe("full");
    expect(readDashboard("acme")).toHaveLength(12);
    expect(readDashboard("other")).toHaveLength(0);
    expect(api.setIssueMetadataKey).not.toHaveBeenCalled();
  });

  it("reads v1 preset sizes and clamps stored sizes", () => {
    window.localStorage.setItem("multica:custom-dashboard:v1:acme", JSON.stringify([
      { url: POPO, title: "a", width: 3, height: "l" },
      { url: "https://charts.example.test/1", width: 5, height: 99999 },
      { url: "https://charts.example.test/2" },
    ]));
    expect(readDashboard("acme").map(({ width, height }) => [width, height])).toEqual([
      [100, 720], [CARD_WIDTH.min, CARD_HEIGHT.max], [CARD_WIDTH.default, CARD_HEIGHT.default],
    ]);
  });

  it("frames each card lazily and lets the viewer resize, reorder and remove it", async () => {
    addDashboardCard("acme", POPO, "任务日历");
    addDashboardCard("acme", "https://charts.example.test/live", "实时图表");
    renderWithQuery(<CustomDashboardPage />);
    const frames = screen.getAllByTitle(/任务日历|实时图表/).filter((el) => el.tagName === "IFRAME");
    expect(frames.map((frame) => frame.getAttribute("src"))).toEqual([POPO, "https://charts.example.test/live"]);
    for (const frame of frames) {
      expect(frame).toHaveAttribute("loading", "lazy");
      expect(frame).toHaveAttribute("referrerpolicy", "no-referrer");
      expect(frame.getAttribute("sandbox")).not.toContain("allow-top-navigation");
    }
    const calendar = screen.getByRole("region", { name: "任务日历" });
    const handle = calendar.querySelector<HTMLButtonElement>('button[title^="拖动调整大小"]')!;
    expect(handle).toHaveAccessibleName(/50% × 480px/);
    fireEvent.keyDown(handle, { key: "ArrowRight" });
    fireEvent.keyDown(handle, { key: "ArrowDown" });
    expect(readDashboard("acme")[0]).toMatchObject({ width: 55, height: 520 });
    expect(calendar.style.width).toBe("calc(55% - 12px)");
    expect(calendar.querySelector("iframe")!.style.height).toBe("520px");
    fireEvent.click(screen.getAllByRole("button", { name: "后移" })[0]!);
    expect(readDashboard("acme").map((card) => card.title)).toEqual(["实时图表", "任务日历"]);
    fireEvent.click(screen.getAllByRole("button", { name: "关联 issue" })[1]!);
    expect(await screen.findByRole("link", { name: /ZEPH-1/ })).toHaveAttribute("href", "/acme/issues/i-1");
    act(() => { fireEvent.click(screen.getAllByRole("button", { name: "移除" })[0]!); });
    expect(readDashboard("acme").map((card) => card.title)).toEqual(["任务日历"]);
  });
});

describe("issue ↔ page binding", () => {
  it("the page view lists its issues through the metadata filter", async () => {
    renderWithQuery(<CustomPageView url={POPO} />);
    expect(await screen.findByRole("link", { name: /ZEPH-1/ })).toHaveAttribute("href", "/acme/issues/i-1");
    expect(api.listIssues).toHaveBeenCalledWith({ metadata: { "page.popo.task-schedule-calendar": POPO }, limit: 50 });
    expect(screen.getByTitle("task-schedule-calendar")).toHaveAttribute("src", POPO);
    fireEvent.click(screen.getByRole("button", { name: "加到我的看板" }));
    expect(readDashboard("acme").map((card) => card.url)).toEqual([POPO]);
    fireEvent.click(screen.getByRole("button", { name: "已在我的看板" }));
    expect(readDashboard("acme")).toHaveLength(0);
  });

  it("refuses to frame a same-origin page", () => {
    renderWithQuery(<CustomPageView url={`${window.location.origin}/acme/issues`} />);
    expect(screen.getByText("这个链接不能在 multica 中显示")).toBeInTheDocument();
    expect(document.querySelector("iframe")).toBeNull();
  });

  it("the issue section binds, opens, pins and unbinds pages", async () => {
    const issue = { id: "i-1", workspace_id: "ws-1", metadata: { "page.popo.task-schedule-calendar": POPO, pr_number: 3 } };
    renderWithQuery(<IssuePagesSection issue={issue} />);
    const open = screen.getByRole("link", { name: "task-schedule-calendar" });
    expect(open).toHaveAttribute("href", pageViewPath("acme", POPO));
    // The pin is a toggle: muted = not on my dashboard, highlighted = on it.
    const pin = screen.getByRole("button", { name: "加到我的看板 task-schedule-calendar" });
    expect(pin).toHaveAttribute("aria-pressed", "false");
    fireEvent.click(pin);
    expect(readDashboard("acme").map((card) => card.url)).toEqual([POPO]);
    const pinned = screen.getByRole("button", { name: "已在我的看板，点击移出 task-schedule-calendar" });
    expect(pinned).toHaveAttribute("aria-pressed", "true");
    expect(pinned).toBeEnabled();
    fireEvent.click(pinned);
    expect(readDashboard("acme")).toHaveLength(0);

    fireEvent.click(screen.getByRole("button", { name: "绑定页面" }));
    fireEvent.change(screen.getByRole("textbox", { name: "粘贴页面链接" }), { target: { value: "charts.example.test/live" } });
    fireEvent.submit(screen.getByRole("textbox", { name: "粘贴页面链接" }).closest("form")!);
    const key = pageRef("https://charts.example.test/live")!.key;
    await waitFor(() => expect(api.setIssueMetadataKey).toHaveBeenCalledWith("i-1", key, "https://charts.example.test/live"));

    fireEvent.click(screen.getByRole("button", { name: "解绑 task-schedule-calendar" }));
    await waitFor(() => expect(api.deleteIssueMetadataKey).toHaveBeenCalledWith("i-1", "page.popo.task-schedule-calendar"));
  });
});

describe("reload loop guard", () => {
  it("pauses a page that keeps reloading and offers a new tab", () => {
    renderWithQuery(<CustomPageView url={POPO} />);
    const frame = () => screen.getByTitle("task-schedule-calendar");
    for (let i = 0; i < RELOAD_LOOP_LOADS - 1; i++) fireEvent.load(frame());
    expect(frame().tagName).toBe("IFRAME");
    fireEvent.load(frame());
    expect(document.querySelector("iframe")).toBeNull();
    expect(screen.getByRole("alert")).toHaveTextContent("反复刷新");
    expect(screen.getAllByRole("link", { name: "新标签页打开" }).every((link) => link.getAttribute("href") === POPO)).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "重试" }));
    expect(frame().tagName).toBe("IFRAME");
  });

  it("does not count loads spread over a long time", () => {
    const now = vi.spyOn(Date, "now");
    renderWithQuery(<CustomPageView url={POPO} />);
    for (let i = 0; i < RELOAD_LOOP_LOADS * 2; i++) {
      now.mockReturnValue(i * RELOAD_LOOP_WINDOW_MS);
      fireEvent.load(screen.getByTitle("task-schedule-calendar"));
    }
    expect(document.querySelector("iframe")).not.toBeNull();
    now.mockRestore();
  });
});
