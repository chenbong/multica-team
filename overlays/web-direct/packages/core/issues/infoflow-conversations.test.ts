// @vitest-environment node
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useBindIssueInfoFlowConversation, useIssueInfoFlowConversations, useUnbindIssueInfoFlowConversation, useCancelIssueInfoFlowRequest, useUpdateIssueInfoFlowSync } from "./infoflow-conversations";
import { ApiError } from "../api";

const state = vi.hoisted(() => ({ user: "user-a" as string | undefined, query: vi.fn(), mutation: vi.fn(), get: vi.fn(), post: vi.fn(), remove: vi.fn(), cancel: vi.fn(), sync: vi.fn(), set: vi.fn(), invalidate: vi.fn() }));
vi.mock("@tanstack/react-query", () => ({ useQuery: (options: unknown) => state.query(options), useMutation: (options: unknown) => state.mutation(options), useQueryClient: () => ({ setQueryData: state.set, invalidateQueries: state.invalidate }) }));
vi.mock("../auth", () => ({ useAuthStore: Object.assign((selector: (value: { user?: { id: string } }) => unknown) => selector({ user: state.user ? { id: state.user } : undefined }), { getState: () => ({ user: state.user ? { id: state.user } : undefined }) }) }));
vi.mock("../api", async (original) => ({ ...await original<typeof import("../api")>(), api: { getIssueInfoFlowConversations: state.get, bindIssueInfoFlowConversation: state.post, unbindIssueInfoFlowConversation: state.remove, cancelIssueInfoFlowRequest: state.cancel, updateIssueInfoFlowSync: state.sync } }));
beforeEach(() => { vi.clearAllMocks(); state.user = "user-a"; });
const fresh = () => ({ enabled: true, group: { canManage: true, binding: null, request: null, groups: [] }, private: { eligible: true, canManage: true, reason: "", bindings: [], request: null } });
describe("unified InfoFlow conversation query and mutations", () => {
  it("saves a versioned sync switch without updating another account's cache",async()=>{
    state.sync.mockResolvedValue(fresh());useUpdateIssueInfoFlowSync("workspace","issue");
    const options=state.mutation.mock.calls[0]?.[0],scope=options.onMutate();
    await options.mutationFn({bindingId:"binding",expectedGeneration:"generation",syncEnabled:false});
    expect(state.sync).toHaveBeenCalledWith("issue","binding",{expected_generation:"generation",sync_enabled:false});
    expect(state.set).not.toHaveBeenCalled();
    state.user="user-b";options.onSuccess(fresh(),{},scope);
    expect(state.set).not.toHaveBeenCalled();
    expect(()=>options.mutationFn({})).toThrow("Authentication changed");
  });
  it("isolates private destinations by account, workspace and issue", () => {
    useIssueInfoFlowConversations("workspace-a", "issue-a");
    expect(state.query.mock.calls[0]?.[0].queryKey).toEqual(["issues", "infoflow-conversations", "user-a", "workspace-a", "issue-a"]);
    state.user = "user-b"; useIssueInfoFlowConversations("workspace-b", "issue-b");
    expect(state.query.mock.calls[1]?.[0].queryKey).toEqual(["issues", "infoflow-conversations", "user-b", "workspace-b", "issue-b"]);
  });
  it("does not query without authenticated workspace context", () => {
    state.user = undefined; useIssueInfoFlowConversations("workspace", "issue"); expect(state.query.mock.calls[0]?.[0].enabled).toBe(false);
    state.user = "user-a"; useIssueInfoFlowConversations("", "issue"); expect(state.query.mock.calls[1]?.[0].enabled).toBe(false);
  });
  it("polls every five seconds for either pending mode, thirty seconds when settled", () => {
    useIssueInfoFlowConversations("workspace", "issue"); const options = state.query.mock.calls[0]?.[0];
    const pendingPrivate = { ...fresh(), private: { ...fresh().private, request: { requestId: "r", state: "pending", reason: "" } } };
    expect(options.refetchInterval({ state: { data: pendingPrivate } })).toBe(5_000);
    expect(options.refetchInterval({ state: { data: fresh() } })).toBe(30_000);
    expect(options.refetchInterval({ state: { error: new Error("Denied"), data: pendingPrivate } })).toBe(false);
    expect(options.retry).toBe(false);
  });
  it("erases private and group cache on background read failure", async () => {
    state.get.mockRejectedValue(new ApiError("Denied", 403, "Forbidden"));
    useIssueInfoFlowConversations("workspace", "issue"); const options = state.query.mock.calls[0]?.[0];
    await expect(options.queryFn()).rejects.toMatchObject({ status: 403 });
    expect(state.set.mock.calls[0]?.[1]).toMatchObject({ enabled: false, private: { bindings: [], request: null }, group: { binding: null, groups: [] } });
  });
  it("refuses a late former-account GET result", async () => {
    state.get.mockImplementation(async () => { state.user = "user-b"; return fresh(); });
    useIssueInfoFlowConversations("workspace", "issue");
    await expect(state.query.mock.calls[0]?.[0].queryFn()).rejects.toThrow("Authentication changed");
    expect(state.set.mock.calls[0]?.[0]).toEqual(["issues", "infoflow-conversations", "user-a", "workspace", "issue"]);
  });
  it.each([useBindIssueInfoFlowConversation, useUnbindIssueInfoFlowConversation, useCancelIssueInfoFlowRequest])("does not let a late mutation populate a switched account", (hook) => {
    hook("workspace", "issue"); const options = state.mutation.mock.calls[0]?.[0]; const scope = options.onMutate();
    state.user = "user-b"; options.onSuccess(fresh(), { kind: "private" }, scope);
    expect(state.set).not.toHaveBeenCalled(); expect(state.invalidate).not.toHaveBeenCalled();
    expect(() => options.mutationFn({ kind: "private" })).toThrow("Authentication changed");
  });
  it.each([useBindIssueInfoFlowConversation, useUnbindIssueInfoFlowConversation, useCancelIssueInfoFlowRequest])("scrubs a denied mutation and refreshes current permissions", (hook) => {
    hook("workspace", "issue"); const options = state.mutation.mock.calls[0]?.[0];
    options.onError(new ApiError("Denied", 403, "Forbidden"), "binding", options.onMutate());
    expect(state.set.mock.calls[0]?.[1]).toMatchObject({ private: { bindings: [] }, group: { binding: null } });
    expect(state.invalidate).toHaveBeenCalledWith({ queryKey: ["issues", "infoflow-conversations", "user-a", "workspace", "issue"] });
  });
  it("binds only this issue and invalidates unified plus legacy observers", async () => {
    state.post.mockResolvedValue(fresh()); useBindIssueInfoFlowConversation("workspace", "issue"); const options = state.mutation.mock.calls[0]?.[0];
    await options.mutationFn({ kind: "private" }); expect(state.post).toHaveBeenCalledWith("issue", { kind: "private" });
    options.onSuccess(fresh(), { kind: "private" }, options.onMutate());
    expect(state.invalidate).toHaveBeenCalledTimes(3); expect(options.retry).toBe(false);
  });
  it("unlinks only the selected association, never a conversation-wide scope", async () => {
    state.remove.mockResolvedValue(fresh()); useUnbindIssueInfoFlowConversation("workspace", "issue"); const options = state.mutation.mock.calls[0]?.[0];
    await options.mutationFn("exact-binding"); expect(state.remove).toHaveBeenCalledWith("issue", "exact-binding");
  });
  it("cancels exactly one admission version and refreshes all observers without optimistic removal", async () => {
    const target = { kind: "private", request_id: "request", expected_generation: "generation" };
    state.cancel.mockResolvedValue(fresh()); useCancelIssueInfoFlowRequest("workspace", "issue");
    const options = state.mutation.mock.calls[0]?.[0];
    await options.mutationFn(target); expect(state.cancel).toHaveBeenCalledWith("issue", target);
    expect(state.set).not.toHaveBeenCalled(); options.onSuccess(fresh(), target, options.onMutate());
    expect(state.invalidate).toHaveBeenCalledTimes(3); expect(options.retry).toBe(false);
  });
  it("does not invent success after a conflict", () => {
    useBindIssueInfoFlowConversation("workspace", "issue"); const options = state.mutation.mock.calls[0]?.[0];
    options.onError(new ApiError("Changed", 409, "Conflict"), { kind: "private" }, options.onMutate());
    expect(state.set).not.toHaveBeenCalled(); expect(state.invalidate).toHaveBeenCalledTimes(1);
  });
});
