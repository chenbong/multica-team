import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, render } from "@testing-library/react";
import { useLayoutEffect } from "react";
import { ResourceWebPanelStateProvider, useResourceWebPanelContext, type WebPanelStore } from "@multica/core/web-panels";
import { useWebPanelInspector } from "./use-web-panel-inspector";

let store: WebPanelStore;
let callbacks: ReturnType<typeof useWebPanelInspector>;
let width = 365;
const panel = { getSize: () => ({ inPixels: width, asPercentage: 30 }), isCollapsed: () => false, collapse: vi.fn(), expand: vi.fn(), resize: vi.fn() };
const panelRef = { current: panel };
const onResize = vi.fn();
const onLayoutChanged = vi.fn();
const ref = { scope: "workspace" as const, resourceId: "ws", panelId: "first" };
const frames = new Map<number, FrameRequestCallback>();
let sequence = 0;
function Harness({ alreadyOpen = false, initiallyOpen = true }: { alreadyOpen?: boolean; initiallyOpen?: boolean }) {
  store = useResourceWebPanelContext()!.store;
  useLayoutEffect(() => { if (alreadyOpen) store.getState().open(ref); }, [alreadyOpen]);
  callbacks = useWebPanelInspector({ panelRef, disabled: false, initiallyOpen, onResize, onLayoutChanged });
  return null;
}
function app(alreadyOpen = false, initiallyOpen = true) {
  return <ResourceWebPanelStateProvider context={{ userId: "user", workspaceId: "ws", workspaceSlug: "acme" }}><Harness alreadyOpen={alreadyOpen} initiallyOpen={initiallyOpen} /></ResourceWebPanelStateProvider>;
}
function flushFrame() {
  act(() => { const pending = [...frames.values()]; frames.clear(); pending.forEach((callback) => callback(0)); });
}
beforeEach(() => {
  vi.clearAllMocks(); width = 365; frames.clear(); sequence = 0;
  vi.spyOn(window, "requestAnimationFrame").mockImplementation((callback) => { frames.set(++sequence, callback); return sequence; });
  vi.spyOn(window, "cancelAnimationFrame").mockImplementation((id) => { frames.delete(id); });
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

describe("temporary Web panel inspector layout", () => {
  it("restores the prior pixel width and does not save temporary resize events", () => {
    render(app());
    act(() => store.getState().open(ref));
    width = 0;
    callbacks.onResize({ inPixels: 0, asPercentage: 0 });
    callbacks.onLayoutChanged({ content: 100, sidebar: 0 });
    expect(onResize).not.toHaveBeenCalled();
    expect(onLayoutChanged).not.toHaveBeenCalled();
    act(() => store.getState().close());
    flushFrame(); flushFrame();
    expect(panel.resize).toHaveBeenCalledWith("365px");
    callbacks.onResize({ inPixels: 365, asPercentage: 30 });
    callbacks.onLayoutChanged({ content: 70, sidebar: 30 });
    expect(onResize).toHaveBeenCalledOnce();
    expect(onLayoutChanged).toHaveBeenCalledOnce();
  });
  it("does not recapture a hidden inspector when switching frames", () => {
    render(app());
    act(() => store.getState().open(ref));
    width = 0;
    act(() => store.getState().open({ ...ref, panelId: "second" }));
    act(() => store.getState().close());
    flushFrame(); flushFrame();
    expect(panel.resize).toHaveBeenCalledWith("365px");
  });
  it("cancels an obsolete restoration when the dock is immediately reopened", () => {
    render(app());
    act(() => { store.getState().open(ref); store.getState().close(); });
    flushFrame();
    act(() => store.getState().open(ref));
    flushFrame();
    expect(panel.resize).not.toHaveBeenCalled();
    act(() => store.getState().close());
    flushFrame(); flushFrame();
    expect(panel.resize).toHaveBeenCalledWith("365px");
  });
  it("restores the initial closed preference when entering with a workspace frame open", () => {
    render(app(true, false));
    act(() => store.getState().close());
    flushFrame(); flushFrame();
    expect(panel.collapse).toHaveBeenCalledOnce();
    expect(panel.resize).not.toHaveBeenCalled();
  });
  it("cancels pending layout writes when the task unmounts", () => {
    const view = render(app());
    act(() => { store.getState().open(ref); store.getState().close(); });
    view.unmount();
    flushFrame(); flushFrame();
    expect(panel.resize).not.toHaveBeenCalled();
    expect(frames.size).toBe(0);
  });
});
