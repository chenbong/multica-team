// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { Agent } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../../locales/en/common.json";
import enAgents from "../../../locales/en/agents.json";
import enSkills from "../../../locales/en/skills.json";

const getAgentMemory = vi.hoisted(() => vi.fn());
const updateAgentMemory = vi.hoisted(() => vi.fn());
const toastError = vi.hoisted(() => vi.fn());
const toastSuccess = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/api", () => ({
  api: { getAgentMemory, updateAgentMemory },
}));

vi.mock("sonner", () => ({
  toast: { error: toastError, success: toastSuccess },
}));

import { MemoryTab } from "./memory-tab";

const TEST_RESOURCES = { en: { common: enCommon, agents: enAgents, skills: enSkills } };

const agent: Agent = {
  id: "agent-1",
  workspace_id: "ws-1",
  runtime_id: "runtime-1",
  name: "Agent",
  description: "",
  instructions: "",
  avatar_url: null,
  runtime_mode: "local",
  runtime_config: {},
  custom_args: [],
  visibility: "workspace",
  permission_mode: "public_to",
  invocation_targets: [{ target_type: "workspace", target_id: null }],
  status: "idle",
  max_concurrent_tasks: 1,
  model: "",
  owner_id: "user-1",
  skills: [],
  created_at: "2026-08-08T00:00:00Z",
  updated_at: "2026-08-08T00:00:00Z",
  archived_at: null,
  archived_by: null,
};

const limits = { max_files: 64, max_file_bytes: 262144, max_total_bytes: 1048576 };

function serverFile(path: string, content: string, source = "manual", enabled = true) {
  return {
    id: `id-${path}`,
    path,
    content,
    source,
    source_runtime_id: null,
    source_path: "",
    enabled,
    size_bytes: content.length,
    updated_by: "user-1",
    created_at: "2026-10-10T00:00:00Z",
    updated_at: "2026-10-10T00:00:00Z",
  };
}

function renderTab(onDirtyChange = vi.fn()) {
  render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <MemoryTab agent={agent} runtime={null} onDirtyChange={onDirtyChange} />
    </I18nProvider>,
  );
  return { onDirtyChange };
}

describe("MemoryTab", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    getAgentMemory.mockResolvedValue({ agent_id: "agent-1", files: [], limits });
    updateAgentMemory.mockImplementation(async (_id: string, body: { files: { path: string; content: string; source?: string; enabled?: boolean }[] }) => ({
      agent_id: "agent-1",
      files: body.files.map((f) => serverFile(f.path, f.content, f.source, f.enabled ?? true)),
      limits,
    }));
  });

  it("creates a memory file by hand and saves the whole set", async () => {
    const user = userEvent.setup();
    const { onDirtyChange } = renderTab();

    expect(await screen.findByText("No memory yet")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /new file/i }));

    const pathInput = screen.getByLabelText("File path");
    expect(pathInput).toHaveValue("MEMORY.md");
    await user.clear(pathInput);
    await user.type(pathInput, "notes/build.md");
    const editor = screen.getByRole("textbox", { name: /notes\/build\.md/ });
    await user.type(editor, "Use pnpm.");

    expect(onDirtyChange).toHaveBeenLastCalledWith(true);
    await user.click(screen.getByRole("button", { name: /^save$/i }));

    await waitFor(() => expect(updateAgentMemory).toHaveBeenCalledTimes(1));
    expect(updateAgentMemory).toHaveBeenCalledWith("agent-1", {
      files: [{ path: "notes/build.md", content: "Use pnpm.", source: "manual", enabled: true }],
    });
    await waitFor(() => expect(onDirtyChange).toHaveBeenLastCalledWith(false));
    expect(toastSuccess).toHaveBeenCalledWith("Memory saved");
  });

  it("adds uploaded text files to the draft", async () => {
    getAgentMemory.mockResolvedValue({ agent_id: "agent-1", files: [serverFile("CLAUDE.md", "rules")], limits });
    renderTab();
    expect(await screen.findByRole("option", { name: /CLAUDE\.md/ })).toBeInTheDocument();

    const input = screen.getByTestId("memory-upload-input");
    fireEvent.change(input, {
      target: { files: [new File(["# Team"], "team.md", { type: "text/markdown" })] },
    });

    expect(await screen.findByRole("option", { name: /team\.md/ })).toBeInTheDocument();
    await userEvent.setup().click(screen.getByRole("button", { name: /^save$/i }));
    await waitFor(() =>
      expect(updateAgentMemory).toHaveBeenCalledWith("agent-1", {
        files: [
          { path: "CLAUDE.md", content: "rules", source: "manual", enabled: true },
          { path: "team.md", content: "# Team", source: "upload", source_path: "team.md", enabled: true },
        ],
      }),
    );
  });

  it("blocks saving an invalid path", async () => {
    const user = userEvent.setup();
    getAgentMemory.mockResolvedValue({ agent_id: "agent-1", files: [serverFile("CLAUDE.md", "rules")], limits });
    renderTab();
    const pathInput = await screen.findByLabelText("File path");
    await user.clear(pathInput);
    await user.type(pathInput, "../escape.md");

    expect(screen.getByText("The path must stay inside the memory folder")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /^save$/i })).toBeDisabled();
  });

  it("deletes a file", async () => {
    const user = userEvent.setup();
    getAgentMemory.mockResolvedValue({
      agent_id: "agent-1",
      files: [serverFile("a.md", "a"), serverFile("b.md", "b")],
      limits,
    });
    renderTab();
    await screen.findByRole("option", { name: /a\.md/ });
    await user.click(screen.getByRole("button", { name: "Delete a.md" }));
    expect(screen.queryByRole("option", { name: /a\.md/ })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /^save$/i }));
    await waitFor(() =>
      expect(updateAgentMemory).toHaveBeenCalledWith("agent-1", {
        files: [{ path: "b.md", content: "b", source: "manual", enabled: true }],
      }),
    );
  });

  it("switches individual files off and saves them as disabled", async () => {
    const user = userEvent.setup();
    getAgentMemory.mockResolvedValue({
      agent_id: "agent-1",
      files: [serverFile("a.md", "a"), serverFile("b.md", "b", "manual", false)],
      limits,
    });
    const { onDirtyChange } = renderTab();
    const aSwitch = await screen.findByRole("switch", { name: "Deliver a.md to runs" });
    expect(aSwitch).toBeChecked();
    expect(screen.getByRole("switch", { name: "Deliver b.md to runs" })).not.toBeChecked();
    expect(screen.getByText(/1 of 2 enabled/)).toBeInTheDocument();

    await user.click(aSwitch);
    expect(onDirtyChange).toHaveBeenLastCalledWith(true);
    expect(screen.getByText(/0 of 2 enabled/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /^save$/i }));
    await waitFor(() =>
      expect(updateAgentMemory).toHaveBeenCalledWith("agent-1", {
        files: [
          { path: "a.md", content: "a", source: "manual", enabled: false },
          { path: "b.md", content: "b", source: "manual", enabled: false },
        ],
      }),
    );

    await user.click(screen.getByRole("button", { name: "Enable all" }));
    expect(screen.getByText(/2 of 2 enabled/)).toBeInTheDocument();
  });
});
