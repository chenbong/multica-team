import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ApiError } from "@multica/core/api";
import { infoFlowConversationURL, type InfoFlowConversationsStatus } from "@multica/core/issues/infoflow-conversations";
import en from "../../locales/en/issues.json";
import { BindInfoFlowConversation } from "./bind-infoflow-conversation";

const state = vi.hoisted(() => ({ user: "user-a", get: vi.fn(), post: vi.fn(), remove: vi.fn(), cancel: vi.fn(), sync: vi.fn(), legacy: vi.fn() }));
vi.mock("@multica/core/api", async (original) => ({ ...await original<typeof import("@multica/core/api")>(), api: {
  getIssueInfoFlowConversations: state.get, bindIssueInfoFlowConversation: state.post, unbindIssueInfoFlowConversation: state.remove,
  cancelIssueInfoFlowRequest: state.cancel,
  updateIssueInfoFlowSync: state.sync,
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
  state.sync.mockReset().mockResolvedValue(fresh());
  state.legacy.mockReset().mockResolvedValue({ enabled: false, canManage: false, binding: null, request: null, groups: [] });
});
afterEach(() => { cleanup(); client.clear(); });
function view(managementURL?: string) { return <QueryClientProvider client={client}><BindInfoFlowConversation workspaceId="ws" issueId="task" managementURL={managementURL} /></QueryClientProvider>; }
async function open(managementURL?: string) {
  render(view(managementURL)); const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: en.infoflow_conversations.bind })); return user;
}

describe("unified InfoFlow dialog", () => {
  it.each(["group", "private"] as const)("uses the same bordered card and inline action row for %s", async (kind) => {
    const current = fresh();
    current.group.binding = { bindingId: id, groupId: "123", name: "Existing group", state: "active", reason: "", generation: id };
    current.private.bindings = [{ bindingId: id, robotId: "robot", robotName: "Existing robot", robotChatId: "456", state: "active", reason: "", generation: id }];
    state.get.mockResolvedValue(current);
    const user = await open();
    if (kind === "private") await user.click(screen.getByRole("tab", { name: en.infoflow_conversations.private }));
    const card = screen.getByRole("region", { name: kind === "group" ? `${en.infoflow_groups.target}：123 · Existing group` : "Existing robot" });
    expect(card).toHaveClass("rounded-lg", "border", "p-3", "min-w-0");
    const openLink = within(card).getByRole("link", { name: kind === "group" ? en.infoflow_groups.open : en.infoflow_conversations.open_private });
    const remove = within(card).getByRole("button", { name: kind === "group" ? en.infoflow_conversations.unbind_group : `${en.infoflow_conversations.unbind_private} Existing robot` });
    expect(openLink.parentElement).toBe(remove.parentElement);
    expect(openLink.parentElement).toHaveClass("flex", "flex-wrap", "items-center", "gap-2");
    expect(within(card).getByRole("switch", { name: kind === "group" ? en.infoflow_conversations.sync_group : en.infoflow_conversations.sync_private })).toBeChecked();
    expect(within(card).getByText(kind === "group" ? en.infoflow_groups.success : en.infoflow_conversations.private_success)).toBeVisible();
  });
  it("saves each sync switch immediately and leaves the other destination unchanged", async () => {
    const current=fresh();
    current.group.binding={bindingId:id,groupId:"123",name:"Group",state:"active",reason:"",generation:id,syncEnabled:true};
    current.private.bindings=[{bindingId:id,robotId:"r",robotName:"Robot",robotChatId:"456",state:"active",reason:"",generation:id,syncEnabled:true}];
    const paused={...current,group:{...current.group,binding:{...current.group.binding,syncEnabled:false}}};
    let persisted=current;
    state.get.mockImplementation(async()=>persisted);
    state.sync.mockImplementation(async()=>{persisted=paused;return persisted;});
    const user=await open();
    await user.click(screen.getByRole("switch",{name:en.infoflow_conversations.sync_group}));
    await waitFor(()=>expect(state.sync).toHaveBeenCalledWith("task",id,{expected_generation:id,sync_enabled:false}));
    await waitFor(()=>expect(screen.getByRole("switch",{name:en.infoflow_conversations.sync_group})).not.toBeChecked());
    await user.click(screen.getByRole("tab",{name:en.infoflow_conversations.private}));
    expect(screen.getByRole("switch",{name:en.infoflow_conversations.sync_private})).toBeChecked();
  });
  it("keeps the persisted switch state and displays an error after a failed save", async () => {
    const current=fresh();
    current.group.binding={bindingId:id,groupId:"123",state:"active",reason:"",generation:id,syncEnabled:true};
    state.get.mockResolvedValue(current);state.sync.mockRejectedValue(new Error("Unable to save fixture"));
    const user=await open();
    await user.click(screen.getByRole("switch",{name:en.infoflow_conversations.sync_group}));
    expect(await screen.findByRole("alert")).toHaveTextContent("Unable to save fixture");
    expect(screen.getByRole("switch",{name:en.infoflow_conversations.sync_group})).toBeChecked();
  });
  it("allows readers to open the bound group without granting sync permission", async () => {
    const current=fresh();current.group.canManage=false;current.private.canManage=false;
    current.group.binding={bindingId:id,groupId:"123",state:"active",reason:"",generation:id,syncEnabled:false};
    state.get.mockResolvedValue(current);const user=await open();
    expect(screen.getByRole("link",{name:en.infoflow_groups.open})).toBeInTheDocument();
    expect(screen.getByRole("switch",{name:en.infoflow_conversations.sync_group})).toHaveAttribute("aria-disabled","true");
    await user.click(screen.getByRole("switch",{name:en.infoflow_conversations.sync_group}));
    expect(state.sync).not.toHaveBeenCalled();
  });
  it("keeps the cross-conversation disclosure visible for both binding types without another confirmation", async () => {
    const user = await open();
    expect(screen.getByText(en.infoflow_conversations.sync_scope)).toBeVisible();
    await user.click(screen.getByRole("tab", { name: en.infoflow_conversations.private }));
    expect(screen.getByText(en.infoflow_conversations.sync_scope)).toBeVisible();
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  });
  it("keeps opening links and sync disclosure inside the dialog only", async () => {
    const current = fresh();
    current.group.binding = { bindingId: id, groupId: "123", name: "Existing group", state: "active", reason: "" };
    state.get.mockResolvedValue(current);
    render(view());
    const user=userEvent.setup();
    await screen.findByRole("button",{name:en.infoflow_conversations.bind});
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
    expect(screen.queryByText(en.infoflow_conversations.sync_short)).not.toBeInTheDocument();
    expect(screen.queryByText(en.infoflow_conversations.sync_scope)).not.toBeInTheDocument();
    await user.click(screen.getByRole("button",{name:en.infoflow_conversations.bind}));
    expect(screen.getByRole("link",{name:en.infoflow_groups.open})).toBeInTheDocument();
    expect(state.post).not.toHaveBeenCalled();
  });
  it("previews the assigned robot name before private binding", async () => {
    const current = fresh(); current.private.robot = { state: "ready", robotId: "assigned", name: "My fixture robot", robotChatId: "123" };
    state.get.mockResolvedValue(current); const user = await open();
    await user.click(screen.getByRole("tab", { name: en.infoflow_conversations.private }));
    expect(screen.getByText(`${en.infoflow_conversations.private_robot}：My fixture robot`)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: en.infoflow_groups.submit })).toBeEnabled();
    expect(state.post).not.toHaveBeenCalled();
  });
  it.each(["missing", "ambiguous", "unavailable"] as const)("blocks new private binding when robot is %s without removing an existing binding", async (robotState) => {
    const current = fresh(); current.private.robot = { state: robotState };
    state.get.mockResolvedValue(current); const user = await open("https://bridge.example.test");
    await user.click(screen.getByRole("tab", { name: en.infoflow_conversations.private }));
    expect(screen.getByRole("button", { name: en.infoflow_groups.submit })).toBeDisabled();
    if (robotState === "missing") expect(screen.getByRole("link", { name: en.infoflow_conversations.bind_robot_first })).toHaveAttribute("href", "https://bridge.example.test");
    const active = { ...current, private: { ...current.private, bindings: [{ bindingId: id, robotId: "old", robotChatId: "123", robotName: "Existing robot", state: "active" as const, reason: "" }] } };
    state.get.mockResolvedValue(active); await user.click(screen.getByRole("button", { name: en.infoflow_groups.refresh }));
    const remove = await screen.findByRole("button", { name: `${en.infoflow_conversations.unbind_private} Existing robot` });
    expect(screen.getAllByRole("link", { name: en.infoflow_conversations.open_private }).length).toBeGreaterThan(0);
    await user.click(remove); await waitFor(() => expect(state.remove).toHaveBeenCalledWith("task", id));
    expect(state.post).not.toHaveBeenCalled();
  });
  it("does not invent a management URL in an unconfigured checkout", async () => {
    const current = fresh(); current.private.robot = { state: "missing" }; state.get.mockResolvedValue(current);
    const user = await open(); await user.click(screen.getByRole("tab", { name: en.infoflow_conversations.private }));
    expect(screen.getByText(en.infoflow_conversations.bind_robot_first)).toBeInTheDocument();
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
  });
  it("ignores leaked preview metadata after management permission loss", async () => {
    const current = fresh(); current.private.canManage = false;
    current.private.robot = { state: "ready", name: "Hidden robot", robotId: "secret", robotChatId: "123" }; state.get.mockResolvedValue(current);
    const user = await open(); await user.click(screen.getByRole("tab", { name: en.infoflow_conversations.private }));
    expect(screen.queryByText(/Hidden robot/)).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: en.infoflow_groups.submit })).not.toBeInTheDocument();
  });
  it("shows the exact group number and name in choices and verified binding details", async () => {
    const user = await open();
    expect(screen.getByRole("option", { name: "123 · Existing group" })).toHaveValue("123");
    const active = fresh(); active.group.binding = { bindingId: id, groupId: "123", name: "Renamed group", state: "active", reason: "" };
    state.get.mockResolvedValue(active);
    await user.click(screen.getByRole("button", { name: en.infoflow_groups.refresh }));
    expect(await screen.findByText(`${en.infoflow_groups.target}：123 · Renamed group`)).toBeInTheDocument();
    expect(screen.getByTitle("123 · Renamed group")).toHaveAttribute("href", infoFlowConversationURL("123", "group"));
    expect(state.post).not.toHaveBeenCalled();
  });
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
    const user=await open();
    expect(screen.getByRole("link", { name: en.infoflow_groups.open })).toHaveAttribute("href", infoFlowConversationURL("123", "group"));
    await user.click(screen.getByRole("tab",{name:en.infoflow_conversations.private}));
    expect(screen.getByRole("link", { name: en.infoflow_conversations.open_private })).toHaveAttribute("href", infoFlowConversationURL("9007199254740993", "private"));
    expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
  });
  it("sends only the selected exact group number", async () => {
    const user = await open(); await user.selectOptions(screen.getByRole("combobox"), "123");
    await user.click(screen.getByRole("button", { name: en.infoflow_groups.submit }));
    await waitFor(() => expect(state.post).toHaveBeenCalledWith("task", { kind: "group", group_id: "123" }));
  });
  it.each([true, false])("shows one private binding constraint for eligible=%s without repeated onboarding copy", async (eligible) => {
    const current = fresh();
    current.private = { ...current.private, eligible, canManage: eligible,
      reason: eligible ? "" : "私聊仅支持由单个智能体负责的任务；小队、人工负责人或未分配任务请使用群聊" };
    state.get.mockResolvedValue(current);
    const user = await open(); await user.click(screen.getByRole("tab", { name: en.infoflow_conversations.private }));
    expect(screen.getByRole("dialog")).toHaveAccessibleDescription(en.infoflow_conversations.private_restriction);
    expect(screen.getAllByText(en.infoflow_conversations.private_restriction)).toHaveLength(1);
    for (const copy of [en.infoflow_conversations.my_private, en.infoflow_conversations.private_routing,
      en.infoflow_conversations.description, en.infoflow_conversations.no_manage, current.private.reason]) {
      if (copy) expect(screen.queryByText(copy)).not.toBeInTheDocument();
    }
    if (!eligible) expect(screen.queryByRole("button", { name: en.infoflow_groups.submit })).not.toBeInTheDocument();
    expect(state.post).not.toHaveBeenCalled();
    await user.click(screen.getByRole("tab", { name: en.infoflow_conversations.group }));
    expect(screen.getByRole("dialog")).toHaveAccessibleDescription(en.infoflow_conversations.description);
  });
  it("retains actual permission notices for eligible private tasks", async () => {
    const current = fresh(); current.private.canManage = false;
    state.get.mockResolvedValue(current); const user = await open();
    await user.click(screen.getByRole("tab", { name: en.infoflow_conversations.private }));
    expect(screen.getByText(en.infoflow_conversations.no_manage)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: en.infoflow_groups.submit })).not.toBeInTheDocument();
  });
  it("retains unrelated service errors even before private eligibility is resolved", async () => {
    const current = fresh(); current.private = { ...current.private, eligible: false, canManage: false, reason: "Unable to verify your InfoFlow account" };
    state.get.mockResolvedValue(current); const user = await open();
    await user.click(screen.getByRole("tab", { name: en.infoflow_conversations.private }));
    expect(screen.getByText(current.private.reason)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: en.infoflow_groups.submit })).not.toBeInTheDocument();
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
