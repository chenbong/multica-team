import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import { DuccCredentialCard } from "./ducc-credential-card";
import { useSettingsSearchIndex } from "./use-settings-search-index";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";

const apiMock = vi.hoisted(() => ({ get: vi.fn(), update: vi.fn() }));
const emptyDuccStatus = { available: false, username: "", enabled: false, imported: false, version: 0, source_daemon: "", pending_daemon: "", imported_at: null, machines: [] };
vi.mock("@multica/core/api", () => ({ api: { getDuccCredential: apiMock.get, updateDuccCredential: apiMock.update } }));
vi.mock("@multica/core/auth", () => {
  const state = { user: { id: "user-fixture" } };
  return { useAuthStore: Object.assign((selector: (s: typeof state) => unknown) => selector(state), { getState: () => state }) };
});
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

function SearchProbe() {
  const entries = useSettingsSearchIndex([{ value: "profile", label: "Profile" }]);
  return <output>{entries.filter(entry => entry.anchor === "ducc").map(entry => `${entry.tab}:${entry.anchor}:${entry.title}`).join(",")}</output>;
}

function renderCard() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(<QueryClientProvider client={client}><I18nProvider locale="en" resources={{ en: { common: enCommon, settings: enSettings } }}><DuccCredentialCard /><SearchProbe /></I18nProvider></QueryClientProvider>);
}

beforeEach(() => {
  apiMock.get.mockReset().mockResolvedValue({ ...emptyDuccStatus, available: true, imported: true });
  apiMock.update.mockReset().mockResolvedValue(undefined);
});

describe("ducc card in the refactored settings", () => {
  it("keeps the account scope, search target and confirmation before removal", async () => {
    const { container } = renderCard();
    expect(await screen.findByRole("button", { name: "Remove stored credential" })).toBeEnabled();
    expect(container.querySelector('[data-settings-anchor="ducc"]')).toBeTruthy();
    expect(screen.getByText("profile:ducc:ducc credentials")).toBeInTheDocument();
    expect(container.querySelector('[data-slot="settings-scope"]')).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Remove stored credential" }));
    expect(apiMock.update).not.toHaveBeenCalled();
    fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Confirm" }));
    await waitFor(() => expect(apiMock.update).toHaveBeenCalledWith({ action: "remove" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("keeps the confirmation open when saving fails", async () => {
    apiMock.update.mockRejectedValue(new Error("fixture failure"));
    renderCard();
    fireEvent.click(await screen.findByRole("button", { name: "Remove stored credential" }));
    fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Confirm" }));
    await waitFor(() => expect(apiMock.update).toHaveBeenCalledTimes(1));
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });

  it("disables credential mutations when the service is unavailable", async () => {
    apiMock.get.mockResolvedValue(emptyDuccStatus);
    renderCard();
    expect(await screen.findByText("Credential service unavailable")).toBeInTheDocument();
    expect(screen.getByRole("checkbox")).toHaveAttribute("aria-disabled", "true");
    fireEvent.click(screen.getByRole("checkbox"));
    expect(apiMock.update).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Import credential to platform" })).toBeDisabled();
  });
});
