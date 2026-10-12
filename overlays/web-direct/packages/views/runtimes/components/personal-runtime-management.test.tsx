// @vitest-environment jsdom
import { type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import { RuntimeDetailPage } from "./runtime-detail-page";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";
import enAgents from "../../locales/en/agents.json";

const mocks = vi.hoisted(() => ({
  role: "member", owner: "user-me", create: vi.fn(), update: vi.fn(),
  auth: { user: { id: "user-me" } },
}));
vi.mock("@multica/core/auth", () => ({
  useAuthStore: Object.assign((selector: (state: typeof mocks.auth) => unknown) => selector(mocks.auth), { getState: () => mocks.auth }),
}));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@multica/core/paths", () => ({ useWorkspacePaths: () => ({
  runtimes: () => "/runtimes", agentDetail: () => "/agents", runtimeSettings: () => "/settings",
}) }));
vi.mock("@multica/core/realtime", () => ({ useWSEvent: () => {} }));
vi.mock("@multica/core/api", () => ({ ApiError: class extends Error {}, api: {
  listRuntimes: async () => [{
    id: "runtime", workspace_id: "ws-1", daemon_id: "daemon", name: "Codex (My Mac)", runtime_mode: "local", provider: "codex",
    status: "offline", device_info: "My Mac", metadata: {}, owner_id: mocks.owner, visibility: "public", profile_id: "profile",
    created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z", last_seen_at: null,
  }],
  listRuntimeProfiles: async () => [{
    id: "profile", workspace_id: "ws-1", display_name: "My Codex", protocol_family: "codex", command_name: "codex",
    description: null, fixed_args: [], visibility: "private", created_by: mocks.owner, enabled: true,
    created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z",
  }],
  listMembers: async () => [{ user_id: "user-me", role: mocks.role, name: "Me" }],
  listAgents: async () => [], getAgentTaskSnapshot: async () => [], getRuntimeUsage: async () => [],
  createRuntimeProfile: mocks.create, updateRuntimeProfile: mocks.update,
} }));
vi.mock("../../navigation", () => ({
  AppLink: ({ children, href }: { children: ReactNode; href: string }) => <a href={href}>{children}</a>,
  useNavigation: () => ({ push: vi.fn(), replace: vi.fn() }), useIntentNavigate: () => vi.fn(), useRowLink: () => () => ({}),
}));
vi.mock("./machine-cli-section", () => ({ MachineCliSection: () => null }));
vi.mock("../../common/actor-avatar", () => ({ ActorAvatar: () => null }));
vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

beforeEach(() => {
  mocks.role = "member";
  mocks.owner = "user-me";
  mocks.create.mockReset().mockResolvedValue({ id: "created" });
  mocks.update.mockReset().mockResolvedValue({ id: "profile" });
});

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(<QueryClientProvider client={client}>
    <I18nProvider locale="en" resources={{ en: { common: enCommon, runtimes: enRuntimes, agents: enAgents } }}>
      <RuntimeDetailPage runtimeId="runtime" />
    </I18nProvider>
  </QueryClientProvider>);
}

describe("personal runtime management from a machine", () => {
  it("lets a non-admin owner create a profile", async () => {
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Add custom runtime" }));
    fireEvent.click(screen.getByRole("button", { name: /^codex$/i }));
    fireEvent.change(screen.getByLabelText("Display name"), { target: { value: "Personal wrapper" } });
    fireEvent.change(screen.getByLabelText("Command"), { target: { value: "my-codex --fast" } });
    fireEvent.click(screen.getByRole("button", { name: "Create runtime" }));
    await waitFor(() => expect(mocks.create).toHaveBeenCalledWith("ws-1", expect.objectContaining({ display_name: "Personal wrapper", command_name: "my-codex", fixed_args: ["--fast"] })));
  });

  it("lets a non-admin owner edit a profile from its row", async () => {
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Row actions" }));
    fireEvent.click(screen.getByText("Edit custom runtime"));
    expect(screen.getByLabelText("Display name")).toHaveValue("My Codex");
    expect(screen.getByText(/same settings in every workspace/)).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Display name"), { target: { value: "Renamed personal profile" } });
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(mocks.update).toHaveBeenCalledWith("ws-1", "profile", expect.objectContaining({ display_name: "Renamed personal profile" })));
    expect(screen.queryByText("Public")).not.toBeInTheDocument();
  });

  it.each(["member", "admin", "owner"])("does not grant another runtime's management to workspace %s", async (role) => {
    mocks.role = role;
    mocks.owner = "someone-else";
    renderPage();
    await screen.findByRole("heading", { name: "My Mac" });
    expect(screen.queryByRole("button", { name: "Add custom runtime" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Row actions" })).not.toBeInTheDocument();
    expect(screen.queryByText("Public")).not.toBeInTheDocument();
  });
});
