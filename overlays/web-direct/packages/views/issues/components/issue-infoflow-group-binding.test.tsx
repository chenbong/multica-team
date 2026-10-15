import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ApiError } from "@multica/core/api";
import { infoFlowGroupURL, type InfoFlowGroupBindingStatus } from "@multica/core/issues/infoflow-group-binding";
import en from "../../locales/en/issues.json";
import { IssueInfoFlowGroupBinding } from "./issue-infoflow-group-binding";

const state = vi.hoisted(() => ({ user: "user-a", get: vi.fn(), post: vi.fn(), legacy: vi.fn() }));
vi.mock("@multica/core/api", async (original) => ({ ...await original<typeof import("@multica/core/api")>(), api: { getIssueInfoFlowGroupBinding: state.get, bindIssueInfoFlowGroup: state.post, getIssueInfoFlowGroups: state.legacy } }));
vi.mock("@multica/core/auth", () => ({ useAuthStore: Object.assign((selector: (value: { user: { id: string } }) => unknown) => selector({ user: { id: state.user } }), { getState: () => ({ user: { id: state.user } }) }) }));
vi.mock("../../i18n", () => ({ useT: () => ({ t: (selector: (value: typeof en) => string, values?: { id: string }) => selector(en).replace("{{id}}", values?.id || "") }) }));
const requestId = "00000000-0000-4000-8000-000000000001";
const fresh = (): InfoFlowGroupBindingStatus => ({ enabled: true, canManage: true, binding: null, request: null, groups: [{ groupId: "123", name: "Existing squad" }] });
const pending = (groupId = "456", reason = "") : InfoFlowGroupBindingStatus => ({ ...fresh(), request: { requestId, groupId, state: "pending", reason } });
let queryClient: QueryClient;
beforeEach(() => {
  vi.clearAllMocks(); state.user = "user-a";
  queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } } });
  state.get.mockReset().mockResolvedValue(fresh()); state.post.mockReset().mockResolvedValue(pending()); state.legacy.mockReset().mockResolvedValue([]);
});
afterEach(() => { cleanup(); queryClient.clear(); });
function view(workspaceId = "workspace", issueId = "issue") {
  return <QueryClientProvider client={queryClient}><IssueInfoFlowGroupBinding key={`${workspaceId}:${issueId}`} workspaceId={workspaceId} issueId={issueId} /></QueryClientProvider>;
}
async function open() {
  render(view()); const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Bind InfoFlow group" }));
  const manual = screen.queryByRole("radio", { name: "Enter a group number" });
  if (manual) await user.click(manual);
  return user;
}
const input = () => screen.getByRole("textbox", { name: "Group number" });

describe("web issue group binding", () => {
  it("keeps the bind action hidden until permissions load", async () => {
    let resolve!: (value: InfoFlowGroupBindingStatus) => void;
    state.get.mockImplementation(() => new Promise<InfoFlowGroupBindingStatus>((done) => { resolve = done; }));
    render(view()); expect(screen.queryByRole("button")).not.toBeInTheDocument();
    await act(async () => { resolve(fresh()); });
    expect(await screen.findByRole("button", { name: "Bind InfoFlow group" })).toBeInTheDocument();
  });
  it.each([{ enabled: false }, { canManage: false }])("requires both enabled and management permission (%j)", async (override) => {
    state.get.mockResolvedValue({ ...fresh(), ...override }); render(view());
    await waitFor(() => expect(queryClient.isFetching()).toBe(0));
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });
  it("uses a labeled dialog with the group visibility and approval warning", async () => {
    await open();
    expect(screen.getByRole("dialog", { name: "Bind InfoFlow group" })).toBeInTheDocument();
    expect(screen.getByText(en.infoflow_groups.warning)).toBeInTheDocument();
    expect(input()).toHaveAttribute("type", "text"); expect(input()).toHaveAttribute("inputmode", "numeric");
    expect(screen.getByRole("button", { name: "Submit binding" })).toBeDisabled();
  });
  it("preserves large typed IDs and waits after 202-equivalent state", async () => {
    const result = pending("9007199254740993", "approval_pending");
    state.post.mockResolvedValue(result);
    const user = await open(); await user.type(input(), "9007199254740993");
    state.get.mockResolvedValue(result); await user.click(screen.getByRole("button", { name: "Submit binding" }));
    await waitFor(() => expect(state.post).toHaveBeenCalledWith("issue", { group_id: "9007199254740993" }));
    expect(await screen.findByText("Waiting for administrator approval")).toBeInTheDocument();
    expect(screen.queryByText("Group bound")).not.toBeInTheDocument(); expect(screen.queryByRole("link")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Submit binding" })).toBeDisabled();
  });
  it("chooses an existing group and sends no group names or user identity", async () => {
    const user = await open(); await user.click(screen.getByRole("radio", { name: "Choose an existing group" }));
    await user.selectOptions(screen.getByRole("combobox", { name: "Select a group" }), "123");
    state.get.mockResolvedValue(pending("123")); state.post.mockResolvedValue(pending("123"));
    await user.click(screen.getByRole("button", { name: "Submit binding" }));
    await waitFor(() => expect(state.post).toHaveBeenCalledWith("issue", { group_id: "123" }));
  });
  it("clears stale dropdown selections when switching to manual entry", async () => {
    const user = await open(); await user.click(screen.getByRole("radio", { name: "Choose an existing group" }));
    await user.selectOptions(screen.getByRole("combobox"), "123");
    await user.click(screen.getByRole("radio", { name: "Enter a group number" })); await user.type(input(), "456");
    state.get.mockResolvedValue(pending()); await user.click(screen.getByRole("button", { name: "Submit binding" }));
    await waitFor(() => expect(state.post).toHaveBeenCalledWith("issue", { group_id: "456" }));
  });
  it("does not restore a stale manual value after toggling modes", async () => {
    const user = await open(); await user.type(input(), "456");
    await user.click(screen.getByRole("radio", { name: "Choose an existing group" }));
    await user.click(screen.getByRole("radio", { name: "Enter a group number" }));
    expect(input()).toHaveValue(""); expect(screen.getByRole("button", { name: "Submit binding" })).toBeDisabled();
  });
  it("cannot submit a stale dropdown choice removed by a fresh status read", async () => {
    const user = await open(); await user.click(screen.getByRole("radio", { name: "Choose an existing group" }));
    await user.selectOptions(screen.getByRole("combobox"), "123");
    state.get.mockResolvedValue({ ...fresh(), groups: [] }); await user.click(screen.getByRole("button", { name: "Refresh status" }));
    await waitFor(() => expect(screen.getByRole("combobox")).toHaveValue(""));
    expect(screen.getByRole("button", { name: "Submit binding" })).toBeDisabled(); expect(state.post).not.toHaveBeenCalled();
  });
  it.each(["01", "9223372036854775808", "https://example.test/invite"])("does not submit invalid group number %s", async (value) => {
    const user = await open(); await user.type(input(), value);
    expect(input()).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByText(en.infoflow_groups.invalid)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Submit binding" })).toBeDisabled(); expect(state.post).not.toHaveBeenCalled();
  });
  it("keeps the manual alternative usable when no groups were discovered", async () => {
    state.get.mockResolvedValue({ ...fresh(), groups: [] }); const user = await open();
    await user.click(screen.getByRole("radio", { name: "Choose an existing group" }));
    expect(screen.getByText(en.infoflow_groups.empty)).toBeInTheDocument();
    await user.click(screen.getByRole("radio", { name: "Enter a group number" }));
    await user.type(input(), "456"); expect(screen.getByRole("button", { name: "Submit binding" })).toBeEnabled();
  });
  it("preserves input and shows conflict errors without closing", async () => {
    state.post.mockRejectedValue(new ApiError("Another group is reserved", 409, "Conflict"));
    const user = await open(); await user.type(input(), "456"); await user.click(screen.getByRole("button", { name: "Submit binding" }));
    expect(await screen.findByText("Another group is reserved")).toBeInTheDocument();
    expect(input()).toHaveValue("456"); expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(state.post).toHaveBeenCalledTimes(1);
  });
  it("locks retries to an existing pending binding", async () => {
    const failed = { ...fresh(), binding: { groupId: "123", state: "pending" as const, reason: "invitation_failed" }, request: { requestId, groupId: "123", state: "failed" as const, reason: "invitation_failed" } };
    state.get.mockResolvedValue(failed); state.post.mockResolvedValue(pending("123")); const user = await open();
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument(); expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
    expect(screen.getByText(en.infoflow_groups.invitation_failed)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Retry binding" }));
    await waitFor(() => expect(state.post).toHaveBeenCalledWith("issue", { group_id: "123" }));
  });
  it("prefills the failed target so an explicit retry needs no retyping", async () => {
    state.get.mockResolvedValue({ ...fresh(), request: { requestId, groupId: "456", state: "failed", reason: "actor_not_in_group" } });
    const user = await open(); expect(input()).toHaveValue("456");
    expect(screen.getByText(en.infoflow_groups.actor)).toBeInTheDocument();
    state.get.mockResolvedValue(pending()); await user.click(screen.getByRole("button", { name: "Retry binding" }));
    await waitFor(() => expect(state.post).toHaveBeenCalledWith("issue", { group_id: "456" }));
  });
  it("drops a former account's candidate names and input when the account changes", async () => {
    const user = await open(); await user.type(input(), "456");
    state.user = "user-b";
    state.get.mockResolvedValue({ ...fresh(), canManage: false, groups: [] });
    // Keep the issue surface mounted while its auth-scoped query changes.
    queryClient.setQueryData(["issues", "infoflow-group-binding", "user-b", "workspace", "issue"], { ...fresh(), canManage: false, groups: [] });
    await act(async () => { queryClient.invalidateQueries({ queryKey: ["issues", "infoflow-group-binding"] }); });
    await waitFor(() => expect(screen.queryByRole("textbox")).not.toBeInTheDocument());
    expect(screen.queryByText("Existing squad · 123")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Submit binding" })).not.toBeInTheDocument();
    expect(state.post).not.toHaveBeenCalled();
  });
  it("resets a draft even when the next account also has binding permission", async () => {
    const rendered = render(view()); const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Bind InfoFlow group" }));
    await user.click(screen.getByRole("radio", { name: "Enter a group number" })); await user.type(input(), "456");
    state.user = "user-b";
    const status = { ...fresh(), groups: [{ groupId: "789", name: "Different group" }] };
    state.get.mockResolvedValue(status);
    queryClient.setQueryData(["issues", "infoflow-group-binding", "user-b", "workspace", "issue"], status);
    rendered.rerender(view());
    expect(screen.queryByText("Existing squad · 123")).not.toBeInTheDocument();
    await user.click(screen.getByRole("radio", { name: "Enter a group number" }));
    expect(input()).toHaveValue(""); expect(state.post).not.toHaveBeenCalled();
  });
  it("only shows success and the canonical link after confirmed active binding", async () => {
    state.get.mockResolvedValue(pending()); const user = await open();
    expect(screen.getByText("Waiting for confirmation")).toBeInTheDocument();
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
    const active = { ...pending(), binding: { groupId: "456", state: "active" as const, reason: "" }, request: null };
    state.get.mockResolvedValue(active); await user.click(screen.getByRole("button", { name: "Refresh status" }));
    expect(await screen.findByText("Group bound")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Open InfoFlow group" })).toHaveAttribute("href", infoFlowGroupURL("456"));
  });
  it("allows permitted readers to open an active group without exposing a bind control", async () => {
    state.get.mockResolvedValue({ ...fresh(), canManage: false, binding: { groupId: "123", state: "active", reason: "" } });
    render(view()); expect(await screen.findByRole("link", { name: "Open InfoFlow group" })).toHaveAttribute("href", infoFlowGroupURL("123"));
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });
  it("does not advertise an active request as successful without a binding", async () => {
    state.get.mockResolvedValue({ ...pending(), request: { requestId, groupId: "456", state: "active", reason: "" } });
    await open(); expect(screen.getByText("Waiting for confirmation")).toBeInTheDocument();
    expect(screen.queryByText("Group bound")).not.toBeInTheDocument(); expect(screen.queryByRole("link")).not.toBeInTheDocument();
  });
  it.each(["future_reason", "__proto__", "constructor", "等待群管理员审批机器人入群"])("renders a server-provided reason as safe text %s", async (reason) => {
    state.get.mockResolvedValue(pending("456", reason)); await open();
    expect(screen.getByText(reason)).toBeInTheDocument();
    expect(screen.queryByText("Group bound")).not.toBeInTheDocument();
  });
  it("preserves a human-readable failed reason from the backend", async () => {
    const reason = "群内没有平台机器人，请先手动拉入机器人。";
    state.get.mockResolvedValue({ ...fresh(), request: { requestId, groupId: "456", state: "failed", reason } });
    await open(); expect(screen.getByText(reason)).toBeInTheDocument();
  });
  it("erases choices and affordances when a status refresh loses permission", async () => {
    const user = await open(); await user.click(screen.getByRole("radio", { name: "Choose an existing group" }));
    state.get.mockRejectedValue(new ApiError("Denied", 403, "Forbidden")); await user.click(screen.getByRole("button", { name: "Refresh status" }));
    expect(await screen.findByText(en.infoflow_groups.read_failed)).toBeInTheDocument();
    expect(screen.queryByRole("combobox")).not.toBeInTheDocument(); expect(screen.queryByText("Existing squad · 123")).not.toBeInTheDocument();
    expect(screen.queryByRole("link")).not.toBeInTheDocument(); expect(screen.queryByRole("button", { name: "Submit binding" })).not.toBeInTheDocument();
    expect(queryClient.getQueryData(["issues", "infoflow-group-binding", "user-a", "workspace", "issue"])).toMatchObject({ groups: [], binding: null });
  });
  it("preserves the old open-link component on an old-backend 404", async () => {
    state.get.mockRejectedValue(new ApiError("Missing", 404, "Not Found"));
    state.legacy.mockResolvedValue([{ groupId: "123", name: "Legacy group", url: infoFlowGroupURL("123") }]);
    render(view()); expect(await screen.findByRole("link", { name: "Open InfoFlow group" })).toHaveAttribute("href", infoFlowGroupURL("123"));
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });
  it("never uses the legacy path to evade a failed permission check", async () => {
    state.get.mockRejectedValue(new ApiError("Denied", 403, "Forbidden"));
    render(view()); await waitFor(() => expect(queryClient.isFetching()).toBe(0));
    expect(state.legacy).not.toHaveBeenCalled(); expect(screen.queryByRole("link")).not.toBeInTheDocument(); expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });
  it("prevents duplicate submits and dismissal while the write is pending", async () => {
    let resolve!: (value: InfoFlowGroupBindingStatus) => void;
    state.post.mockImplementation(() => new Promise<InfoFlowGroupBindingStatus>((done) => { resolve = done; }));
    const user = await open(); await user.type(input(), "456"); await user.dblClick(screen.getByRole("button", { name: "Submit binding" }));
    expect(state.post).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("button", { name: "Submitting binding..." })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Close" })).toBeDisabled();
    await user.keyboard("{Escape}"); expect(screen.getByRole("dialog")).toBeInTheDocument();
    state.get.mockResolvedValue(pending()); await act(async () => { resolve(pending()); });
    expect(await screen.findByText("Waiting for confirmation")).toBeInTheDocument();
  });
  it("restores focus to the opener when closing the dialog", async () => {
    const user = await open(); const dialog = screen.getByRole("dialog");
    await user.click(within(dialog).getAllByRole("button", { name: "Close" }).at(-1)!);
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(screen.getByRole("button", { name: "Bind InfoFlow group" })).toHaveFocus();
  });
});
