"use client";

import { createContext, useContext, useLayoutEffect, useMemo, useId, type ReactNode } from "react";
import { useStore } from "zustand";
import { defaultStorage } from "../platform/storage";
import type { StorageAdapter } from "../types/storage";
import { createWebPanelStore, type WebPanelState, type WebPanelStore } from "./panel-store";
import type { WebPanelQueryContext } from "./types";

interface WebPanelContextValue { store: WebPanelStore; queryContext: WebPanelQueryContext }
const WebPanelContext = createContext<WebPanelContextValue | null>(null);
const unavailableStorage: StorageAdapter = { getItem: () => null, setItem: () => {}, removeItem: () => {} };
const emptyStore = createWebPanelStore("", "", unavailableStorage);

export function ResourceWebPanelStateProvider({ context, storage = defaultStorage, children }: {
  context: WebPanelQueryContext; storage?: StorageAdapter; children: ReactNode;
}) {
  const { userId, workspaceId, workspaceSlug } = context;
  const value = useMemo(() => ({
    store: createWebPanelStore(userId, workspaceSlug, storage),
    queryContext: { userId, workspaceId, workspaceSlug },
  }), [userId, workspaceId, workspaceSlug, storage]);
  return <WebPanelContext.Provider value={value}>{children}</WebPanelContext.Provider>;
}

export const useResourceWebPanelContext = () => useContext(WebPanelContext);

export function useResourceWebPanelState<T>(selector: (state: WebPanelState) => T): T {
  const context = useResourceWebPanelContext();
  return useStore(context?.store ?? emptyStore, selector);
}

export const useResourceWebPanelOpen = () => useResourceWebPanelState((state) => state.activeKey !== null);

export function useResourceWebPanelIssueScope(issueId: string | null) {
  const store = useResourceWebPanelContext()?.store;
  const token = useId();
  useLayoutEffect(() => {
    if (!store || !issueId) return;
    store.getState().enterIssue(token, issueId);
    return () => store.getState().leaveIssue(token);
  }, [store, issueId, token]);
}
