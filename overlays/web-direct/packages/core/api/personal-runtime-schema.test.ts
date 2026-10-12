// @vitest-environment node
import { describe, expect, it } from "vitest";
import { PersonalMachineDeletePlanSchema } from "./personal-runtime-schema";
import { parseWithFallback } from "./schema";

const valid = {
  ids: ["runtime-other-workspace", "runtime-current"],
  agents: [{ id: "agent-1", name: "Same name", workspace_id: "workspace-other" }],
};

describe("personal machine deletion plan", () => {
  it("preserves the complete ordered cross-workspace snapshot", () => {
    expect(PersonalMachineDeletePlanSchema.parse(valid)).toEqual(valid);
    expect(PersonalMachineDeletePlanSchema.parse({ ...valid, agents: [] }).agents).toEqual([]);
  });

  it.each([
    null, {}, { ...valid, ids: [] }, { ...valid, agents: null },
    { ...valid, ids: ["same", "same"] },
    { ...valid, agents: [...valid.agents, ...valid.agents] },
    { ...valid, agents: [{ id: "agent-1", name: "Missing workspace" }] },
    { ...valid, agents: [{ ...valid.agents[0], id: 42 }] },
  ])("refuses malformed or incomplete destructive confirmations: %j", (input) => {
    expect(parseWithFallback(input, PersonalMachineDeletePlanSchema, null, { endpoint: "delete-machine-plan" })).toBeNull();
  });
});
