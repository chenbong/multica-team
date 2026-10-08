import type { SkillRow } from "./skill-list-filter";
import type { SkillSummary } from "@multica/core/types";

type Scope = NonNullable<SkillSummary["scope"]>;

export function skillScope(scope: SkillSummary["scope"]): Scope {
  return scope === "team" || scope === "personal" ? scope : "workspace";
}

export function compareSkillScopes(left: SkillSummary, right: SkillSummary): number {
  const rank = { team: 0, workspace: 1, personal: 2 };
  return rank[skillScope(left.scope)] - rank[skillScope(right.scope)];
}

export type SkillListItem =
  | { kind: "scope"; key: string; scope: Scope }
  | { kind: "skill"; key: string; row: SkillRow };

/** Section headers are virtual items too, so scroll offsets include their height. */
export function skillListItems(rows: SkillRow[]): SkillListItem[] {
  const items: SkillListItem[] = [];
  let previous: Scope | undefined;
  for (const row of rows) {
    const scope = skillScope(row.skill.scope);
    if (scope !== previous) {
      items.push({ kind: "scope", key: `scope:${scope}`, scope });
      previous = scope;
    }
    items.push({ kind: "skill", key: row.skill.id, row });
  }
  return items;
}
