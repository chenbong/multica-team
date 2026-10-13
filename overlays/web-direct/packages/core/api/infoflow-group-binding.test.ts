// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient, ApiError } from "./client";
import { InfoFlowGroupBindingSchema, isInfoFlowGroupBindingWaiting, parseInfoFlowGroupTarget } from "./infoflow-groups-schema";

const requestId = "00000000-0000-4000-8000-000000000001";
const pending = { enabled: true, can_manage: true, binding: null, request: { request_id: requestId, group_id: "9007199254740993", state: "pending", reason: "approval_pending" }, groups: [{ group_id: "123", name: "Squad" }] };
const client = () => new ApiClient("https://api.example.test");
function respond(body: unknown, status = 200) {
  const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify(body), { status }));
  vi.stubGlobal("fetch", fetch); return fetch;
}
afterEach(() => vi.unstubAllGlobals());

describe("web group binding API boundary", () => {
  it("reads checked, camel-case state while preserving int64 IDs", async () => {
    const fetch = respond(pending);
    expect(await client().getIssueInfoFlowGroupBinding("TEST/1")).toEqual({ enabled: true, canManage: true, binding: null,
      request: { requestId, groupId: "9007199254740993", state: "pending", reason: "approval_pending" }, groups: [{ groupId: "123", name: "Squad" }] });
    expect(String(fetch.mock.calls[0]?.[0])).toContain("/api/issues/TEST%2F1/infoflow-group-binding");
  });
  it("accepts a 202 as waiting, never as a verified active binding", async () => {
    const fetch = respond(pending, 202);
    const result = await client().bindIssueInfoFlowGroup("TEST-1", { group_id: "9007199254740993" });
    expect(result.binding).toBeNull(); expect(isInfoFlowGroupBindingWaiting(result)).toBe(true);
    expect(fetch.mock.calls[0]?.[1]).toMatchObject({ method: "POST", body: '{"group_id":"9007199254740993"}', credentials: "include" });
  });
  it("defaults optional fields safely without granting management", () => {
    expect(InfoFlowGroupBindingSchema.parse({})).toEqual({ enabled: false, can_manage: false, binding: null, request: null, groups: [] });
    expect(InfoFlowGroupBindingSchema.parse({ binding: { group_id: "123", state: "pending" }, groups: [{ group_id: "123" }] }).groups).toEqual([{ group_id: "123", name: "" }]);
  });
  it("erases management metadata if permission is false even in a stale response", async () => {
    respond({ ...pending, can_manage: false, binding: { group_id: "9007199254740993", state: "pending" } });
    expect(await client().getIssueInfoFlowGroupBinding("TEST-1")).toEqual({ enabled: true, canManage: false, binding: null, request: null, groups: [] });
  });
  it("retains only a verified active destination for permitted non-managers", async () => {
    respond({ ...pending, can_manage: false, binding: { group_id: "9007199254740993", state: "active" } });
    expect(await client().getIssueInfoFlowGroupBinding("TEST-1")).toEqual({ enabled: true, canManage: false,
      binding: { groupId: "9007199254740993", state: "active", reason: "" }, request: null, groups: [] });
  });
  it("hides stale active destinations when the integration is disabled", async () => {
    respond({ ...pending, enabled: false, binding: { group_id: "9007199254740993", state: "active" } });
    expect(await client().getIssueInfoFlowGroupBinding("TEST-1")).toEqual({ enabled: false, canManage: false, binding: null, request: null, groups: [] });
  });
  it.each([
    null, [], { enabled: "true" }, { can_manage: 1 }, { groups: null }, { groups: [{ group_id: 123 }] },
    { groups: [{ group_id: "123" }, { group_id: "123" }] },
    { groups: Array.from({ length: 101 }, (_, i) => ({ group_id: String(i + 1) })) },
    { binding: { group_id: "123", state: "future" } }, { binding: { group_id: "9223372036854775808", state: "active" } },
    { request: { request_id: "not-uuid", group_id: "123", state: "pending" } },
    { request: { request_id: requestId, group_id: "123", state: "future" } },
    { binding: { group_id: "123", state: "active" }, request: { request_id: requestId, group_id: "456", state: "active" } },
  ])("fails closed on malformed or ambiguous state %#", async (body) => {
    respond(body); await expect(client().getIssueInfoFlowGroupBinding("TEST-1")).rejects.toThrow("Invalid InfoFlow group binding status");
  });
  it.each([403, 409, 503])("preserves write HTTP %i and does not retry", async (status) => {
    const fetch = respond({ error: "Cannot bind this group" }, status);
    await expect(client().bindIssueInfoFlowGroup("TEST-1", { group_id: "123" })).rejects.toMatchObject({ status });
    expect(fetch).toHaveBeenCalledTimes(1);
  });
  it("recognizes the old-backend 404 without treating malformed data as compatibility", async () => {
    respond({}, 404); await expect(client().getIssueInfoFlowGroupBinding("TEST-1")).rejects.toBeInstanceOf(ApiError);
  });
  it.each(["123", "9007199254740993", "9223372036854775807"])("validates exact decimal string %s", (id) => {
    expect(parseInfoFlowGroupTarget(` ${id} `)).toEqual({ group_id: id });
  });
  it.each(["", "0", "-1", "01", "1.2", "1e3", "9223372036854775808", "https://example.test/invite", "邀请你加入群123"])("rejects noncanonical input %s", (input) => {
    expect(parseInfoFlowGroupTarget(input)).toBeNull();
  });
  it("rejects extra write fields without making a network call", async () => {
    const fetch = respond(pending);
    await expect(client().bindIssueInfoFlowGroup("TEST-1", { group_id: "123", sender: "someone" } as never)).rejects.toThrow("Invalid InfoFlow group target");
    expect(fetch).not.toHaveBeenCalled();
  });
  it("treats a request marked active without a binding as still waiting", () => {
    expect(isInfoFlowGroupBindingWaiting({ enabled: true, canManage: true, binding: null, request: { requestId, groupId: "123", state: "active", reason: "" }, groups: [] })).toBe(true);
  });
});
