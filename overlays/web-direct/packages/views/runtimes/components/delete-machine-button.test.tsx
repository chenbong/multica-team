import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import type { AgentRuntime } from "@multica/core/types";
import { DeleteMachineButton } from "./delete-machine-button";
import type { RuntimeMachine } from "./runtime-machines";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";

const mocks = vi.hoisted(() => ({ members: vi.fn(), agents: vi.fn(), remove: vi.fn() }));
vi.mock("@multica/core/api", () => ({ api: { listMembers: mocks.members, listAgents: mocks.agents, deleteOfflineMachine: mocks.remove } }));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "workspace-fixture" }));
vi.mock("@multica/core/auth", () => {
  const state = { user: { id: "owner-fixture" } };
  return { useAuthStore: Object.assign((selector: (s: typeof state) => unknown) => selector(state), { getState: () => state }) };
});
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

function machine(status = "offline", owner = "owner-fixture"): RuntimeMachine {
  return {
    id: "machine-fixture", daemonId: "daemon-fixture", title: "Fixture computer", subtitle: null, deviceInfo: null,
    cliVersion: null, launchedBy: null, mode: "local", section: "remote", isCurrent: false, health: "offline",
    runtimes: [{ id: "runtime-fixture", status, owner_id: owner } as AgentRuntime], onlineCount: status === "online" ? 1 : 0,
    issueCount: 0, runningCount: 0, queuedCount: 0, providerNames: [], lastSeenAt: null,
  };
}

function renderButton(value = machine()) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const result = render(<QueryClientProvider client={client}><I18nProvider locale="en" resources={{ en: { common: enCommon, runtimes: enRuntimes } }}><DeleteMachineButton machine={value} /></I18nProvider></QueryClientProvider>);
  return { ...result, client };
}

beforeEach(() => {
  mocks.members.mockReset().mockResolvedValue([{ user_id: "owner-fixture", role: "member" }]);
  mocks.agents.mockReset().mockResolvedValue([{ id: "agent-fixture", name: "Fixture agent", runtime_id: "runtime-fixture", archived_at: null }]);
  mocks.remove.mockReset().mockResolvedValue(undefined);
});

describe("delete offline computer", () => {
  it.each([["online", "owner-fixture"], ["offline", "another-owner"]])("does not offer deletion for %s / %s", async (status, owner) => {
    renderButton(machine(status, owner));
    await waitFor(() => expect(mocks.members).toHaveBeenCalled());
    expect(screen.queryByRole("button", { name: /Delete computer/ })).not.toBeInTheDocument();
  });

  it("confirms the exact runtime and agent snapshot before deletion", async () => {
    const { client } = renderButton();
    const opener = screen.getByRole("button", { name: "Delete computer Fixture computer" });
    await waitFor(() => expect(opener).toBeEnabled());
    fireEvent.click(opener);
    const dialog = screen.getByRole("dialog");
    const confirm = within(dialog).getByRole("button", { name: "Delete computer" });
    expect(confirm).toBeDisabled();
    expect(within(dialog).getByText("Fixture agent")).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("checkbox"));
    fireEvent.click(confirm);
    await waitFor(() => expect(mocks.remove).toHaveBeenCalledWith("runtime-fixture", ["runtime-fixture"], ["agent-fixture"]));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    client.clear();
  });

  it("shows failure without closing or allowing an unsafe retry of a stale snapshot", async () => {
    mocks.remove.mockRejectedValue(new Error("Computer reconnected"));
    renderButton();
    const opener = screen.getByRole("button", { name: "Delete computer Fixture computer" });
    await waitFor(() => expect(opener).toBeEnabled());
    fireEvent.click(opener);
    fireEvent.click(screen.getByRole("checkbox"));
    fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Delete computer" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Computer reconnected");
    expect(within(screen.getByRole("dialog")).getByRole("button", { name: "Delete computer" })).toBeDisabled();
  });
});
