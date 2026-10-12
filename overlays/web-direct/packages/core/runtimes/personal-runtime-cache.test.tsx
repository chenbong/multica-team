// @vitest-environment jsdom
import { createElement, type ReactNode } from "react";
import { act, renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { runtimeKeys, runtimeListOptions, runtimeUsageOptions, runtimeUsageByAgentOptions, runtimeUsageByHourOptions } from "./queries";
import { runtimeProfileKeys, runtimeProfileListOptions, useCreateRuntimeProfile, useUpdateRuntimeProfile, useDeleteRuntimeProfile } from "./profiles";
import { useUpdateRuntime, useDeleteRuntime, useUnbindAgentsAndDeleteRuntime } from "./mutations";

const mocks = vi.hoisted(() => ({ user: { id: "first" }, write: vi.fn() }));
vi.mock("../auth", () => ({ useAuthStore: { getState: () => ({ user: mocks.user }) } }));
vi.mock("../api", () => ({ ApiError: class extends Error {}, api: {
  listRuntimes: vi.fn(), listRuntimeProfiles: vi.fn(),
  updateRuntime: mocks.write, deleteRuntime: mocks.write, unbindAgentsAndDeleteRuntime: mocks.write,
  createRuntimeProfile: mocks.write, updateRuntimeProfile: mocks.write, deleteRuntimeProfile: mocks.write,
} }));

beforeEach(() => {
  mocks.user = { id: "first" };
  mocks.write.mockReset().mockResolvedValue(undefined);
});

describe("personal runtime caches with workspace aliases", () => {
  it("isolates lists, profiles, details and usage after an account switch", () => {
    const client = new QueryClient();
    const keys = () => [
      runtimeListOptions("ws-1").queryKey, runtimeListOptions("ws-1", "me").queryKey,
      runtimeProfileListOptions("ws-1").queryKey, runtimeProfileKeys.detail("ws-1", "profile"),
      runtimeUsageOptions("runtime", 7, "UTC").queryKey,
      runtimeUsageByAgentOptions("runtime", 7, "UTC").queryKey,
      runtimeUsageByHourOptions("runtime", 7, "UTC").queryKey,
    ];
    for (const key of keys()) client.setQueryData(key, ["first-account-only"]);
    mocks.user = { id: "second" };
    for (const key of keys()) expect(client.getQueryData(key)).toBeUndefined();
    expect(runtimeListOptions("ws-2").queryKey).not.toEqual(runtimeListOptions("ws-1").queryKey);
    expect(runtimeProfileListOptions("ws-2").queryKey).not.toEqual(runtimeProfileListOptions("ws-1").queryKey);
  });

  const cases: ReadonlyArray<readonly [string, () => () => Promise<unknown>]> = [
    ["rename runtime", () => { const m = useUpdateRuntime("ws-1"); return () => m.mutateAsync({ runtimeId: "runtime", patch: { custom_name: "Renamed" } }); }],
    ["delete runtime", () => { const m = useDeleteRuntime("ws-1"); return () => m.mutateAsync("runtime"); }],
    ["unbind and delete", () => { const m = useUnbindAgentsAndDeleteRuntime("ws-1"); return () => m.mutateAsync({ runtimeId: "runtime", expectedActiveAgentIds: ["agent"] }); }],
    ["create profile", () => { const m = useCreateRuntimeProfile("ws-1"); return () => m.mutateAsync({ display_name: "Mine", runtime_type: "codex", command_name: "codex", fixed_args: [] }); }],
    ["edit profile", () => { const m = useUpdateRuntimeProfile("ws-1"); return () => m.mutateAsync({ profileId: "profile", patch: { display_name: "Renamed" } }); }],
    ["delete profile", () => { const m = useDeleteRuntimeProfile("ws-1"); return () => m.mutateAsync("profile"); }],
  ] as const;

  it.each(cases)("refreshes all workspace projections after %s", async (_name, useAction) => {
    const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
    const keys = ["ws-1", "ws-2"].flatMap((workspace) => [runtimeKeys.list(workspace), runtimeProfileKeys.list(workspace)]);
    for (const key of keys) client.setQueryData(key, ["cached"]);
    const wrapper = ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
    const { result, unmount } = renderHook(useAction, { wrapper });
    await act(async () => { await result.current(); });
    for (const key of keys) expect(client.getQueryState(key)?.isInvalidated).toBe(true);
    unmount();
    client.clear();
  });
});
