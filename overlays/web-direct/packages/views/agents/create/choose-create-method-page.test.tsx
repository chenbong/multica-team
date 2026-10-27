import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import copy from "../../locales/en/agents.json";
import { CreateMethodChooser } from "./choose-create-method-page";

const state = vi.hoisted(() => ({ push: vi.fn(), listCopySources: vi.fn() }));
vi.mock("@multica/core/api", () => ({ api: state }));
vi.mock("@multica/core/auth", () => ({
  useAuthStore: (select: (s: { user: { id: string } }) => unknown) => select({ user: { id: "owner" } }),
}));
vi.mock("../../navigation", () => ({
  AppLink: ({ href, children, ...props }: React.ComponentProps<"a">) => <a href={href} {...props}>{children}</a>,
  useNavigation: () => ({ push: state.push }),
  useBackOrReplace: () => vi.fn(),
}));
vi.mock("./create-shell", () => ({ AgentCreateShell: ({ children }: { children: React.ReactNode }) => <div>{children}</div> }));
vi.mock("../../i18n", () => ({ useT: () => ({ t: (select: (s: typeof copy) => string) => select(copy) }) }));

function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={client}><CreateMethodChooser blankHref="/ws/agents/new/manual?squad=team" aiHref="/ws/agents/new/ai?squad=team" /></QueryClientProvider>);
}

describe("agent creation method cards", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    state.listCopySources.mockResolvedValue([
      { workspaceName: "Other workspace", agent: { id: "source", name: "Source agent", workspace_id: "other-ws" } },
    ]);
  });
  afterEach(cleanup);

  it("places all three methods in the same responsive grid with matching cards", () => {
    mount();
    const blank = screen.getByRole("link", { name: new RegExp(copy.creation_studio.modes.blank.title) });
    const ai = screen.getByRole("link", { name: new RegExp(copy.creation_studio.modes.ai.title) });
    const duplicate = screen.getByRole("button", { name: new RegExp(copy.platform_expansion.copy) });
    expect(duplicate.parentElement).toBe(blank.parentElement);
    expect(ai.parentElement).toBe(blank.parentElement);
    expect(blank.parentElement).toHaveClass("grid", "grid-cols-1", "md:grid-cols-3");
    for (const card of [blank, ai, duplicate]) expect(card).toHaveClass("min-h-56", "rounded-xl", "p-5");
    expect(blank).toHaveAttribute("href", "/ws/agents/new/manual?squad=team");
    expect(ai).toHaveAttribute("href", "/ws/agents/new/ai?squad=team");
    expect(state.listCopySources).not.toHaveBeenCalled();
  });

  it("opens the existing chooser, restores focus on Escape, and preserves copy navigation", async () => {
    const user = userEvent.setup();
    mount();
    const trigger = screen.getByRole("button", { name: new RegExp(copy.platform_expansion.copy) });
    trigger.focus();
    await user.keyboard("{Enter}");
    await screen.findByRole("dialog", { name: copy.platform_expansion.copy });
    await screen.findByRole("button", { name: /Source agent/ });
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    await waitFor(() => expect(trigger).toHaveFocus());
    await user.click(trigger);
    await user.type(screen.getByRole("textbox", { name: copy.platform_expansion.search }), "Other workspace");
    await user.click(await screen.findByRole("button", { name: /Source agent/ }));
    expect(state.push).toHaveBeenCalledWith("/ws/agents/new/manual?squad=team&duplicate=source&duplicate_ws=other-ws");
  });
});
