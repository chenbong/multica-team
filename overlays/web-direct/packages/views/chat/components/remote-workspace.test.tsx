import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { RemoteWorkspace, RemoteWorkspaceChips } from "./remote-workspace";
import { useRemoteWorkspaceDrafts } from "./remote-workspace-state";

const request = vi.hoisted(() => vi.fn());
vi.mock("@multica/core/api", () => ({ api: { chatWorkspace: request } }));
vi.mock("@multica/core/auth", () => ({ useAuthStore: (select: (state: unknown) => unknown) => select({ user: { id: "user" } }) }));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "workspace" }));
vi.mock("../../rich-content/rich-code-block", () => ({ StaticCodeBody: ({ body }: { body: string }) => <code>{body}</code> }));
function mount(sessionId: string | null = "session") {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const ui = (session: string | null) => <QueryClientProvider client={client}><RemoteWorkspace sessionId={session}><RemoteWorkspaceChips/><div>Chat body</div></RemoteWorkspace></QueryClientProvider>;
  return { ...render(ui(sessionId)), client, ui };
}
describe("remote workspace UI", () => {
  beforeEach(() => {
    request.mockReset(); useRemoteWorkspaceDrafts.setState({ drafts: {} });
    request.mockImplementation(async (_session, query) => query.operation === "read"
      ? { binding: "binding", root_name: "project", content: "export const answer = 42;", revision: "rev" }
      : { binding: "binding", root_name: "project", entries: [{ name: "main.ts", path: "main.ts", kind: "file", size: 25 }, { name: "src", path: "src", kind: "directory", size: 0 }] });
  });
  it("does not request files until opened and explains unprepared sessions", () => {
    mount(null); expect(request).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "文件 / Files" }));
    expect(screen.getByText(/先创建聊天/)).toBeInTheDocument(); expect(request).not.toHaveBeenCalled();
  });
  it("opens a file, attaches it with a revision, removes it and adds a directory", async () => {
    mount(); fireEvent.click(screen.getByRole("button", { name: "文件 / Files" }));
    fireEvent.click(await screen.findByRole("button", { name: "main.ts" }));
    expect(await screen.findByText("export const answer = 42;")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "文件 / File" }));
    expect(Object.values(useRemoteWorkspaceDrafts.getState().drafts)[0]?.references).toEqual([{ path: "main.ts", kind: "file", revision: "rev" }]);
    fireEvent.click(screen.getByRole("button", { name: "移除引用 / Remove main.ts" }));
    fireEvent.click(screen.getByRole("button", { name: "添加到聊天 / Add src" }));
    expect(Object.values(useRemoteWorkspaceDrafts.getState().drafts)[0]?.references).toEqual([{ path: "src", kind: "directory" }]);
  });
  it("isolates conversation switches and removes cached source contents", async () => {
    const view = mount(); fireEvent.click(screen.getByRole("button", { name: "文件 / Files" }));
    fireEvent.click(await screen.findByRole("button", { name: "main.ts" })); await screen.findByText("export const answer = 42;");
    fireEvent.click(screen.getByRole("button", { name: "文件 / File" }));
    view.rerender(view.ui("other-session"));
    expect(screen.queryByText("export const answer = 42;")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "移除引用 / Remove main.ts" })).not.toBeInTheDocument();
    await waitFor(() => expect(view.client.getQueryCache().getAll().filter((q) => q.queryKey[1] === JSON.stringify(["user", "workspace", "session"]))).toHaveLength(0));
  });
  it("shows actionable offline errors and supports retry", async () => {
    request.mockRejectedValueOnce(new Error("workspace_unavailable_or_upgrade_required"));
    mount(); fireEvent.click(screen.getByRole("button", { name: "文件 / Files" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("daemon 需要升级");
    fireEvent.click(screen.getByRole("button", { name: "刷新文件 / Refresh files" }));
    expect(await screen.findByRole("button", { name: "main.ts" })).toBeInTheDocument();
  });
  it("does not allow an old response to appear in another session", async () => {
    let resolve!: (data: unknown) => void;
    request.mockReturnValueOnce(new Promise((done) => { resolve = done; }));
    const view = mount(); fireEvent.click(screen.getByRole("button", { name: "文件 / Files" }));
    view.rerender(view.ui("other-session"));
    await act(async () => resolve({ binding: "old", root_name: "private-project", entries: [] }));
    expect(screen.queryByText("private-project")).not.toBeInTheDocument();
  });
});
