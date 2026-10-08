// @vitest-environment node
import { describe, expect, it } from "vitest";
import { EMPTY_SKILL_FILTERS } from "@multica/core/skills/stores";
import type { SkillSummary } from "@multica/core/types";
import { compareSkillScopes, skillListItems } from "./skill-list-scope";
import { rowMatchesFilters, type SkillRow } from "./skill-list-filter";

const row = (id: string, scope: SkillSummary["scope"], workspace = "current", count = 0): SkillRow => ({
  skill: { id, scope, workspace_id: workspace, name: id, usage_count: count, description: "", config: {}, created_by: null, created_at: "", updated_at: "", labels: [] },
  agents: [], creator: null, runtime: null, originType: "manual", canEdit: true,
});

describe("scope groups with upstream filters", () => {
  it("orders team/workspace/personal first and usage within a scope", () => {
    const rows = [row("personal", "personal", "current", 100), row("less", "team", "current", 1), row("more", "team", "current", 9), row("workspace", undefined)];
    rows.sort((a, b) => compareSkillScopes(a.skill, b.skill) || (b.skill.usage_count ?? 0) - (a.skill.usage_count ?? 0));
    expect(rows.map(r => r.skill.id)).toEqual(["more", "less", "workspace", "personal"]);
    const items = skillListItems(rows);
    expect(items.map(i => i.key)).toEqual(["scope:team", "more", "less", "scope:workspace", "workspace", "scope:personal", "personal"]);
    expect(new Set(items.map(i => i.key)).size).toBe(items.length);
  });

  it("reveals other workspaces only when selected, and still requires matching labels", () => {
    const other = row("other", "workspace", "other-workspace");
    expect(rowMatchesFilters(other, EMPTY_SKILL_FILTERS, "", "current")).toBe(false);
    const filters = { ...EMPTY_SKILL_FILTERS, projects: ["other-workspace"], labels: ["ready"] };
    expect(rowMatchesFilters(other, filters, "", "current")).toBe(false);
    other.skill.labels = [{ id: "ready", name: "Ready", workspace_id: "other-workspace", resource_type: "skill", color: "blue", created_at: "", updated_at: "" }];
    expect(rowMatchesFilters(other, filters, "other", "current")).toBe(true);
    expect(rowMatchesFilters(other, filters, "unrelated", "current")).toBe(false);
    expect(rowMatchesFilters(row("team", "team", "elsewhere"), EMPTY_SKILL_FILTERS, "", "current")).toBe(true);
  });
});
