// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";

afterEach(() => vi.unstubAllGlobals());

describe("personal runtime deletion API", () => {
  it("reads every alias and posts the exact confirmed IDs to the selected runtime", async () => {
    const plan = { ids: ["alias-other", "runtime"], agents: [{ id: "agent", name: "Assistant", workspace_id: "other" }] };
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify(plan)))
      .mockResolvedValueOnce(new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");
    expect(await client.personalMachineDeletePlan("runtime")).toEqual(plan);
    await client.deleteOfflineMachine("runtime", plan.ids, plan.agents.map((agent) => agent.id));
    expect(fetchMock.mock.calls[0]?.[0]).toBe("https://api.example.test/api/runtimes/runtime/delete-machine-plan");
    expect(fetchMock.mock.calls[1]?.[0]).toBe("https://api.example.test/api/runtimes/runtime/delete-offline-machine");
    expect(fetchMock.mock.calls[1]?.[1].method).toBe("POST");
    expect(JSON.parse(fetchMock.mock.calls[1]?.[1].body)).toEqual({ expected_runtime_ids: plan.ids, expected_active_agent_ids: ["agent"] });
  });

  it.each([null, { ids: [], agents: [] }, { ids: ["wrong-runtime"], agents: [] }, { ids: ["runtime"] }])("refuses an invalid confirmation: %j", async (body) => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify(body)));
    vi.stubGlobal("fetch", fetchMock);
    await expect(new ApiClient("https://api.example.test").personalMachineDeletePlan("runtime")).rejects.toThrow("Invalid deletion plan");
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});
