import { describe, expect, it } from "vitest";
import type { SkillSummary } from "@multica/core/types";
import { readSkillCreator } from "./skill-creator";

describe("cross-workspace skill creator identity", () => {
  it("shows the server creator when the current workspace has no such member", () => {
    const creator = { user_id: "outside", name: "Outside creator", avatar_url: null };
    const skill = { created_by: "outside", creator } as SkillSummary;
    expect(readSkillCreator(skill, [])).toEqual(creator);
  });
  it("does not invent a creator for missing legacy identity", () => {
    expect(readSkillCreator({ created_by: null } as SkillSummary)).toBeNull();
  });
});
