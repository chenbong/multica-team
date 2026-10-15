// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";
import { InfoFlowConversationsSchema, infoFlowConversationURL, isInfoFlowConversationWaiting } from "./infoflow-conversations-schema";

const bindingId = "00000000-0000-4000-8000-000000000001";
const requestId = "00000000-0000-4000-8000-000000000002";
const status = () => ({
  enabled: true,
  group: { can_manage: true, binding: { binding_id: bindingId, group_id: "9007199254740993", state: "active", reason: "" }, request: null, groups: [{ group_id: "123", name: "Group" }] },
  private: { eligible: true, can_manage: true, reason: "", bindings: [{ binding_id: bindingId, robot_id: "robot-a", robot_chat_id: "9223372036854775807", robot_name: "Assigned robot", state: "active", reason: "" }], request: null },
});
const client = () => new ApiClient("https://api.example.test");
function respond(body: unknown, status = 200) {
  const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify(body), { status }));
  vi.stubGlobal("fetch", fetch); return fetch;
}
afterEach(() => vi.unstubAllGlobals());

describe("unified InfoFlow conversation API", () => {
  it("preserves coexisting group and own private associations with exact int64 IDs", async () => {
    const fetch = respond(status());
    const result = await client().getIssueInfoFlowConversations("TEST/1");
    expect(result.group.binding).toEqual({ bindingId, groupId: "9007199254740993", state: "active", reason: "" });
    expect(result.private.bindings).toEqual([{ bindingId, robotId: "robot-a", robotChatId: "9223372036854775807", robotName: "Assigned robot", state: "active", reason: "" }]);
    expect(String(fetch.mock.calls[0]?.[0])).toContain("/api/issues/TEST%2F1/infoflow-conversations");
  });
  it("defaults optional fields without granting permissions", async () => {
    respond({});
    expect(await client().getIssueInfoFlowConversations("TEST-1")).toEqual({
      enabled: false, group: { canManage: false, binding: null, request: null, groups: [] },
      private: { eligible: false, canManage: false, reason: "", bindings: [], request: null },
    });
    expect(InfoFlowConversationsSchema.parse({ enabled: true, private: { bindings: [] } }).private.eligible).toBe(false);
  });
  it("retains an active public group but hides private metadata on permission loss", async () => {
    const wire = status(); wire.group.can_manage = false; wire.private.can_manage = false;
    respond(wire); const result = await client().getIssueInfoFlowConversations("TEST-1");
    expect(result.group.binding?.state).toBe("active"); expect(result.group.groups).toEqual([]);
    expect(result.private.bindings).toEqual([]); expect(result.private.request).toBeNull();
  });
  it("does not expose a stale active private link after reassignment to a squad", async () => {
    const wire = status(); wire.private.eligible = false; wire.private.reason = "private_requires_single_agent";
    respond(wire); const result = await client().getIssueInfoFlowConversations("TEST-1");
    expect(result.private.bindings[0]).toMatchObject({ state: "needs_rebind", reason: "private_requires_single_agent" });
  });
  it("hides all destinations and requests when the feature is disabled", async () => {
    respond({ ...status(), enabled: false }); const result = await client().getIssueInfoFlowConversations("TEST-1");
    expect(result.group).toMatchObject({ binding: null, groups: [], canManage: false });
    expect(result.private).toMatchObject({ eligible: false, canManage: false, bindings: [], request: null, reason: "" });
  });
  it.each([
    null, [], { enabled: "true" }, { group: null }, { private: null }, { private: { eligible: 1 } },
    { group: { groups: [{ group_id: 123 }] } },
    { group: { groups: [{ group_id: "123" }, { group_id: "123" }] } },
    { group: { binding: { group_id: "123", state: "future" } } },
    { group: { binding: { group_id: "123", state: "active" }, request: { request_id: requestId, group_id: "456", state: "pending" } } },
    { private: { bindings: [{ binding_id: bindingId, robot_id: "a", state: "future" }] } },
    { private: { bindings: [{ binding_id: "no", robot_id: "a", state: "active" }] } },
    { private: { bindings: [{ binding_id: bindingId, robot_id: "a", state: "active", robot_chat_id: "infoflow://arbitrary" }] } },
    { private: { bindings: [{ binding_id: bindingId, robot_id: "a", state: "active", robot_chat_id: "9223372036854775808" }] } },
    { private: { bindings: [{ binding_id: bindingId, robot_id: "a", state: "active" }, { binding_id: bindingId, robot_id: "b", state: "active" }] } },
    { private: { request: { request_id: requestId, state: "active" } } },
  ])("rejects malformed or ambiguous state %# without a compatibility fallback", async (body) => {
    respond(body); await expect(client().getIssueInfoFlowConversations("TEST-1")).rejects.toThrow("Invalid InfoFlow conversation status");
  });
  it.each([{ kind: "private" }, { kind: "group", group_id: "9007199254740993" }])("sends only the strict target %j", async (target) => {
    const fetch = respond({ enabled: true, private: { can_manage: true, eligible: true, request: { request_id: requestId, state: "queued" } } }, 202);
    const result = await client().bindIssueInfoFlowConversation("TEST-1", target as never);
    expect(fetch.mock.calls[0]?.[1]).toMatchObject({ method: "POST", body: JSON.stringify(target), credentials: "include" });
    expect(isInfoFlowConversationWaiting(result)).toBe(true); expect(result.private.bindings).toEqual([]);
  });
  it.each([
    { kind: "private", sender: "other-user" }, { kind: "private", robot_id: "other-robot" },
    { kind: "private", group_id: "123" }, { kind: "group", group_id: 123 }, { kind: "group", group_id: "01" }, { kind: "unknown" },
  ])("rejects extra fields and arbitrary private scopes %# before network access", async (target) => {
    const fetch = respond(status());
    await expect(client().bindIssueInfoFlowConversation("TEST-1", target as never)).rejects.toThrow("Invalid InfoFlow conversation target");
    expect(fetch).not.toHaveBeenCalled();
  });
  it("unlinks one exact association with no sender or group-wide delete payload", async () => {
    const fetch = respond({ enabled: true });
    await client().unbindIssueInfoFlowConversation("TEST/1", bindingId);
    expect(String(fetch.mock.calls[0]?.[0])).toContain(`/api/issues/TEST%2F1/infoflow-conversations/${bindingId}`);
    expect(fetch.mock.calls[0]?.[1]).toMatchObject({ method: "DELETE", credentials: "include" });
    expect(fetch.mock.calls[0]?.[1].body).toBeUndefined();
  });
  it.each(["", "all", "../other", "group:123"])("refuses non-UUID deletion target %s", async (id) => {
    const fetch = respond(status()); await expect(client().unbindIssueInfoFlowConversation("TEST-1", id)).rejects.toThrow("Invalid InfoFlow conversation binding ID");
    expect(fetch).not.toHaveBeenCalled();
  });
  it.each([401, 403, 409, 503])("preserves HTTP %i without retrying a write", async (httpStatus) => {
    const fetch = respond({ error: "Denied" }, httpStatus);
    await expect(client().bindIssueInfoFlowConversation("TEST-1", { kind: "private" })).rejects.toMatchObject({ status: httpStatus });
    expect(fetch).toHaveBeenCalledTimes(1);
  });
  it.each(["group", "private"] as const)("constructs only the canonical %s AppLink locally", (kind) => {
    const url = new URL(infoFlowConversationURL("9007199254740993", kind));
    expect(url.protocol).toBe("infoflow:"); expect(url.hostname).toBe("APICenter");
    expect(JSON.parse(atob(url.searchParams.get("data")!))).toEqual({ APIName: "BdHiJs.service.hi.open", version: 65,
      data: { id: "9007199254740993", type: kind === "group" ? "2" : "4" } });
  });
  it.each(["0", "-1", "01", "9223372036854775808", "https://evil.test", "infoflow://bad"])("rejects unsafe AppLink ID %s", (id) => {
    expect(() => infoFlowConversationURL(id, "private")).toThrow("Invalid InfoFlow conversation");
  });
  it("ignores arbitrary URLs and unsupported robot metadata from network responses", async () => {
    const wire = status(); respond({ ...wire, private: { ...wire.private, bindings: [{ ...wire.private.bindings[0], url: "https://evil.test", avatar: "https://evil.test" }] } });
    expect((await client().getIssueInfoFlowConversations("TEST-1")).private.bindings[0]).not.toHaveProperty("url");
  });
  it("polls pending private work even with an already active group", async () => {
    const wire = status(); respond({ ...wire, private: { ...wire.private, request: { request_id: requestId, state: "pending" } } });
    expect(isInfoFlowConversationWaiting(await client().getIssueInfoFlowConversations("TEST-1"))).toBe(true);
    respond(status()); expect(isInfoFlowConversationWaiting(await client().getIssueInfoFlowConversations("TEST-1"))).toBe(false);
  });
  it.each(["group", "private"] as const)("preserves own %s cancellation without granting binding permission", async (kind) => {
    const wire = status(); wire.group.can_manage = false; wire.private.can_manage = false; wire.private.eligible = false;
    const request = { request_id: requestId, generation: bindingId, can_cancel: true, state: "queued", reason: "Waiting" };
    respond({ ...wire, group: { ...wire.group, binding: null, request: kind === "group" ? { ...request, group_id: "123" } : null }, private: { ...wire.private, request: kind === "private" ? request : null } });
    const result = await client().getIssueInfoFlowConversations("TEST-1");
    expect(result[kind].canManage).toBe(false);
    expect(result[kind].request).toMatchObject({ requestId, generation: bindingId, canCancel: true });
    expect(result.private.bindings).toEqual([]); expect(result.group.groups).toEqual([]);
  });
  it.each(["group", "private"] as const)("sends exact %s request generation and excludes URL identity from the body", async (kind) => {
    const fetch = respond({ enabled: true });
    await client().cancelIssueInfoFlowRequest("TEST/1", { kind, request_id: requestId, expected_generation: bindingId });
    expect(String(fetch.mock.calls[0]?.[0])).toContain(`/api/issues/TEST%2F1/infoflow-conversation-requests/${requestId}`);
    expect(fetch.mock.calls[0]?.[1]).toMatchObject({ method: "DELETE", body: JSON.stringify({ kind, expected_generation: bindingId }), credentials: "include" });
  });
  it.each([
    { kind: "private", request_id: requestId }, { kind: "private", request_id: requestId, expected_generation: "bad" },
    { kind: "group", request_id: "bad", expected_generation: bindingId },
    { kind: "group", request_id: requestId, expected_generation: bindingId, sender: "forged" },
  ])("rejects unversioned or forged cancellation %j before network access", async (target) => {
    const fetch = respond({ enabled: true }); await expect(client().cancelIssueInfoFlowRequest("TEST-1", target as never)).rejects.toThrow("Invalid InfoFlow conversation request target");
    expect(fetch).not.toHaveBeenCalled();
  });
  it.each(["group", "private"] as const)("rejects cancellable %s metadata without a version", async (kind) => {
    const request = { request_id: requestId, state: "pending", can_cancel: true };
    respond(kind === "group" ? { group: { request: { ...request, group_id: "123" } } } : { private: { request } });
    await expect(client().getIssueInfoFlowConversations("TEST-1")).rejects.toThrow("Invalid InfoFlow conversation status");
  });
});
