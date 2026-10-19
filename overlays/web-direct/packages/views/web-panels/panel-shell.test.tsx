import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useEffect, type ReactNode } from "react";
import { act, cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ResourceWebPanelStateProvider, useResourceWebPanelContext, useResourceWebPanelIssueScope, webPanelKeys, type ResourceWebPanel, type WebPanelQueryContext, type WebPanelResource, type WebPanelStore } from "@multica/core/web-panels";
import { renderWithI18n } from "../test/i18n";
import { ResourceWebPanelLayout, RESOURCE_WEB_PANEL_SANDBOX } from "./panel-shell";
import { WorkspaceWebPanelsNav, IssueWebPanelsSection } from "./panel-list";
import { ApiError } from "@multica/core/api";

const api = vi.hoisted(() => ({ listResourceWebPanels: vi.fn(), createResourceWebPanel: vi.fn(), updateResourceWebPanel: vi.fn(), deleteResourceWebPanel: vi.fn() }));
vi.mock("@multica/core/api", async (original) => ({ ...await original<object>(), api }));

const ids = {
  user: "00000000-0000-4000-8000-000000000001", workspace: "00000000-0000-4000-8000-000000000002", issue: "00000000-0000-4000-8000-000000000003",
};
const context: WebPanelQueryContext = { userId: ids.user, workspaceId: ids.workspace, workspaceSlug: "acme" };
const resource: WebPanelResource = { scope: "workspace", resourceId: ids.workspace };
function panel(id: string, name: string, url: string, scope: "workspace" | "issue" = "workspace"): ResourceWebPanel {
  return { id, name, url, scope, resourceId: scope === "workspace" ? ids.workspace : ids.issue, workspaceId: ids.workspace, createdBy: ids.user, canManage: true, createdAt: "2026-10-18T00:00:00Z", updatedAt: "2026-10-18T00:00:00Z" };
}
const first = panel("00000000-0000-4000-8000-000000000010", "排期日历", "https://calendar.example.test/#/team-a");
const second = panel("00000000-0000-4000-8000-000000000011", "数据图表", "https://charts.example.test/?view=team#daily");
const task = panel("00000000-0000-4000-8000-000000000012", "任务结果", "https://result.example.test/", "issue");
let store: WebPanelStore;
function TaskScope({ active }: { active: boolean }) {
  useResourceWebPanelIssueScope(active ? ids.issue : null);
  const current = useResourceWebPanelContext()!;
  useEffect(() => { store = current.store; }, [current.store]);
  return active ? <IssueWebPanelsSection issueId={ids.issue} /> : null;
}
function app(client: QueryClient, nextContext = context, activeTask = true, children?: ReactNode) {
  return <QueryClientProvider client={client}><ResourceWebPanelStateProvider context={nextContext}><ResourceWebPanelLayout>
    <label>任务草稿<input aria-label="任务草稿" defaultValue="未发送的内容" /></label>
    <WorkspaceWebPanelsNav /><TaskScope active={activeTask} />{children}
  </ResourceWebPanelLayout></ResourceWebPanelStateProvider></QueryClientProvider>;
}
const clients: QueryClient[] = [];
function client() { const value = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } }); clients.push(value); return value; }
beforeEach(() => {
  window.localStorage.clear();
  vi.clearAllMocks();
  api.listResourceWebPanels.mockImplementation(async (target: WebPanelResource) => ({ panels: target.scope === "workspace" ? [first, second] : [task], canAdd: true, limit: 50 }));
});
afterEach(() => { cleanup(); clients.forEach((value) => value.clear()); clients.length = 0; });

describe("resource-bound web panel dock", () => {
  it("uses workspace panels and task panels as the resource entry labels", async () => {
    renderWithI18n(app(client()), { locale: "zh-Hans" });
    const workspaceHeading = await screen.findByText("工作区面板");
    expect(workspaceHeading).toBeInTheDocument();
    expect(workspaceHeading.querySelector("svg")).toBeNull();
    expect(screen.getByRole("heading", { name: "任务面板" })).toBeInTheDocument();
  });
  it("refreshes only the active frame while preserving the task draft and other frames", async () => {
    renderWithI18n(app(client()), { locale: "zh-Hans" });
    fireEvent.click(await screen.findByRole("button", { name: "排期日历" }));
    const other = await screen.findByTitle("排期日历");
    fireEvent.click(screen.getByRole("button", { name: "数据图表" }));
    const original = await screen.findByTitle("数据图表");
    fireEvent.load(original);
    fireEvent.change(screen.getByLabelText("任务草稿"), { target: { value: "不要丢掉任务草稿" } });
    fireEvent.click(screen.getByRole("button", { name: "刷新页面" }));
    const refreshed = await screen.findByTitle("数据图表");
    expect(refreshed).not.toBe(original);
    expect(original.isConnected).toBe(false);
    expect(refreshed).toHaveAttribute("src", second.url);
    expect(screen.getByTitle("排期日历")).toBe(other);
    expect(screen.getByLabelText("任务草稿")).toHaveValue("不要丢掉任务草稿");
    expect(screen.getByRole("button", { name: "刷新页面" })).toBeEnabled();
    expect(screen.getByRole("status")).toHaveTextContent("加载中...");
    expect(store.getState().visited).toHaveLength(2);
    fireEvent.load(refreshed);
    expect(screen.queryByRole("status")).toBeNull();
  });
  it("can refresh a stalled frame without waiting for its load event", async () => {
    renderWithI18n(app(client(), context, false), { locale: "zh-Hans" });
    fireEvent.click(await screen.findByRole("button", { name: "排期日历" }));
    const original = await screen.findByTitle("排期日历");
    expect(screen.getByRole("status")).toHaveTextContent("加载中...");
    fireEvent.click(screen.getByRole("button", { name: "刷新页面" }));
    const refreshed = await screen.findByTitle("排期日历");
    expect(refreshed).not.toBe(original);
    expect(refreshed).toHaveAttribute("src", first.url);
    expect(screen.getByRole("status")).toHaveTextContent("加载中...");
  });
  it("never creates a frame for verified incompatible sites and preserves the original external URL", async () => {
    for (const url of ["https://www.baidu.com/?source=panel#top", "https://ku.baidu-int.com/knowledge/a/b/c/d"]) {
      api.listResourceWebPanels.mockResolvedValue({ panels: [{ ...first, url }], canAdd: true, limit: 50 });
      const view = renderWithI18n(app(client(), context, false), { locale: "zh-Hans" });
      fireEvent.click(await screen.findByRole("button", { name: "排期日历" }));
      const fallback = screen.getByRole("alert");
      expect(fallback).toHaveTextContent("由于该网站限制，无法在页内打开，请从新标签页打开。");
      expect(document.querySelector("iframe")).toBeNull();
      expect(fallback.querySelector("a")).toHaveAttribute("href", url);
      expect(fallback.querySelector("a")).toHaveAttribute("target", "_blank");
      expect(fallback.querySelector("a")).toHaveAttribute("rel", "noopener noreferrer");
      fireEvent.click(screen.getByRole("button", { name: "刷新页面" }));
      expect(document.querySelector("iframe")).toBeNull();
      view.unmount();
    }
  });
  it("stops a stalled navigation after 20 seconds without losing task input and permits explicit retry", async () => {
    renderWithI18n(app(client(), context, false), { locale: "zh-Hans" });
    await screen.findByRole("button", { name: "排期日历" });
    vi.useFakeTimers();
    try {
      fireEvent.change(screen.getByLabelText("任务草稿"), { target: { value: "保留草稿" } });
      fireEvent.click(screen.getByRole("button", { name: "排期日历" }));
      act(() => { vi.advanceTimersByTime(20_000); });
      expect(screen.getByRole("alert")).toHaveTextContent("页面加载超时");
      expect(screen.queryByTitle("排期日历")).toBeNull();
      expect(screen.getByLabelText("任务草稿")).toHaveValue("保留草稿");
      fireEvent.click(screen.getByRole("button", { name: "关闭 Web 面板" }));
      fireEvent.click(screen.getByRole("button", { name: "排期日历" }));
      expect(screen.queryByTitle("排期日历")).toBeNull();
      fireEvent.click(screen.getByRole("button", { name: "刷新页面" }));
      const retry = screen.getByTitle("排期日历");
      fireEvent.load(retry);
      act(() => { vi.advanceTimersByTime(30_000); });
      expect(screen.getByTitle("排期日历")).toBe(retry);
      expect(screen.queryByRole("alert")).toBeNull();
    } finally { vi.useRealTimers(); }
  });
  it("unmounts a repeatedly navigating frame, including while hidden, without touching the active page", async () => {
    renderWithI18n(app(client(), context, false), { locale: "zh-Hans" });
    fireEvent.click(await screen.findByRole("button", { name: "排期日历" }));
    const looping = screen.getByTitle("排期日历");
    fireEvent.load(looping);
    fireEvent.click(screen.getByRole("button", { name: "数据图表" }));
    const healthy = screen.getByTitle("数据图表");
    fireEvent.load(healthy);
    for (let index = 0; index < 4; index++) fireEvent.load(looping);
    expect(looping.isConnected).toBe(false);
    expect(screen.getByTitle("数据图表")).toBe(healthy);
    fireEvent.click(screen.getByRole("button", { name: "排期日历" }));
    expect(screen.getByRole("alert")).toHaveTextContent("页面反复跳转");
    expect(screen.queryByTitle("排期日历")).toBeNull();
  });
  it("does not stop ordinary page loads outside the rapid-navigation window", async () => {
    renderWithI18n(app(client(), context, false), { locale: "zh-Hans" });
    await screen.findByRole("button", { name: "排期日历" });
    vi.useFakeTimers();
    try {
      fireEvent.click(screen.getByRole("button", { name: "排期日历" }));
      const frame = screen.getByTitle("排期日历");
      for (let index = 0; index < 6; index++) {
        fireEvent.load(frame);
        act(() => { vi.advanceTimersByTime(6_000); });
      }
      expect(screen.getByTitle("排期日历")).toBe(frame);
      expect(screen.queryByRole("alert")).toBeNull();
    } finally { vi.useRealTimers(); }
  });
  it("lets the user stop an undetectable cross-origin blank page even after a load event", async () => {
    renderWithI18n(app(client(), context, false), { locale: "zh-Hans" });
    fireEvent.click(await screen.findByRole("button", { name: "排期日历" }));
    fireEvent.load(screen.getByTitle("排期日历"));
    fireEvent.click(screen.getByRole("button", { name: "页面无法显示？" }));
    expect(screen.getByRole("alert")).toHaveTextContent("这个页面无法在此嵌入，请在新标签页打开。");
    expect(screen.queryByTitle("排期日历")).toBeNull();
  });
  it("handles a frame error by removing the browsing context", async () => {
    renderWithI18n(app(client(), context, false), { locale: "zh-Hans" });
    fireEvent.click(await screen.findByRole("button", { name: "排期日历" }));
    fireEvent.error(screen.getByTitle("排期日历"));
    expect(screen.getByRole("alert")).toHaveTextContent("页面未能加载");
    expect(document.querySelector("iframe")).toBeNull();
  });
  it("only reacts to an enforced parent frame policy for this page's origin", async () => {
    renderWithI18n(app(client(), context, false), { locale: "zh-Hans" });
    fireEvent.click(await screen.findByRole("button", { name: "排期日历" }));
    const frame = screen.getByTitle("排期日历");
    const violation = (effectiveDirective: string, blockedURI: string, disposition = "enforce") => {
      const event = new Event("securitypolicyviolation");
      Object.defineProperties(event, { effectiveDirective: { value: effectiveDirective }, blockedURI: { value: blockedURI }, disposition: { value: disposition } });
      fireEvent(document, event);
    };
    violation("frame-src", first.url, "report");
    violation("script-src", first.url);
    violation("frame-src", "https://other.example.test/");
    expect(screen.getByTitle("排期日历")).toBe(frame);
    violation("frame-src", "https://calendar.example.test/");
    expect(frame.isConnected).toBe(false);
    expect(screen.getByRole("alert")).toHaveTextContent("由于该网站限制");
  });
  it("cancels a pending navigation watchdog when leaving its task", async () => {
    const queryClient = client();
    const view = renderWithI18n(app(queryClient), { locale: "zh-Hans" });
    await screen.findByRole("button", { name: "任务结果" });
    vi.useFakeTimers();
    try {
      const before = vi.getTimerCount();
      fireEvent.click(screen.getByRole("button", { name: "任务结果" }));
      expect(vi.getTimerCount()).toBeGreaterThan(before);
      view.rerender(app(queryClient, context, false));
      expect(screen.queryByTitle("任务结果")).toBeNull();
      act(() => { vi.advanceTimersByTime(30_000); });
      expect(screen.queryByRole("alert")).toBeNull();
    } finally { vi.useRealTimers(); }
  });
  it("loads only on click, keeps iframe instances on switch/close, and leaves the task usable", async () => {
    renderWithI18n(app(client()), { locale: "zh-Hans" });
    expect(document.querySelector("iframe")).toBeNull();
    fireEvent.click(await screen.findByRole("button", { name: "排期日历" }));
    const frame = await screen.findByTitle("排期日历");
    expect(frame).toHaveAttribute("src", first.url);
    expect(frame).toHaveAttribute("sandbox", RESOURCE_WEB_PANEL_SANDBOX);
    expect(frame).toHaveAttribute("referrerpolicy", "no-referrer");
    expect(frame.getAttribute("sandbox")).not.toContain("allow-top-navigation");
    expect(frame.getAttribute("sandbox")).not.toContain("allow-modals");
    expect(screen.queryByRole("dialog")).toBeNull();
    fireEvent.change(screen.getByLabelText("任务草稿"), { target: { value: "继续编辑" } });
    fireEvent.click(screen.getByRole("button", { name: "数据图表" }));
    expect(await screen.findByTitle("数据图表")).toHaveAttribute("src", second.url);
    expect(screen.getByTitle("排期日历")).toBe(frame);
    fireEvent.click(screen.getByRole("button", { name: "排期日历" }));
    expect(screen.getByTitle("排期日历")).toBe(frame);
    expect(screen.getByLabelText("任务草稿")).toHaveValue("继续编辑");
    const dock = screen.getByRole("complementary");
    fireEvent.keyDown(screen.getByRole("separator", { name: "调整 Web 面板宽度" }), { key: "ArrowLeft" });
    expect(dock.style.width).toBe("472px");
    expect(screen.getByTitle("排期日历")).toBe(frame);
    expect(screen.getByRole("link", { name: "新标签页打开" })).toHaveAttribute("href", first.url);
    fireEvent.click(screen.getByRole("button", { name: "关闭 Web 面板" }));
    expect(dock.style.display).toBe("none");
    expect(screen.getByTitle("排期日历")).toBe(frame);
    fireEvent.click(screen.getByRole("button", { name: "排期日历" }));
    expect(screen.getByTitle("排期日历")).toBe(frame);
  });
  it("releases task frames when leaving a task, but keeps workspace frames", async () => {
    const queryClient = client();
    const view = renderWithI18n(app(queryClient), { locale: "zh-Hans" });
    fireEvent.click(await screen.findByRole("button", { name: "排期日历" }));
    const workspaceFrame = await screen.findByTitle("排期日历");
    fireEvent.click(await screen.findByRole("button", { name: "任务结果" }));
    expect(await screen.findByTitle("任务结果")).toHaveAttribute("src", task.url);
    view.rerender(app(queryClient, context, false));
    await waitFor(() => expect(screen.queryByTitle("任务结果")).toBeNull());
    expect(screen.getByTitle("排期日历")).toBe(workspaceFrame);
    expect(store.getState().visited).toHaveLength(1);
  });
  it("does not carry open pages or width preferences to a different account", async () => {
    const queryClient = client();
    const view = renderWithI18n(app(queryClient), { locale: "zh-Hans" });
    fireEvent.click(await screen.findByRole("button", { name: "排期日历" }));
    await screen.findByTitle("排期日历");
    act(() => store.getState().setWidth(600));
    view.rerender(app(queryClient, { ...context, userId: "00000000-0000-4000-8000-000000000099" }));
    await waitFor(() => expect(document.querySelector("iframe")).toBeNull());
    expect(store.getState().width).toBe(440);
  });
  it("releases cached frames when a workspace ID changes even if the slug is reused", async () => {
    const queryClient = client();
    const view = renderWithI18n(app(queryClient, context, false), { locale: "zh-Hans" });
    fireEvent.click(await screen.findByRole("button", { name: "排期日历" }));
    const oldFrame = screen.getByTitle("排期日历");
    view.rerender(app(queryClient, { ...context, workspaceId: "00000000-0000-4000-8000-000000000099" }, false));
    await waitFor(() => expect(oldFrame.isConnected).toBe(false));
    expect(store.getState().visited).toEqual([]);
  });
  it("offers same-origin URLs as new-tab links without embedding the platform", async () => {
    api.listResourceWebPanels.mockResolvedValue({ panels: [{ ...first, url: `${window.location.origin}/acme/issues` }], canAdd: true, limit: 50 });
    renderWithI18n(app(client(), context, false), { locale: "zh-Hans" });
    fireEvent.click(await screen.findByRole("button", { name: "排期日历" }));
    expect(await screen.findByText("这个页面无法在此嵌入，请在新标签页打开。")).toBeInTheDocument();
    expect(document.querySelector("iframe")).toBeNull();
    for (const link of screen.getAllByRole("link", { name: "新标签页打开" })) expect(link).toHaveAttribute("target", "_blank");
  });
  it("hides management controls for other members' panels", async () => {
    api.listResourceWebPanels.mockResolvedValue({ panels: [{ ...first, canManage: false }], canAdd: true, limit: 50 });
    renderWithI18n(app(client(), context, false), { locale: "zh-Hans" });
    await screen.findByRole("button", { name: "排期日历" });
    expect(screen.queryByRole("button", { name: /修改面板|移除面板/ })).toBeNull();
    expect(screen.getByRole("button", { name: "添加面板" })).toBeEnabled();
  });
  it("preserves form input and does not close on a failed create", async () => {
    api.createResourceWebPanel.mockRejectedValue(new Error("server unavailable"));
    renderWithI18n(app(client(), context, false), { locale: "zh-Hans" });
    fireEvent.click(await screen.findByRole("button", { name: "添加面板" }));
    fireEvent.change(screen.getByLabelText("名称"), { target: { value: "新面板" } });
    fireEvent.change(screen.getByLabelText("链接"), { target: { value: "https://new.example.test/#/route" } });
    fireEvent.click(screen.getByRole("button", { name: "保存" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("无法保存面板");
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.getByLabelText("链接")).toHaveValue("https://new.example.test/#/route");
  });
  it("puts the required URL first and allows an empty page name in both scopes", async () => {
    for (const scope of ["workspace", "issue"] as const) {
      api.createResourceWebPanel.mockResolvedValue(first);
      const view = renderWithI18n(app(client(), context, scope === "issue"), { locale: "zh-Hans" });
      await waitFor(() => expect(screen.getAllByRole("button", { name: "添加面板" })).toHaveLength(scope === "issue" ? 2 : 1));
      const buttons = screen.getAllByRole("button", { name: "添加面板" });
      fireEvent.click(buttons[scope === "issue" ? 1 : 0]!);
      const url = screen.getByLabelText("链接");
      const name = screen.getByLabelText("名称");
      expect(url.compareDocumentPosition(name) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
      expect(url).toBeRequired();
      expect(name).not.toBeRequired();
      expect(name).toHaveAttribute("placeholder", "可选，留空则使用链接");
      expect(name).toHaveValue("");
      const href = "https://example.test/" + "x".repeat(180) + "?q=one#view";
      fireEvent.change(url, { target: { value: href } });
      fireEvent.click(screen.getByRole("button", { name: "保存" }));
      await waitFor(() => expect(api.createResourceWebPanel).toHaveBeenLastCalledWith({ scope, resourceId: scope === "issue" ? ids.issue : ids.workspace }, { name: "", url: href }, "acme"));
      await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
      view.unmount();
    }
  });
  it("keeps URL-derived names optional on edit and preserves custom names", async () => {
    const href = "https://example.test/" + "x".repeat(180) + "?q=one#view";
    api.listResourceWebPanels.mockResolvedValue({ panels: [{ ...first, name: href, url: href }], canAdd: true, limit: 50 });
    api.updateResourceWebPanel.mockResolvedValue(first);
    renderWithI18n(app(client(), context, false), { locale: "zh-Hans" });
    fireEvent.click(await screen.findByRole("button", { name: `修改面板 ${href}` }));
    expect(screen.getByLabelText("名称")).toHaveValue("");
    fireEvent.change(screen.getByLabelText("链接"), { target: { value: href + "-new" } });
    fireEvent.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(api.updateResourceWebPanel).toHaveBeenCalledWith(resource, first.id, { name: "", url: href + "-new" }, "acme"));
  });
  it("rejects oversized custom page names without discarding input", async () => {
    renderWithI18n(app(client(), context, false), { locale: "zh-Hans" });
    fireEvent.click(await screen.findByRole("button", { name: "添加面板" }));
    fireEvent.change(screen.getByLabelText("链接"), { target: { value: "https://example.test/" } });
    fireEvent.change(screen.getByLabelText("名称"), { target: { value: "字".repeat(121) } });
    fireEvent.click(screen.getByRole("button", { name: "保存" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("自定义页面名称不能超过 120 个字符。");
    expect(api.createResourceWebPanel).not.toHaveBeenCalled();
  });
  it("removes cached frames when a successful refetch removes the shared panel", async () => {
    const queryClient = client();
    renderWithI18n(app(queryClient, context, false), { locale: "zh-Hans" });
    fireEvent.click(await screen.findByRole("button", { name: "排期日历" }));
    await screen.findByTitle("排期日历");
    await act(async () => { queryClient.setQueryData(webPanelKeys.resource(context, resource), { panels: [], canAdd: true, limit: 50 }); });
    await waitFor(() => expect(screen.queryByTitle("排期日历")).toBeNull());
    expect(store.getState().activeKey).toBeNull();
  });
  it("discards cached frames after authorization is revoked", async () => {
    const queryClient = client();
    renderWithI18n(app(queryClient, context, false), { locale: "zh-Hans" });
    fireEvent.click(await screen.findByRole("button", { name: "排期日历" }));
    await screen.findByTitle("排期日历");
    api.listResourceWebPanels.mockRejectedValue(new ApiError("Forbidden", 403, "Forbidden"));
    await act(async () => { await queryClient.invalidateQueries({ queryKey: webPanelKeys.resource(context, resource) }); });
    await waitFor(() => expect(screen.queryByTitle("排期日历")).toBeNull());
    expect(store.getState().visited).toHaveLength(0);
    expect(screen.queryByRole("button", { name: "排期日历" })).toBeNull();
  });
  it("retains a valid form during a transient server failure", async () => {
    const queryClient = client();
    renderWithI18n(app(queryClient, context, false), { locale: "zh-Hans" });
    fireEvent.click(await screen.findByRole("button", { name: "排期日历" }));
    const frame = await screen.findByTitle("排期日历");
    api.listResourceWebPanels.mockRejectedValue(new ApiError("Unavailable", 503, "Unavailable"));
    await act(async () => { await queryClient.invalidateQueries({ queryKey: webPanelKeys.resource(context, resource) }); });
    expect(screen.getByTitle("排期日历")).toBe(frame);
    expect(store.getState().visited).toHaveLength(1);
  });
});
