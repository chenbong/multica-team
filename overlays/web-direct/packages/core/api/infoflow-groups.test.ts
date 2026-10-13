// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";
import { infoFlowGroupURL } from "./infoflow-groups-schema";

afterEach(() => vi.unstubAllGlobals());
function response(body: unknown) {
  const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify(body)));
  vi.stubGlobal("fetch", fetch);
  return fetch;
}
describe("InfoFlow group destinations", () => {
  it("preserves large IDs and encodes only the group-open action", () => {
    const id = "9007199254740993";
    const url = infoFlowGroupURL(id);
    expect(url).toMatch(/^infoflow:\/\/APICenter\?data=/);
    const decoded = JSON.parse(atob(decodeURIComponent(url.split("data=")[1]!)));
    expect(decoded).toEqual({ APIName: "BdHiJs.service.hi.open", version: 65, data: { id, type: "2" } });
    expect(decoded.data).not.toHaveProperty("draft");
  });
  it("uses the authenticated issue route and deduplicates groups", async () => {
    const fetch = response({ groups: [{ group_id: "123", name: "Squad", url: "unused" }, { group_id: "123", url: "unused" }] });
    expect(await new ApiClient("https://api.example.test").getIssueInfoFlowGroups("TEST-1")).toEqual([
      { groupId: "123", name: "Squad", url: infoFlowGroupURL("123") },
    ]);
    expect(String(fetch.mock.calls[0]?.[0])).toContain("/api/issues/TEST-1/infoflow-groups");
  });
  it.each(["javascript:alert(1)", "https://external.example/", "infoflow://APICenter?data=unsafe"])("never navigates to a server-provided arbitrary URL: %s", async (url) => {
    response({ groups: [{ group_id: "123", url }] });
    expect((await new ApiClient("https://api.example.test").getIssueInfoFlowGroups("TEST-1"))[0]?.url).toBe(infoFlowGroupURL("123"));
  });
  it.each([{}, { groups: null }, { groups: [{}] }, { groups: [{ group_id: 123, url: "" }] }, { groups: [{ group_id: "9223372036854775808", url: "" }] }])("rejects malformed lists %#", async (body) => {
    response(body);
    await expect(new ApiClient("https://api.example.test").getIssueInfoFlowGroups("TEST-1")).rejects.toThrow();
  });
  it.each(["0", "-1", "1e5", "1/2", "123?draft=x", "001", "9223372036854775808"])("rejects invalid group ID %s", (id) => {
    expect(() => infoFlowGroupURL(id)).toThrow();
  });
  it("keeps an empty list empty", async () => {
    response({ groups: [] });
    expect(await new ApiClient("https://api.example.test").getIssueInfoFlowGroups("TEST-1")).toEqual([]);
  });
  it("does not reuse group destinations after access is denied", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("{}", { status: 403 })));
    await expect(new ApiClient("https://api.example.test").getIssueInfoFlowGroups("TEST-1")).rejects.toThrow();
  });
});
