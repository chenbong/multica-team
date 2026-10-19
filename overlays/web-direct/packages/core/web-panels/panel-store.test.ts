// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { StorageAdapter } from "../types/storage";
import { createWebPanelStore, webPanelReferenceKey } from "./panel-store";
import { webPanelKeys } from "./queries";
import { webPanelUrl, canEmbedWebPanel } from "./url";
import { clearAllWorkspaceStorage, clearWorkspaceStorage } from "../platform/storage-cleanup";

function memoryStorage(): StorageAdapter {
  const items = new Map<string, string>();
  return { getItem: (key) => items.get(key) ?? null, setItem: (key, value) => { items.set(key, value); }, removeItem: (key) => { items.delete(key); }, keys: () => [...items.keys()] };
}

describe("resource web panel client state", () => {
  const issue = { scope: "issue" as const, resourceId: "issue-a", panelId: "panel-a" };
  const workspace = { scope: "workspace" as const, resourceId: "workspace-a", panelId: "panel-b" };
  it("keeps visited pointers on switch/close and releases issue pointers on leave", () => {
    const store = createWebPanelStore("alice", "acme", memoryStorage());
    store.getState().enterIssue("view-a", issue.resourceId);
    store.getState().open(issue);
    store.getState().open(workspace);
    expect(store.getState().visited).toEqual([issue, workspace]);
    store.getState().open(issue);
    expect(store.getState().activeKey).toBe(webPanelReferenceKey(issue));
    store.getState().close();
    expect(store.getState().visited).toHaveLength(2);
    store.getState().leaveIssue("view-a");
    expect(store.getState().visited).toEqual([workspace]);
    store.getState().open(issue);
    expect(store.getState().activeKey).toBeNull();
  });
  it("does not discard a still-mounted task when a nested view closes", () => {
    const store = createWebPanelStore("alice", "acme", memoryStorage());
    store.getState().enterIssue("outer", "issue-a");
    store.getState().enterIssue("nested", "issue-a");
    store.getState().open(issue);
    store.getState().leaveIssue("nested");
    expect(store.getState().visited).toHaveLength(1);
    store.getState().leaveIssue("outer");
    expect(store.getState().visited).toHaveLength(0);
    expect(store.getState().activeKey).toBeNull();
  });
  it("persists only an account/workspace-specific width, never URLs or selection", () => {
    const storage = memoryStorage();
    const alice = createWebPanelStore("alice", "acme", storage);
    alice.getState().setWidth(612);
    alice.getState().open(workspace);
    const restored = createWebPanelStore("alice", "acme", storage);
    expect(restored.getState().width).toBe(612);
    expect(restored.getState().visited).toEqual([]);
    expect(restored.getState().activeKey).toBeNull();
    expect(createWebPanelStore("bob", "acme", storage).getState().width).toBe(440);
    expect(createWebPanelStore("alice", "other", storage).getState().width).toBe(440);
    expect(storage.keys?.().map((key) => storage.getItem(key))).toEqual(["612"]);
  });
  it("forgets a removed panel and handles unavailable preference storage", () => {
    const storage: StorageAdapter = { getItem: () => { throw new Error("unavailable"); }, setItem: () => { throw new Error("unavailable"); }, removeItem: () => {} };
    const store = createWebPanelStore("alice", "acme", storage);
    store.getState().setWidth(Infinity);
    expect(store.getState().width).toBe(440);
    store.getState().open(workspace);
    store.getState().forget(workspace.panelId);
    expect(store.getState().visited).toEqual([]);
    expect(store.getState().activeKey).toBeNull();
  });
  it("isolates server query keys by account, workspace, resource type and id", () => {
    const context = { userId: "alice", workspaceId: "ws-a", workspaceSlug: "a" };
    const keys = [
      webPanelKeys.resource(context, workspace),
      webPanelKeys.resource({ ...context, userId: "bob" }, workspace),
      webPanelKeys.resource({ ...context, workspaceId: "ws-b" }, workspace),
      webPanelKeys.resource(context, { ...workspace, scope: "issue" }),
      webPanelKeys.resource(context, { ...workspace, resourceId: "other" }),
    ];
    expect(new Set(keys.map((key) => JSON.stringify(key))).size).toBe(5);
  });
  it("clears account-scoped widths on workspace removal and logout", () => {
    const storage = memoryStorage();
    for (const user of ["alice", "bob"]) createWebPanelStore(user, "acme", storage).getState().setWidth(612);
    createWebPanelStore("alice", "other", storage).getState().setWidth(700);
    clearWorkspaceStorage(storage, "acme");
    expect(createWebPanelStore("alice", "acme", storage).getState().width).toBe(440);
    expect(createWebPanelStore("bob", "acme", storage).getState().width).toBe(440);
    expect(createWebPanelStore("alice", "other", storage).getState().width).toBe(700);
    expect(clearAllWorkspaceStorage(storage)).toBe(true);
    expect(storage.keys?.()).toEqual([]);
  });
});

describe("resource web panel navigation", () => {
  it("preserves hash-router routes, query values and supplied navigation bytes", () => {
    const url = "https://pages.example.test/#/board?team=a";
    expect(webPanelUrl(url)).toBe(url);
    expect(webPanelUrl("http://127.0.0.1:1234/path?q=hello%20world#part")).toBe("http://127.0.0.1:1234/path?q=hello%20world#part");
  });
  it("rejects unsafe schemes, credentials and control characters", () => {
    for (const url of ["javascript:alert(1)", "data:text/html,page", "file:///tmp/a", "https://user:pass@example.test/", "https://example.test/a b", "https://example.test/a\nb", "https://example.test/" + "字".repeat(2000)]) expect(webPanelUrl(url)).toBeNull();
  });
  it("keeps same-origin and mixed-content URLs as links but not frames", () => {
    expect(canEmbedWebPanel("https://app.example.test/other", "https://app.example.test")).toBe(false);
    expect(canEmbedWebPanel("https://app.example.test:9000/other", "https://app.example.test")).toBe(false);
    expect(canEmbedWebPanel("http://pages.example.test/", "https://app.example.test")).toBe(false);
    expect(canEmbedWebPanel("https://pages.example.test/", "https://app.example.test")).toBe(true);
  });
});
