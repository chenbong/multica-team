// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";
import { ResourceWebPanelListSchema, ResourceWebPanelSchema, WebPanelChangedSchema } from "./web-panel-schema";

const row = {
  id: "00000000-0000-4000-8000-000000000001", scope: "issue", resource_id: "00000000-0000-4000-8000-000000000002",
  workspace_id: "00000000-0000-4000-8000-000000000003", created_by: "00000000-0000-4000-8000-000000000004",
  name: "Panel", url: "https://pages.example.test/#/board?team=a", created_at: "2026-10-18T00:00:00Z", updated_at: "2026-10-18T00:00:00Z", can_manage: true,
};
afterEach(() => vi.unstubAllGlobals());

describe("web panel wire contracts", () => {
  it("normalizes response fields and does not infer management rights", () => {
    expect(ResourceWebPanelSchema.parse(row)).toMatchObject({ resourceId: row.resource_id, workspaceId: row.workspace_id, canManage: true, url: row.url });
    expect(ResourceWebPanelSchema.parse({ ...row, can_manage: undefined }).canManage).toBe(false);
    expect(ResourceWebPanelListSchema.parse({ panels: [row] }).canAdd).toBe(false);
  });
  it("preserves agent provenance while accepting responses from older servers", () => {
    expect(ResourceWebPanelSchema.parse(row)).toMatchObject({ createdByAgentId: null, sourceTaskId: null });
    expect(ResourceWebPanelSchema.parse({ ...row, created_by_agent_id: row.id, source_task_id: row.resource_id })).toMatchObject({ createdByAgentId: row.id, sourceTaskId: row.resource_id });
    expect(ResourceWebPanelSchema.safeParse({ ...row, created_by_agent_id: "forged" }).success).toBe(false);
    expect(ResourceWebPanelSchema.safeParse({ ...row, source_task_id: "invalid" }).success).toBe(false);
  });
  it("rejects unsafe or malformed server data instead of opening a frame", () => {
    for (const patch of [{ scope: "private" }, { url: "javascript:alert(1)" }, { id: "bad" }, { name: "" }, { can_manage: "yes" }]) expect(ResourceWebPanelSchema.safeParse({ ...row, ...patch }).success).toBe(false);
    expect(WebPanelChangedSchema.safeParse({ scope: "issue", resource_id: row.resource_id, workspace_id: row.workspace_id }).success).toBe(true);
    expect(WebPanelChangedSchema.safeParse({ scope: "issue", resource_id: "bad", workspace_id: row.workspace_id }).success).toBe(false);
  });
  it("supports valid Unicode names using the same character limit as the server", () => {
    expect(ResourceWebPanelSchema.safeParse({ ...row, name: "🌞".repeat(120) }).success).toBe(true);
    expect(ResourceWebPanelSchema.safeParse({ ...row, name: "🌞".repeat(121) }).success).toBe(false);
  });
  it("sends resource URLs with an explicit workspace header and no iframe credentials", async () => {
    const fetch = vi.fn().mockImplementation(() => Promise.resolve(new Response(JSON.stringify({ panels: [row], can_add: true, limit: 50 }), { status: 200, headers: { "Content-Type": "application/json" } })));
    vi.stubGlobal("fetch", fetch);
    const client = new ApiClient("https://api.example.test");
    const resource = { scope: "issue" as const, resourceId: row.resource_id };
    const value = await client.listResourceWebPanels(resource, "expected-workspace");
    expect(value.panels[0]?.url).toBe(row.url);
    expect(fetch.mock.calls[0]?.[0]).toBe(`https://api.example.test/api/issues/${row.resource_id}/web-panels`);
    expect(fetch.mock.calls[0]?.[1]?.headers).toMatchObject({ "X-Workspace-Slug": "expected-workspace" });
  });
  it("reports invalid API responses as query errors rather than a misleading empty list", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ panels: "broken" }), { status: 200, headers: { "Content-Type": "application/json" } })));
    await expect(new ApiClient("https://api.example.test").listResourceWebPanels({ scope: "workspace", resourceId: row.workspace_id }, "acme")).rejects.toThrow("Invalid web panel list");
  });
});
