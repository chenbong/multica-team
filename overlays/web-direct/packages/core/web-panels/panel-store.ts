import { createStore } from "zustand/vanilla";
import type { StorageAdapter } from "../types/storage";
import type { WebPanelReference } from "./types";

export const WEB_PANEL_DEFAULT_WIDTH = 440;
export const clampWebPanelWidth = (width: number) => Math.round(Math.max(280, Math.min(1000, Number.isFinite(width) ? width : WEB_PANEL_DEFAULT_WIDTH)));
export const webPanelReferenceKey = (ref: WebPanelReference) => `${ref.scope}:${ref.resourceId}:${ref.panelId}`;

export interface WebPanelState {
  activeKey: string | null;
  visited: WebPanelReference[];
  issueContexts: { token: string; issueId: string }[];
  width: number;
  open: (reference: WebPanelReference) => void;
  close: () => void;
  forget: (panelId: string) => void;
  setWidth: (width: number) => void;
  enterIssue: (token: string, issueId: string) => void;
  leaveIssue: (token: string) => void;
}

/** Only client-owned pointers live here; names/URLs and permissions stay in Query. */
export function createWebPanelStore(userId: string, workspaceSlug: string, storage: StorageAdapter) {
  const preferenceKey = `multica_resource_web_panels:${encodeURIComponent(userId)}:${encodeURIComponent(workspaceSlug)}`;
  let width = WEB_PANEL_DEFAULT_WIDTH;
  try {
    const saved = storage.getItem(preferenceKey);
    if (saved !== null) width = clampWebPanelWidth(Number(saved));
  } catch { /* Unavailable preference storage must not block a panel. */ }
  return createStore<WebPanelState>((set) => ({
    activeKey: null, visited: [], issueContexts: [], width,
    open: (ref) => set((state) => {
      const key = webPanelReferenceKey(ref);
      if (ref.scope === "issue" && !state.issueContexts.some((context) => context.issueId === ref.resourceId)) return state;
      return { activeKey: key, visited: state.visited.some((item) => webPanelReferenceKey(item) === key) ? state.visited : [...state.visited, ref] };
    }),
    // Closing the dock does not discard an external form or its scroll state.
    close: () => set({ activeKey: null }),
    forget: (panelId) => set((state) => ({
      visited: state.visited.filter((ref) => ref.panelId !== panelId),
      activeKey: state.visited.find((ref) => ref.panelId === panelId && webPanelReferenceKey(ref) === state.activeKey) ? null : state.activeKey,
    })),
    setWidth: (next) => {
      const value = clampWebPanelWidth(next);
      try { storage.setItem(preferenceKey, String(value)); } catch { /* Keep the in-memory preference. */ }
      set({ width: value });
    },
    enterIssue: (token, issueId) => set((state) => ({ issueContexts: [...state.issueContexts.filter((item) => item.token !== token), { token, issueId }] })),
    leaveIssue: (token) => set((state) => {
      const contexts = state.issueContexts.filter((item) => item.token !== token);
      const present = new Set(contexts.map((item) => item.issueId));
      const visited = state.visited.filter((ref) => ref.scope !== "issue" || present.has(ref.resourceId));
      return { issueContexts: contexts, visited, activeKey: visited.some((ref) => webPanelReferenceKey(ref) === state.activeKey) ? state.activeKey : null };
    }),
  }));
}

export type WebPanelStore = ReturnType<typeof createWebPanelStore>;
