// @vitest-environment node
import { describe, expect, it } from "vitest";
import { SkillSchema, SkillSummarySchema } from "./schemas";

describe("custom skill fields across the v0.6.1 API boundary", () => {
  it("keeps labels and custom scope/usage fields together", () => {
    const skill = SkillSummarySchema.parse({
      id: "skill-1", workspace_id: "workspace-1", name: "review", scope: "team",
      workspace_name: "Workspace", usage_count: 17, owner_user_id: "user-1",
      labels: [{ id: "label-1", name: "Ready", workspace_id: "workspace-1", resource_type: "skill", color: "blue", created_at: "", updated_at: "" }],
    });
    expect(skill.scope).toBe("team");
    expect(skill.usage_count).toBe(17);
    expect(skill.workspace_name).toBe("Workspace");
    expect(skill.labels[0]?.id).toBe("label-1");
    expect(skill).not.toHaveProperty("content");
  });

  it.each([{}, { scope: "future", usage_count: "invalid", workspace_name: null, owner_user_id: 7 }])(
    "defaults absent and malformed extension fields without rejecting a skill", (extra) => {
      const skill = SkillSchema.parse({ id: "skill-1", workspace_id: "workspace-1", name: "review", ...extra });
      expect(skill).toMatchObject({ scope: "workspace", usage_count: 0, workspace_name: "", owner_user_id: null, labels: [], content: "", files: [] });
    },
  );
});
