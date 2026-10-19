import { beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { renderWithI18n } from "../../test/i18n";
import { CreateSkillDialog } from "./create-skill-dialog";

const mocks = vi.hoisted(() => ({ importSkill: vi.fn(), openExternal: vi.fn() }));
vi.mock("@multica/core/api", () => ({ api: { importSkill: (...args: unknown[]) => mocks.importSkill(...args) } }));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-local" }));
vi.mock("../../platform", async () => ({ ...await vi.importActual<typeof import("../../platform")>("../../platform"), openExternal: mocks.openExternal }));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

const version = "https://mcp-proxy.baidu-int.com/api/v1/skills/download/42424242/9.8.7/popo.zip";
const permanent = "https://mcp-proxy.baidu-int.com/api/v1/skills/download/permanent/11111111-2222-4333-8444-555555555555/popo.zip";

function openForm() {
  const onCreated = vi.fn();
  renderWithI18n(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
    <CreateSkillDialog onClose={vi.fn()} onCreated={onCreated} />
  </QueryClientProvider>);
  fireEvent.click(screen.getByRole("button", { name: /Workspace skill/ }));
  fireEvent.click(screen.getByRole("button", { name: /Import from URL/ }));
  return onCreated;
}

beforeEach(() => {
  vi.clearAllMocks();
  mocks.importSkill.mockResolvedValue({ id: "imported-local", workspace_id: "ws-local", name: "popo" });
});

describe("OneTool URL import form", () => {
  it("shows OneTool and opens the requested browse page", () => {
    openForm();
    expect(screen.getByText("OneTool")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "console.cloud.baidu-int.com/onetool/skills" }));
    expect(mocks.openExternal).toHaveBeenCalledWith("https://console.cloud.baidu-int.com/onetool/skills");
    expect(screen.getByText(/OneTool: paste a ZIP download link/)).toBeInTheDocument();
  });
  it.each([version, permanent, `下载并安装此Skill ${version}`, `下载并安装此Skill ${permanent}`])("imports a supported package link or clipboard text", async input => {
    const onCreated = openForm();
    fireEvent.change(screen.getByLabelText("Skill URL"), { target: { value: input } });
    fireEvent.click(screen.getByRole("button", { name: "Import" }));
    await waitFor(() => expect(onCreated).toHaveBeenCalled());
    expect(mocks.importSkill).toHaveBeenCalledWith(expect.objectContaining({ url: input.includes("permanent") ? permanent : version }));
  });
  it("explains that a detail page needs a download link without calling the API", () => {
    openForm();
    fireEvent.change(screen.getByLabelText("Skill URL"), { target: { value: "https://console.cloud.baidu-int.com/onetool/skills/154" } });
    fireEvent.click(screen.getByRole("button", { name: "Import" }));
    expect(screen.getByRole("alert")).toHaveTextContent("Detail-page URLs are not supported without authorization");
    expect(mocks.importSkill).not.toHaveBeenCalled();
  });
  it("keeps the entered link available after anonymous download is denied", async () => {
    mocks.importSkill.mockRejectedValue(new Error("OneTool denied anonymous download"));
    openForm();
    fireEvent.change(screen.getByLabelText("Skill URL"), { target: { value: permanent } });
    fireEvent.click(screen.getByRole("button", { name: "Import" }));
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("denied anonymous download"));
    expect(screen.getByLabelText("Skill URL")).toHaveValue(permanent);
    expect(screen.getByRole("button", { name: "Import" })).toBeEnabled();
  });
});
