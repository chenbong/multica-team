// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { toast } from "sonner";
import { I18nProvider } from "@multica/core/i18n/react";
import type { AgentRuntime } from "@multica/core/types";
import { DeleteMachineButton } from "./delete-machine-button";
import type { RuntimeMachine } from "./runtime-machines";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";
import zhRuntimes from "../../locales/zh-Hans/runtimes.json";

const mocks = vi.hoisted(() => ({
  plan: vi.fn(), workspaces: vi.fn(), remove: vi.fn(),
  auth: { user: { id: "owner-fixture" } as { id: string } | null },
}));
vi.mock("@multica/core/api", () => ({ api: {
  personalMachineDeletePlan: mocks.plan, listWorkspaces: mocks.workspaces,
  deleteOfflineMachine: mocks.remove,
} }));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "workspace-fixture" }));
vi.mock("@multica/core/auth", () => ({
  useAuthStore: Object.assign((selector: (s: typeof mocks.auth) => unknown) => selector(mocks.auth), {
    getState: () => mocks.auth,
  }),
}));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

function machine(status = "offline", owner = "owner-fixture"): RuntimeMachine {
  return {
    id: "machine-fixture", daemonId: "daemon-fixture", title: "Fixture computer", subtitle: null, deviceInfo: null,
    cliVersion: null, launchedBy: null, mode: "local", section: "remote", isCurrent: false, health: "offline",
    runtimes: [{ id: "runtime-fixture", status, owner_id: owner } as AgentRuntime], onlineCount: status === "online" ? 1 : 0,
    issueCount: 0, runningCount: 0, queuedCount: 0, providerNames: [], lastSeenAt: null,
  };
}
function renderButton(value = machine(), locale: "en" | "zh-Hans" = "en") {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const tree = (current: RuntimeMachine) => <QueryClientProvider client={client}>
    <I18nProvider locale={locale} resources={{ en: { common: enCommon, runtimes: enRuntimes }, "zh-Hans": { common: enCommon, runtimes: zhRuntimes } }}>
      <DeleteMachineButton machine={current} />
    </I18nProvider>
  </QueryClientProvider>;
  const result = render(tree(value));
  return { ...result, client, show: (current: RuntimeMachine) => result.rerender(tree(current)) };
}
async function openPlan() {
  fireEvent.click(screen.getByRole("button", { name: "Delete computer Fixture computer" }));
  return screen.findByRole("dialog");
}

const plan = {
  ids: ["runtime-other-workspace", "runtime-fixture"],
  agents: [
    { id: "agent-other", name: "Other workspace agent", workspace_id: "workspace-other" },
    { id: "agent-fixture", name: "Fixture agent", workspace_id: "workspace-fixture" },
  ],
};

beforeEach(() => {
  vi.clearAllMocks();
  mocks.auth.user = { id: "owner-fixture" };
  mocks.plan.mockReset().mockResolvedValue(plan);
  mocks.workspaces.mockReset().mockResolvedValue([
    { id: "workspace-other", name: "Research" }, { id: "workspace-fixture", name: "Home" },
  ]);
  mocks.remove.mockReset().mockResolvedValue(undefined);
});

describe("delete a personal offline computer", () => {
  it.each([["online", "owner-fixture"], ["offline", "another-owner"]])("does not offer deletion for %s / %s", (status, owner) => {
    renderButton(machine(status, owner));
    expect(screen.queryByRole("button", { name: /Delete computer/ })).not.toBeInTheDocument();
    expect(mocks.plan).not.toHaveBeenCalled();
  });

  it.each(["anonymous", "cloud", "no-daemon", "empty", "mixed-owner"])("does not offer deletion for %s", (condition) => {
    const value = machine();
    if (condition === "anonymous") mocks.auth.user = null;
    if (condition === "cloud") value.mode = "cloud";
    if (condition === "no-daemon") value.daemonId = null;
    if (condition === "empty") value.runtimes = [];
    if (condition === "mixed-owner") value.runtimes.push(...machine("offline", "someone-else").runtimes);
    renderButton(value);
    expect(screen.queryByRole("button", { name: /Delete computer/ })).not.toBeInTheDocument();
  });

  it("confirms the full server snapshot across workspaces without requiring an admin role", async () => {
    const { client } = renderButton();
    client.setQueryData(["runtimes", "other-workspace", "owner-fixture"], []);
    client.setQueryData(["runtime-profiles", "other-workspace", "owner-fixture"], []);
    client.setQueryData(["workspaces", "other-workspace", "agents"], []);
    const dialog = await openPlan();
    expect(mocks.plan).toHaveBeenCalledWith("runtime-fixture");
    const confirm = within(dialog).getByRole("button", { name: "Delete computer" });
    expect(confirm).toBeDisabled();
    expect(dialog).toHaveTextContent("2 runtime registrations across all workspaces");
    expect(dialog).toHaveTextContent("Unfinished runs block deletion");
    expect(within(dialog).getByText("Other workspace agent")).toBeInTheDocument();
    expect(await within(dialog).findByText("Research")).toBeInTheDocument();
    expect(await within(dialog).findByText("Home")).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("checkbox", { name: /across all workspaces/ }));
    fireEvent.click(confirm);
    await waitFor(() => expect(mocks.remove).toHaveBeenCalledWith("runtime-fixture", plan.ids, ["agent-other", "agent-fixture"]));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    for (const key of [["runtimes", "other-workspace", "owner-fixture"], ["runtime-profiles", "other-workspace", "owner-fixture"], ["workspaces", "other-workspace", "agents"]]) {
      expect(client.getQueryState(key)?.isInvalidated).toBe(true);
    }
  });

  it("requires all-workspace confirmation even when no agents are bound", async () => {
    mocks.plan.mockResolvedValue({ ...plan, agents: [] });
    renderButton();
    const dialog = await openPlan();
    const confirm = within(dialog).getByRole("button", { name: "Delete computer" });
    expect(confirm).toBeDisabled();
    fireEvent.click(within(dialog).getByRole("checkbox", { name: /all workspaces/ }));
    fireEvent.click(confirm);
    await waitFor(() => expect(mocks.remove).toHaveBeenCalledWith("runtime-fixture", plan.ids, []));
  });

  it("does not open a confirmation or submit when loading the plan fails", async () => {
    mocks.plan.mockRejectedValueOnce(new Error("Failed to read dependencies"));
    renderButton();
    fireEvent.click(screen.getByRole("button", { name: "Delete computer Fixture computer" }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Failed to read dependencies"));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(mocks.remove).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Delete computer Fixture computer" })).toBeEnabled();
    await openPlan();
    expect(mocks.plan).toHaveBeenCalledTimes(2);
  });

  it.each(["Computer reconnected", "Runtime still has unfinished runs", "Deletion plan changed"])("blocks stale retries after %s and refreshes on reopen", async (reason) => {
    mocks.remove.mockRejectedValueOnce(new Error(reason));
    renderButton();
    const dialog = await openPlan();
    fireEvent.click(within(dialog).getByRole("checkbox"));
    fireEvent.click(within(dialog).getByRole("button", { name: "Delete computer" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(reason);
    expect(within(dialog).getByRole("button", { name: "Delete computer" })).toBeDisabled();
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    mocks.plan.mockResolvedValue({ ids: ["runtime-fixture"], agents: [] });
    const refreshed = await openPlan();
    expect(within(refreshed).getByRole("checkbox")).not.toBeChecked();
    expect(within(refreshed).queryByRole("alert")).not.toBeInTheDocument();
    expect(mocks.remove).toHaveBeenCalledTimes(1);
  });

  it("ignores an in-flight plan after changing accounts", async () => {
    let resolve!: (value: typeof plan) => void;
    mocks.plan.mockReturnValue(new Promise<typeof plan>((done) => { resolve = done; }));
    const view = renderButton();
    const opener = screen.getByRole("button", { name: "Delete computer Fixture computer" });
    fireEvent.click(opener);
    expect(opener).toBeDisabled();
    mocks.auth.user = { id: "next-owner" };
    view.show(machine("offline", "next-owner"));
    await act(async () => { resolve(plan); });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(mocks.remove).not.toHaveBeenCalled();
  });

  it("discards confirmation when the computer reconnects", async () => {
    const view = renderButton();
    await openPlan();
    fireEvent.click(screen.getByRole("checkbox"));
    view.show(machine("online"));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(mocks.remove).not.toHaveBeenCalled();
  });

  it("keeps the dialog open and prevents duplicate submits while deleting", async () => {
    let resolve!: () => void;
    mocks.remove.mockReturnValue(new Promise<void>((done) => { resolve = done; }));
    renderButton();
    const dialog = await openPlan();
    fireEvent.click(within(dialog).getByRole("checkbox"));
    fireEvent.click(within(dialog).getByRole("button", { name: "Delete computer" }));
    const pending = await within(dialog).findByRole("button", { name: "Deleting..." });
    expect(pending).toBeDisabled();
    expect(within(dialog).getByRole("button", { name: "Cancel" })).toBeDisabled();
    fireEvent.click(pending);
    fireEvent.keyDown(dialog, { key: "Escape" });
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(mocks.remove).toHaveBeenCalledTimes(1);
    await act(async () => { resolve(); });
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("warns and confirms the global effect in Chinese too", async () => {
    renderButton(machine(), "zh-Hans");
    fireEvent.click(screen.getByRole("button", { name: "删除电脑 Fixture computer" }));
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("所有工作区");
    expect(dialog).toHaveTextContent("有未结束任务时拒绝删除");
    expect(within(dialog).getByRole("checkbox", { name: /所有工作区/ })).not.toBeChecked();
    expect(dialog).not.toHaveTextContent("未完成运行会取消");
  });
});
