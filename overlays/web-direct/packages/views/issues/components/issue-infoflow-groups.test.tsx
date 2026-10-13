import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { InfoFlowGroup } from "@multica/core/issues/infoflow-groups";
import en from "../../locales/en/issues.json";
import { IssueInfoFlowGroups } from "./issue-infoflow-groups";

const state = vi.hoisted(() => ({ data: undefined as InfoFlowGroup[] | undefined, isError: false, hook: vi.fn() }));
vi.mock("@multica/core/issues/infoflow-groups", () => ({ useIssueInfoFlowGroups: (...args: unknown[]) => { state.hook(...args); return state; } }));
vi.mock("../../i18n", () => ({ useT: () => ({ t: (selector: (value: typeof en) => string, values?: { id: string }) => selector(en).replace("{{id}}", values?.id || "") }) }));
beforeEach(() => { state.data = undefined; state.isError = false; vi.clearAllMocks(); });
const view = () => <IssueInfoFlowGroups workspaceId="workspace" issueId="issue" />;

describe("issue group entry", () => {
  it("hides absent/loading and unbound destinations", () => {
    const { rerender } = render(view());
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
    state.data = []; rerender(view());
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    expect(state.hook).toHaveBeenCalledWith("workspace", "issue");
  });
  it("opens a single group with a real user-activated link", () => {
    state.data = [{ groupId: "123", name: "Squad", url: "infoflow://APICenter?data=test" }];
    render(view());
    expect(screen.getByRole("link", { name: "Open InfoFlow group" })).toHaveAttribute("href", state.data[0]!.url);
    expect(screen.getByRole("link")).toHaveAttribute("title", "Squad");
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });
  it("hides stale data when fetching fails or permission is revoked", () => {
    state.data = [{ groupId: "123", url: "infoflow://APICenter?data=test" }]; state.isError = true;
    render(view()); expect(screen.queryByRole("link")).not.toBeInTheDocument();
  });
  it("does not render a dropdown or guess when given multiple groups", () => {
    state.data = [{ groupId: "123", name: "Squad A", url: "infoflow://APICenter?data=a" }, { groupId: "456", url: "infoflow://APICenter?data=b" }];
    render(view());
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });
  it("keeps the single group link keyboard accessible", async () => {
    state.data = [{ groupId: "123", url: "infoflow://APICenter?data=a" }];
    const user = userEvent.setup(); render(view()); await user.tab();
    expect(screen.getByRole("link", { name: "Open InfoFlow group" })).toHaveFocus();
  });
});
