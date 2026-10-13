// @vitest-environment node
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useBindIssueInfoFlowGroup, useIssueInfoFlowGroupBinding } from "./infoflow-group-binding";
import { ApiError } from "../api";

const state = vi.hoisted(() => ({ user: "user-a" as string | undefined, query: vi.fn(), mutation: vi.fn(), get: vi.fn(), post: vi.fn(), set: vi.fn(), invalidate: vi.fn() }));
vi.mock("@tanstack/react-query", () => ({ useQuery: (options: unknown) => state.query(options), useMutation: (options: unknown) => state.mutation(options), useQueryClient: () => ({ setQueryData: state.set, invalidateQueries: state.invalidate }) }));
vi.mock("../auth", () => ({ useAuthStore: Object.assign((selector: (value: { user?: { id: string } }) => unknown) => selector({ user: state.user ? { id: state.user } : undefined }), { getState: () => ({ user: state.user ? { id: state.user } : undefined }) }) }));
vi.mock("../api", async (original) => ({ ...await original<typeof import("../api")>(), api: { getIssueInfoFlowGroupBinding: state.get, bindIssueInfoFlowGroup: state.post } }));
beforeEach(() => { vi.clearAllMocks(); state.user = "user-a"; });
describe("web group binding query and mutation", () => {
  it("isolates status by account, workspace and issue", () => {
    useIssueInfoFlowGroupBinding("workspace-a", "issue-a");
    expect(state.query.mock.calls[0]?.[0].queryKey).toEqual(["issues", "infoflow-group-binding", "user-a", "workspace-a", "issue-a"]);
    state.user = "user-b"; useIssueInfoFlowGroupBinding("workspace-b", "issue-b");
    expect(state.query.mock.calls[1]?.[0].queryKey).toEqual(["issues", "infoflow-group-binding", "user-b", "workspace-b", "issue-b"]);
  });
  it("does not query without authenticated workspace context", () => {
    state.user = undefined; useIssueInfoFlowGroupBinding("workspace", "issue");
    expect(state.query.mock.calls[0]?.[0].enabled).toBe(false);
    state.user = "user-a"; useIssueInfoFlowGroupBinding("", "issue");
    expect(state.query.mock.calls[1]?.[0].enabled).toBe(false);
  });
  it("polls every five seconds only while awaiting confirmation", () => {
    useIssueInfoFlowGroupBinding("workspace", "issue");
    const options = state.query.mock.calls[0]?.[0];
    expect(options.staleTime).toBe(0); expect(options.retry).toBe(false);
    const status = { enabled: true, canManage: true, binding: null, request: { state: "queued" }, groups: [] };
    expect(options.refetchInterval({ state: { data: status } })).toBe(5_000);
    expect(options.refetchInterval({ state: { data: { ...status, request: { state: "pending" } } } })).toBe(5_000);
    expect(options.refetchInterval({ state: { data: { ...status, request: { state: "failed" } } } })).toBe(30_000);
    expect(options.refetchInterval({ state: { data: { ...status, binding: { state: "active" } } } })).toBe(30_000);
    expect(options.refetchInterval({ state: { data: status, error: new Error("Denied") } })).toBe(false);
  });
  it("erases cached group metadata after read failures", async () => {
    state.get.mockRejectedValueOnce(new Error("Denied"));
    useIssueInfoFlowGroupBinding("workspace", "issue");
    await expect(state.query.mock.calls[0]?.[0].queryFn()).rejects.toThrow("Denied");
    expect(state.set).toHaveBeenCalledWith(["issues", "infoflow-group-binding", "user-a", "workspace", "issue"], { enabled: false, canManage: false, binding: null, request: null, groups: [] });
  });
  it("awaits a checked write and updates only the scoped caches", async () => {
    useBindIssueInfoFlowGroup("workspace", "issue");
    const options = state.mutation.mock.calls[0]?.[0];
    expect(options.retry).toBe(false);
    await options.mutationFn({ group_id: "123" }); expect(state.post).toHaveBeenCalledWith("issue", { group_id: "123" });
    const data = { enabled: true, canManage: true, binding: null, request: { state: "pending" }, groups: [] };
    options.onSuccess(data, { group_id: "123" }, options.onMutate());
    expect(state.set).toHaveBeenCalledWith(["issues", "infoflow-group-binding", "user-a", "workspace", "issue"], data);
    expect(state.invalidate).toHaveBeenCalledWith({ queryKey: ["issues", "infoflow-groups", "user-a", "workspace", "issue"] });
  });
  it("invalidates a rejected write without inventing a binding", () => {
    useBindIssueInfoFlowGroup("workspace", "issue");
    const options = state.mutation.mock.calls[0]?.[0];
    options.onError(new ApiError("Denied", 403, "Forbidden"), { group_id: "123" }, options.onMutate());
    expect(state.set.mock.calls[0]?.[1]).toMatchObject({ binding: null, request: null, groups: [] });
    expect(state.invalidate).toHaveBeenCalledWith({ queryKey: ["issues", "infoflow-group-binding", "user-a", "workspace", "issue"] });
  });
  it("keeps user input recoverable after a conflict while refreshing state", () => {
    useBindIssueInfoFlowGroup("workspace", "issue");
    const options = state.mutation.mock.calls[0]?.[0];
    options.onError(new ApiError("Conflict", 409, "Conflict"), { group_id: "123" }, options.onMutate());
    expect(state.set).not.toHaveBeenCalled();
    expect(state.invalidate).toHaveBeenCalledWith({ queryKey: ["issues", "infoflow-group-binding", "user-a", "workspace", "issue"] });
  });
  it("does not publish a late write result into a switched account", () => {
    useBindIssueInfoFlowGroup("workspace", "issue");
    const options = state.mutation.mock.calls[0]?.[0]; const scope = options.onMutate();
    state.user = "user-b";
    options.onSuccess({ ...scope, groups: [{ groupId: "123", name: "Private group" }] }, { group_id: "123" }, scope);
    expect(state.set).not.toHaveBeenCalled(); expect(state.invalidate).not.toHaveBeenCalled();
    expect(() => options.mutationFn({ group_id: "123" })).toThrow("Authentication changed");
    expect(state.post).not.toHaveBeenCalled();
  });
});
