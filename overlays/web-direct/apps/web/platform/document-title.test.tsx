// @vitest-environment jsdom
/**
 * MUL-6222 — the web half of the tab-title contract.
 *
 * What a URL is *named* is resolved by `@multica/core/paths` +
 * `useTabPresentation` and tested there; mocked out here so these cover only
 * the platform wiring web owns: the site suffix, the unknown-route and
 * navigation behavior of `document.title`.
 */
import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { act, cleanup, render, waitFor } from "@testing-library/react";

const route = vi.hoisted(() => ({ pathname: "/acme/issues", search: "" }));
vi.mock("next/navigation", () => ({
  usePathname: () => route.pathname,
  useSearchParams: () => new URLSearchParams(route.search),
}));

const presentation = vi.hoisted(() => ({
  title: "Issues",
  urls: [] as string[],
}));
vi.mock("@multica/views/layout", () => ({
  useTabPresentation: (url: string) => {
    presentation.urls.push(url);
    return { title: presentation.title, visual: { kind: "icon", icon: "ListTodo" } };
  },
}));

import {
  MAX_PAGE_TITLE_LENGTH,
  SITE_TITLE,
  TITLE_SUFFIX,
  formatDocumentTitle,
} from "./document-title";
import { WorkspaceDocumentTitle } from "./workspace-document-title";

function open(pathname: string, search = "") {
  route.pathname = pathname;
  route.search = search;
}

beforeEach(() => {
  presentation.urls = [];
  presentation.title = "Issues";
  document.title = SITE_TITLE;
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const originalERAD20Title =
  "ERNIE-5.5-Flash 2bit 部署  算法策略&推理性能 优化";

async function flushTitleMutations() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}

function observeTitleWork() {
  const NativeObserver = globalThis.MutationObserver;
  const callbacks = vi.fn();
  const writes = vi.spyOn(document, "title", "set");
  vi.stubGlobal(
    "MutationObserver",
    class extends NativeObserver {
      constructor(callback: MutationCallback) {
        super((records, observer) => {
          callbacks();
          // Bound a regressed observer loop so the regression itself cannot
          // starve the event loop or exhaust the test runner's memory.
          if (callbacks.mock.calls.length > 20) {
            observer.disconnect();
            return;
          }
          callback(records, observer);
        });
      }
    },
  );
  return { callbacks, writes };
}

describe("formatDocumentTitle", () => {
  it.each(["  ", "\t", "\n", "\r", "\f", " \t\n\r\f "])(
    "collapses HTML ASCII whitespace %j before formatting",
    (whitespace) => {
      expect(formatDocumentTitle(`${whitespace}ERAD-20:${whitespace}部署${whitespace}优化${whitespace}`)).toBe(
        `ERAD-20: 部署 优化${TITLE_SUFFIX}`,
      );
    },
  );

  it("preserves non-ASCII whitespace and vertical tab, including title edges", () => {
    for (const whitespace of ["\u00a0", "\u3000", "\v"]) {
      const title = `${whitespace}部署${whitespace}${whitespace}优化${whitespace}`;
      expect(formatDocumentTitle(title)).toBe(`${title}${TITLE_SUFFIX}`);
    }
  });

  it("normalizes ERAD-20 only for display without changing its original name", () => {
    expect(formatDocumentTitle(`ERAD-20: ${originalERAD20Title}`)).toBe(
      `ERAD-20: ERNIE-5.5-Flash 2bit 部署 算法策略&推理性能 优化${TITLE_SUFFIX}`,
    );
    expect(originalERAD20Title).toContain("部署  算法");
  });

  it("does not remove a non-breaking space at the clipping boundary", () => {
    const prefix = "x".repeat(MAX_PAGE_TITLE_LENGTH - 1) + "\u00a0";
    expect(formatDocumentTitle(prefix + "more")).toBe(`${prefix}…${TITLE_SUFFIX}`);
  });
  it("puts the page name in front of the product name", () => {
    expect(formatDocumentTitle("MUL-123: Fix login")).toBe(
      `MUL-123: Fix login${TITLE_SUFFIX}`,
    );
  });

  it("falls back to the site title rather than a bare suffix", () => {
    expect(formatDocumentTitle("")).toBe(SITE_TITLE);
    expect(formatDocumentTitle("   ")).toBe(SITE_TITLE);
    expect(formatDocumentTitle(null)).toBe(SITE_TITLE);
    expect(formatDocumentTitle(undefined)).toBe(SITE_TITLE);
  });

  it("clips an over-long title and keeps the identifying prefix", () => {
    const title = `MUL-123: ${"long ".repeat(60)}`;
    const formatted = formatDocumentTitle(title);

    expect(formatted.startsWith("MUL-123: long")).toBe(true);
    expect(formatted.endsWith(`…${TITLE_SUFFIX}`)).toBe(true);
    // Ellipsis replaces the clipped remainder, and no trailing space survives.
    expect(formatted).not.toContain(" … ");
  });

  it("clips on code points so an emoji is never cut in half", () => {
    const formatted = formatDocumentTitle("🎯".repeat(MAX_PAGE_TITLE_LENGTH + 10));
    const pageTitle = formatted.slice(0, formatted.indexOf(TITLE_SUFFIX));

    expect(Array.from(pageTitle)).toHaveLength(MAX_PAGE_TITLE_LENGTH + 1);
    expect(pageTitle).not.toContain("�");
    expect(pageTitle.endsWith("🎯…")).toBe(true);
  });

  it("leaves a title at the limit untouched", () => {
    const exact = "x".repeat(MAX_PAGE_TITLE_LENGTH);
    expect(formatDocumentTitle(exact)).toBe(`${exact}${TITLE_SUFFIX}`);
  });
});

describe("bounded title synchronization", () => {
  it.each(["  ", "\t", "\n", "\r", "\f", " \t\r\n\f ", "\u00a0\u00a0"])(
    "does not observe its own writes after late metadata with %j",
    async (whitespace) => {
      const probe = observeTitleWork();
      open("/era-duandaoduan/issues/ERAD-20");
      presentation.title = `ERAD-20: 部署${whitespace}算法策略&推理性能 优化`;
      render(<WorkspaceDocumentTitle />);
      await flushTitleMutations();
      expect(probe.callbacks).not.toHaveBeenCalled();

      document.title = SITE_TITLE;
      await flushTitleMutations();
      const expected = whitespace.includes("\u00a0")
        ? `ERAD-20: 部署${whitespace}算法策略&推理性能 优化${TITLE_SUFFIX}`
        : `ERAD-20: 部署 算法策略&推理性能 优化${TITLE_SUFFIX}`;
      expect(document.title).toBe(expected);
      expect(document.head.querySelector("title")?.textContent).toBe(expected);
      expect(probe.callbacks.mock.calls.length).toBeLessThanOrEqual(1);
      expect(probe.writes.mock.calls.length).toBeLessThanOrEqual(3);

      const callbacks = probe.callbacks.mock.calls.length;
      const writes = probe.writes.mock.calls.length;
      await flushTitleMutations();
      expect(probe.callbacks).toHaveBeenCalledTimes(callbacks);
      expect(probe.writes).toHaveBeenCalledTimes(writes);
    },
  );

  it("keeps ERAD-20 stable across navigation, renaming and title-node replacement", async () => {
    const probe = observeTitleWork();
    open("/era-duandaoduan/issues/ERAD-20");
    presentation.title = `ERAD-20: ${originalERAD20Title}`;
    const view = render(<WorkspaceDocumentTitle />);
    await flushTitleMutations();

    open("/era-duandaoduan/issues/ERAD-21");
    presentation.title = "ERAD-21: 新任务\t继续";
    view.rerender(<WorkspaceDocumentTitle />);
    const metadata = document.createElement("title");
    metadata.textContent = SITE_TITLE;
    document.head.querySelector("title")?.replaceWith(metadata);
    await flushTitleMutations();
    expect(document.title).toBe(`ERAD-21: 新任务 继续${TITLE_SUFFIX}`);

    presentation.title = "ERAD-21: 已  改名\n保留\u00a0空格";
    view.rerender(<WorkspaceDocumentTitle />);
    await flushTitleMutations();
    expect(document.title).toBe(`ERAD-21: 已 改名 保留\u00a0空格${TITLE_SUFFIX}`);
    expect(probe.callbacks.mock.calls.length).toBeLessThanOrEqual(1);
    expect(probe.writes.mock.calls.length).toBeLessThanOrEqual(4);

    view.unmount();
    const callbacks = probe.callbacks.mock.calls.length;
    document.title = "Login";
    await flushTitleMutations();
    expect(document.title).toBe("Login");
    expect(probe.callbacks).toHaveBeenCalledTimes(callbacks);
  });
});

describe("WorkspaceDocumentTitle", () => {
  it("names the tab after the open issue", () => {
    open("/acme/issues/MUL-123");
    presentation.title = "MUL-123: Fix login";

    render(<WorkspaceDocumentTitle />);

    expect(document.title).toBe(`MUL-123: Fix login${TITLE_SUFFIX}`);
  });

  it("resolves against the full URL so a container's selection titles the tab", () => {
    open("/acme/inbox", "issue=abc&view=archived");
    presentation.title = "MUL-9: Ping";

    render(<WorkspaceDocumentTitle />);

    expect(presentation.urls).toContain("/acme/inbox?issue=abc&view=archived");
    expect(document.title).toBe(`MUL-9: Ping${TITLE_SUFFIX}`);
  });

  it("keeps the site title on an unrecognized route", () => {
    open("/acme/not-a-route");
    presentation.title = "Unknown page";

    render(<WorkspaceDocumentTitle />);

    expect(document.title).toBe(SITE_TITLE);
  });

  it("restores the site title when the dashboard unmounts", () => {
    open("/acme/projects/p1");
    presentation.title = "Website redesign";

    const view = render(<WorkspaceDocumentTitle />);
    expect(document.title).toBe(`Website redesign${TITLE_SUFFIX}`);

    view.unmount();
    expect(document.title).toBe(SITE_TITLE);
  });

  it("re-asserts the title when a navigation keeps the same page name", () => {
    open("/acme/inbox");
    presentation.title = "Inbox";
    const view = render(<WorkspaceDocumentTitle />);
    expect(document.title).toBe(`Inbox${TITLE_SUFFIX}`);

    // A route change re-renders the target's metadata, resetting the title to
    // the root default before our effect gets to run again.
    document.title = SITE_TITLE;
    open("/acme/inbox", "view=archived");
    view.rerender(<WorkspaceDocumentTitle />);

    expect(document.title).toBe(`Inbox${TITLE_SUFFIX}`);
  });
});
describe("late route metadata", () => {
 it("restores the issue title after Next metadata arrives late and stops on unmount", async () => {
  open("/acme/issues/MUL-123"); presentation.title="MUL-123: Fix login";
  const view=render(<WorkspaceDocumentTitle />);
  document.title=SITE_TITLE;
  await waitFor(()=>expect(document.title).toBe(formatDocumentTitle(presentation.title)));
  open("/acme/issues/MUL-124"); presentation.title="MUL-124: Next issue";
  view.rerender(<WorkspaceDocumentTitle />);
  document.title=SITE_TITLE;
  await waitFor(()=>expect(document.title).toBe(formatDocumentTitle("MUL-124: Next issue")));
  view.unmount();
  document.title="Login";
  await Promise.resolve();
  expect(document.title).toBe("Login");
 });
});
