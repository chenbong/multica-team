import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, screen } from "@testing-library/react";
import { renderWithI18n } from "../../test/i18n";
import { IssueJumpToLatest } from "./issue-jump-to-latest";

let resizeCallbacks: Array<() => void>;
let scroller: HTMLDivElement;
let height: number;
let viewport: EventTarget & { height: number };
const composerRef = { current: null as HTMLDivElement | null };

function renderJump(props = {}) {
  const onPrepareJump = vi.fn();
  const view = renderWithI18n(<IssueJumpToLatest scrollContainerEl={scroller} composerRef={composerRef}
    stickyComposer={false} onPrepareJump={onPrepareJump} {...props} />);
  return { ...view, onPrepareJump };
}
function frame() { act(() => vi.advanceTimersByTime(32)); }

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["Date", "requestAnimationFrame", "cancelAnimationFrame"] });
  resizeCallbacks = [];
  vi.stubGlobal("ResizeObserver", class {
    constructor(callback: () => void) { resizeCallbacks.push(callback); }
    observe() {}
    disconnect() {}
  });
  viewport = Object.assign(new EventTarget(), { height: window.innerHeight });
  vi.stubGlobal("visualViewport", viewport);
  height = 8000;
  scroller = document.createElement("div");
  scroller.append(document.createElement("div"));
  Object.defineProperties(scroller, {
    scrollHeight: { get: () => height },
    clientHeight: { value: 600 },
  });
  document.body.append(scroller);
  composerRef.current = document.createElement("div");
  vi.spyOn(composerRef.current, "getBoundingClientRect").mockReturnValue({ height: 180 } as DOMRect);
});
afterEach(() => {
  cleanup();
  scroller.remove();
  composerRef.current = null;
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("IssueJumpToLatest", () => {
  it("shows a labelled touch target without changing the reading position", () => {
    scroller.scrollTop = 1200;
    renderJump();
    const button = screen.getByRole("button", { name: "Latest replies" });
    expect(button).toHaveClass("min-h-11", "absolute");
    expect(button.textContent).toBe("");
    expect(button.style.bottom).toContain("safe-area-inset-bottom");
    expect(scroller.scrollTop).toBe(1200);
  });
  it("hides on short pages and near the bottom", () => {
    height = 600;
    renderJump();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    height = 8000;
    act(() => resizeCallbacks.forEach(callback => callback())); frame();
    expect(screen.getByRole("button")).toBeInTheDocument();
    scroller.scrollTop = 7350;
    fireEvent.scroll(scroller); frame();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });
  it("jumps instantly, materializes the last virtual row, and settles late layout", () => {
    const { onPrepareJump } = renderJump();
    fireEvent.click(screen.getByRole("button"));
    expect(onPrepareJump).toHaveBeenCalledOnce();
    expect(scroller.scrollTop).toBe(7400);
    height = 16000; frame();
    expect(scroller.scrollTop).toBe(15400);
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    act(() => vi.advanceTimersByTime(2100));
    scroller.scrollTop = 4000;
    height = 18000;
    act(() => resizeCallbacks.forEach(callback => callback())); frame();
    expect(scroller.scrollTop).toBe(4000);
    expect(screen.getByRole("button")).toBeInTheDocument();
  });
  it.each(["wheel", "touchstart", "pointerdown", "keydown"])("cancels settling as soon as the user sends %s", event => {
    renderJump();
    fireEvent.click(screen.getByRole("button"));
    fireEvent(scroller, new Event(event, { bubbles: true }));
    scroller.scrollTop = 1000;
    height = 12000; frame();
    expect(scroller.scrollTop).toBe(1000);
  });
  it("does not follow incoming replies while the reader is in history", () => {
    renderJump();
    height = 12000;
    act(() => resizeCallbacks.forEach(callback => callback())); frame();
    expect(scroller.scrollTop).toBe(0);
    expect(screen.getByRole("button")).toBeInTheDocument();
  });
  it("reserves pinned composer height without focusing the editor", () => {
    renderJump({ stickyComposer: true });
    const button = screen.getByRole("button");
    expect(button.style.bottom).toContain("180px");
    fireEvent.click(button);
    expect(document.activeElement).not.toBe(composerRef.current);
  });
  it("hides while the mobile keyboard is open and restores after it closes", () => {
    renderJump();
    const input = document.createElement("textarea");
    scroller.append(input);
    input.focus();
    viewport.height = window.innerHeight - 300;
    viewport.dispatchEvent(new Event("resize")); frame();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    expect(document.activeElement).toBe(input);
    viewport.height = window.innerHeight;
    viewport.dispatchEvent(new Event("resize")); frame();
    expect(screen.getByRole("button")).toBeInTheDocument();
  });
  it("cancels pending frames on unmount", () => {
    const view = renderJump();
    fireEvent.click(screen.getByRole("button"));
    view.unmount();
    scroller.scrollTop = 200;
    frame();
    expect(scroller.scrollTop).toBe(200);
  });
  it("renders the agreed Chinese label", () => {
    renderWithI18n(<IssueJumpToLatest scrollContainerEl={scroller} composerRef={composerRef}
      stickyComposer={false} onPrepareJump={vi.fn()} />, { locale: "zh-Hans" });
    expect(screen.getByRole("button", { name: "最新对话" })).toBeInTheDocument();
  });
});
