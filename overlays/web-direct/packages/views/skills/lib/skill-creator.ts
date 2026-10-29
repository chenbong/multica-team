import type { MemberWithUser, SkillSummary } from "@multica/core/types";

export type SkillCreator = Pick<MemberWithUser, "user_id" | "name" | "avatar_url">;

// The server summary is authoritative across workspace membership boundaries.
// The member fallback keeps older backends usable during rolling upgrades.
export function readSkillCreator(
  skill: SkillSummary | null | undefined,
  members: readonly MemberWithUser[] = [],
): SkillCreator | null {
  return skill?.creator ?? members.find((member) => member.user_id === skill?.created_by) ?? null;
}
