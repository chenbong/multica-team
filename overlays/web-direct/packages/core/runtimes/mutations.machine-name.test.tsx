// @vitest-environment jsdom
import { act, cleanup, renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setApiInstance } from "../api";
import type { ApiClient } from "../api/client";
import { runtimeKeys } from "./queries";
import { useUpdateRuntime } from "./mutations";

describe("machine rename across cached workspaces", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it.each(["Shared computer", ""])("invalidates inactive workspace names after saving %j", async (name) => {
    const client = new QueryClient({ defaultOptions: { queries: { staleTime: Infinity, retry: false } } });
    client.setQueryData(runtimeKeys.list("a"), [{ id: "a-runtime", custom_name: "old" }]);
    client.setQueryData(runtimeKeys.list("b"), [{ id: "b-runtime", custom_name: "old" }]);
    client.setQueryData(runtimeKeys.listMine("b"), [{ id: "b-runtime", custom_name: "old" }]);
    client.setQueryData(["unrelated"], "unchanged");
    setApiInstance({ updateRuntime: vi.fn().mockResolvedValue({ id: "a-runtime", custom_name: name || null }) } as unknown as ApiClient);
    const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
    const { result } = renderHook(() => useUpdateRuntime("a"), { wrapper });
    await act(() => result.current.mutateAsync({ runtimeId: "a-runtime", patch: { custom_name: name, apply_to_machine: true } }));
    expect(client.getQueryState(runtimeKeys.list("a"))?.isInvalidated).toBe(true);
    expect(client.getQueryState(runtimeKeys.list("b"))?.isInvalidated).toBe(true);
    expect(client.getQueryState(runtimeKeys.listMine("b"))?.isInvalidated).toBe(true);
    expect(client.getQueryState(["unrelated"])?.isInvalidated).toBe(false);
    client.clear();
  });

  it.each([{ visibility: "public" as const }])("keeps a visibility edit scoped to its workspace", async (patch) => {
    const client = new QueryClient();
    client.setQueryData(runtimeKeys.list("a"), []);
    client.setQueryData(runtimeKeys.list("b"), []);
    setApiInstance({ updateRuntime: vi.fn().mockResolvedValue({ id: "a-runtime" }) } as unknown as ApiClient);
    const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
    const { result } = renderHook(() => useUpdateRuntime("a"), { wrapper });
    await act(() => result.current.mutateAsync({ runtimeId: "a-runtime", patch }));
    expect(client.getQueryState(runtimeKeys.list("a"))?.isInvalidated).toBe(true);
    expect(client.getQueryState(runtimeKeys.list("b"))?.isInvalidated).toBe(false);
    client.clear();
  });
});
