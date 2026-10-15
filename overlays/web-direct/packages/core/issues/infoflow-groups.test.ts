// @vitest-environment node
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useIssueInfoFlowGroups } from "./infoflow-groups";

const state = vi.hoisted(() => ({ user: "user-a" as string | undefined, query: vi.fn(), get: vi.fn() }));
vi.mock("@tanstack/react-query", () => ({ useQuery: (options: unknown) => state.query(options) }));
vi.mock("../auth", () => ({ useAuthStore: (selector: (value: { user?: { id: string } }) => unknown) => selector({ user: state.user ? { id: state.user } : undefined }) }));
vi.mock("../api", () => ({ api: { getIssueInfoFlowGroups: (id: string) => state.get(id) } }));
beforeEach(() => { vi.clearAllMocks(); state.user = "user-a"; });

describe("issue group query scope", () => {
  it("keys destinations by user, workspace and issue", () => {
    useIssueInfoFlowGroups("workspace-a", "issue-a");
    expect(state.query.mock.calls[0]?.[0].queryKey).toEqual(["issues", "infoflow-groups", "user-a", "workspace-a", "issue-a"]);
    state.user = "user-b";
    useIssueInfoFlowGroups("workspace-b", "issue-b");
    expect(state.query.mock.calls[1]?.[0].queryKey).toEqual(["issues", "infoflow-groups", "user-b", "workspace-b", "issue-b"]);
  });
  it("does not query without an authenticated workspace context", () => {
    state.user = undefined; useIssueInfoFlowGroups("workspace", "issue");
    expect(state.query.mock.calls[0]?.[0].enabled).toBe(false);
    state.user = "user-a"; useIssueInfoFlowGroups("", "issue");
    expect(state.query.mock.calls[1]?.[0].enabled).toBe(false);
  });
  it("refreshes membership-sensitive destinations and uses the checked API", async () => {
    useIssueInfoFlowGroups("workspace", "issue");
    const options = state.query.mock.calls[0]?.[0];
    expect(options.staleTime).toBe(0);
    expect(options.refetchInterval).toBe(30_000);
    expect(options.retry).toBe(false);
    await options.queryFn();
    expect(state.get).toHaveBeenCalledWith("issue");
  });
});
