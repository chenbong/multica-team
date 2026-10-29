"use client";

import type { MemberRole, SkillSummary } from "@multica/core/types";
import { useAuthStore } from "@multica/core/auth";

/** Creator identity is authoritative, independent of workspace membership. */
export function useCanEditSkill(
  skill: SkillSummary | null | undefined,
  _wsId: string,
): boolean {
  const userId = useAuthStore((s) => s.user?.id ?? null);

  if (!skill) return false;
  return canEditSkill(skill, { userId, role: null });
}

/**
 * Non-hook variant for places that already have the role + userId at hand
 * (e.g. list rows that compute role once for the whole page).
 */
export function canEditSkill(
  skill: SkillSummary,
  opts: { userId: string | null; role: MemberRole | null },
): boolean {
  return !!opts.userId && skill.created_by === opts.userId;
}
