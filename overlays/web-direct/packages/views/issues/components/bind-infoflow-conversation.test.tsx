import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ApiError } from "@multica/core/api";
import { infoFlowConversationURL, type InfoFlowConversationsStatus } from "@multica/core/issues/infoflow-conversations";
import en from "../../locales/en/issues.json";
import { BindInfoFlowConversation } from "./bind-infoflow-conversation";

const state = vi.hoisted(() => ({ user: "user-a", get: vi.fn(), post: vi.fn(), remove: vi.fn(), cancel: vi.fn(), legacy: vi.fn() }));
vi.mock("@multica/core/api", async (original) => ({ ...await original<typeof import("@multica/core/api")>(), api: {
  getIssueInfoFlowConversations: state.get, bindIssueInfoFlowConversation: state.post, unbindIssueInfoFlowConversation: state.remove,
  cancelIssueInfoFlowRequest: state.cancel,
  getIssueInfoFlowGroupBinding: state.legacy, getIssueInfoFlowGroups: vi.fn().mockResolvedValue([]),
} }));
vi.mock("@multica/core/auth", () => ({ useAuthStore: Object.assign((selector: (value: { user: { id: string } }) => unknown) => selector({ user: { id: state.user } }), { getState: () => ({ user: { id: state.user } }) }) }));
vi.mock("../../i18n", () => ({ useT: () => ({ t: (selector: (value: typeof en) => string) => selector(en) }) }));
const id = "00000000-0000-4000-8000-000000000001";
const fresh = (): InfoFlowConversationsStatus => ({ enabled: true, group: { canManage: true, binding: null, request: null, groups: [{ groupId: "123", name: "Existing group" }] }, private: { eligible: true, canManage: true, reason: "", bindings: [], request: null } });
let client: QueryClient;
beforeEach(() => {
  vi.clearAllMocks(); state.user = "user-a";
  client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } } });
  state.get.mockReset().mockResolvedValue(fresh()); state.post.mockReset().mockResolvedValue(fresh()); state.remove.mockReset().mockResolvedValue(fresh());
  state.cancel.mockReset().mockResolvedValue(fresh());
  state.legacy.mockReset().mockResolvedValue({ enabled: false, canManage: false, binding: null, request: null, groups: [] });
});
afterEach(() => { cleanup(); client.clear(); });
function view() { return <QueryClientProvider client={client}><BindInfoFlowConversation workspaceId="ws" issueId="task" /></QueryClientProvider>; }
async function open() {
  render(view()); const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: en.infoflow_conversations.bind })); return user;
}

describe("unified InfoFlow dialog", () => {
  it("has one labeled entry, group and own-private tabs", async () => {
    await open(); expect(screen.getByRole("dialog", { name: en.infoflow_conversations.bind })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: en.infoflow_conversations.group })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: en.infoflow_conversations.private })).toBeInTheDocument();
  });
  it("binds only the current user's private target and waits for verification", async () => {
    const user = await open(); await user.click(screen.getByRole("tab", { name: en.infoflow_conversations.private }));
    const pending = { ...fresh(), private: { ...fresh().private, request: { requestId: id, state: "pending" as const, reason: "" } } };
    state.post.mockResolvedValue(pending); state.get.mockResolvedValue(pending);
    await user.click(screen.getByRole("button", { name: en.infoflow_groups.submit }));
    await waitFor(() => expect(state.post).toHaveBeenCalledWith("task", { kind: "private" }));
    expect(await screen.findByText(en.infoflow_conversations.private_waiting)).toBeInTheDocument();
    expect(screen.queryByRole("link")).not.toBeInTheDocument(); expect(screen.getByRole("button", { name: en.infoflow_groups.submit })).toBeDisabled();
  });
  it("disallows squad private creation but retains explicit removal of a stale own binding", async () => {
    state.get.mockResolvedValue({ ...fresh(), private: { eligible: false, canManage: true, reason: "", request: null, bindings: [{ bindingId: id, robotId: "old", robotChatId: "123", robotName: "Previous robot", state: "needs_rebind", reason: "assignment_changed" }] } });
    const user = await open(); await user.click(screen.getByRole("tab", { name: en.infoflow_conversations.private }));
    expect(screen.getByText(en.infoflow_conversations.private_restriction)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: en.infoflow_groups.submit })).not.toBeInTheDocument();
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: `${en.infoflow_conversations.unbind_private} Previous robot` }));
    await waitFor(() => expect(state.remove).toHaveBeenCalledWith("task", id)); expect(state.post).not.toHaveBeenCalled();
  });
  it("requires explicit rebind and does not open stale private links", async () => {
    const stale = { ...fresh(), private: { ...fresh().private, bindings: [{ bindingId: id, robotId: "old", robotChatId: "123", robotName: "Previous robot", state: "needs_rebind" as const, reason: "assignment_changed" }] } };
    state.get.mockResolvedValue(stale); const user = await open();
    await user.click(screen.getByRole("tab", { name: en.infoflow_conversations.private }));
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: en.infoflow_conversations.rebind }));
    await waitFor(() => expect(state.post).toHaveBeenCalledWith("task", { kind: "private" }));
  });
  it("shows coexisting verified links without any multi-group dropdown", async () => {
    state.get.mockResolvedValue({ ...fresh(), group: { ...fresh().group, binding: { bindingId: id, groupId: "123", state: "active", reason: "" } }, private: { ...fresh().private, bindings: [{ bindingId: id, robotId: "assigned", robotChatId: "9007199254740993", robotName: "Assigned robot", state: "active", reason: "" }] } });
    render(view());
    expect(await screen.findByRole("link", { name: en.infoflow_groups.open })).toHaveAttribute("href", infoFlowConversationURL("123", "group"));
    expect(screen.getByRole("link", { name: en.infoflow_conversations.open_private })).toHaveAttribute("href", infoFlowConversationURL("9007199254740993", "private"));
    expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
  });
  it("sends only the selected exact group number", async () => {
    const user = await open(); await user.selectOptions(screen.getByRole("combobox"), "123");
    await user.click(screen.getByRole("button", { name: en.infoflow_groups.submit }));
    await waitFor(() => expect(state.post).toHaveBeenCalledWith("task", { kind: "group", group_id: "123" }));
  });
  it("keeps errors visible and does not retry failed mutations", async () => {
    state.post.mockRejectedValue(new ApiError("Assignment changed", 409, "Conflict"));
    const user = await open(); await user.click(screen.getByRole("tab", { name: en.infoflow_conversations.private }));
    await user.click(screen.getByRole("button", { name: en.infoflow_groups.submit }));
    expect(await screen.findByText("Assignment changed")).toBeInTheDocument(); expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(state.post).toHaveBeenCalledTimes(1);
  });
  it("drops links and write affordances on permission loss", async () => {
    const user = await open(); state.get.mockRejectedValue(new ApiError("Denied", 403, "Forbidden"));
    await user.click(screen.getByRole("button", { name: en.infoflow_groups.refresh }));
    expect(await screen.findByText(en.infoflow_groups.read_failed)).toBeInTheDocument();
    expect(screen.queryByRole("tab")).not.toBeInTheDocument(); expect(screen.queryByRole("link")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: en.infoflow_groups.submit })).not.toBeInTheDocument();
  });
  it("resets group drafts after an account change", async () => {
    const rendered = render(view()); const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: en.infoflow_conversations.bind }));
    await user.click(screen.getByRole("radio", { name: en.infoflow_groups.manual })); await user.type(screen.getByRole("textbox"), "456");
    state.user = "user-b"; client.setQueryData(["issues", "infoflow-conversations", "user-b", "ws", "task"], fresh());
    await act(async () => { rendered.rerender(view()); });
    await user.click(screen.getByRole("radio", { name: en.infoflow_groups.manual })); expect(screen.getByRole("textbox")).toHaveValue("");
    expect(state.post).not.toHaveBeenCalled();
  });
  it.each([403, 503])("does not use the legacy permission path on %i", async (status) => {
    state.get.mockRejectedValue(new ApiError("Denied", status, "Error")); render(view());
    await waitFor(() => expect(client.isFetching()).toBe(0)); expect(state.legacy).not.toHaveBeenCalled();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });
  it.each(["group", "private"] as const)("cancels a %s request before any binding exists and allows a new submission", async (kind) => {
    const generation = "00000000-0000-4000-8000-000000000002";
    const pending = { requestId: id, generation, canCancel: true, state: "pending" as const, reason: "Waiting" };
    const current = fresh();
    if (kind === "group") current.group.request = { ...pending, groupId: "456" };
    else current.private.request = pending;
    state.get.mockResolvedValue(current);
    const user = await open();
    if (kind === "private") await user.click(screen.getByRole("tab", { name: en.infoflow_conversations.private }));
    expect(screen.queryByRole("button", { name: en.infoflow_conversations.unbind })).not.toBeInTheDocument();
    state.get.mockResolvedValue(fresh());
    await user.click(screen.getByRole("button", { name: kind === "group" ? en.infoflow_conversations.cancel_group : en.infoflow_conversations.cancel_private }));
    await waitFor(() => expect(state.cancel).toHaveBeenCalledWith("task", { kind, request_id: id, expected_generation: generation }));
    expect(screen.getByRole("dialog")).toBeInTheDocument(); expect(state.remove).not.toHaveBeenCalled();
    if (kind === "group") {
      await user.click(screen.getByRole("radio", { name: en.infoflow_groups.manual }));
      await user.clear(screen.getByRole("textbox")); await user.type(screen.getByRole("textbox"), "789");
    }
    await user.click(screen.getByRole("button", { name: en.infoflow_groups.submit }));
    await waitFor(() => expect(state.post).toHaveBeenCalledWith("task", kind === "group" ? { kind, group_id: "789" } : { kind }));
  });
  it("permits cancelling an own stale private request without granting a new binding", async () => {
    const current = fresh(); current.group.canManage = false;
    current.private = { eligible: false, canManage: false, reason: "", bindings: [], request: { requestId: id, generation: id, canCancel: true, state: "queued", reason: "Waiting" } };
    state.get.mockResolvedValue(current); const user = await open();
    await user.click(screen.getByRole("tab", { name: en.infoflow_conversations.private }));
    expect(screen.queryByRole("button", { name: en.infoflow_groups.submit })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: en.infoflow_conversations.cancel_private }));
    await waitFor(() => expect(state.cancel).toHaveBeenCalledTimes(1)); expect(state.post).not.toHaveBeenCalled();
  });
  it("preserves the dialog and reports a stale cancel instead of hiding a newer request", async () => {
    const current = fresh(); current.group.request = { requestId: id, generation: id, canCancel: true, groupId: "123", state: "queued", reason: "Waiting" };
    state.get.mockResolvedValue(current); state.cancel.mockRejectedValue(new ApiError("Request changed", 409, "Conflict"));
    const user = await open(); await user.click(screen.getByRole("button", { name: en.infoflow_conversations.cancel_group }));
    expect(await screen.findByText("Request changed")).toBeInTheDocument(); expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(state.cancel).toHaveBeenCalledTimes(1);
  });
});
