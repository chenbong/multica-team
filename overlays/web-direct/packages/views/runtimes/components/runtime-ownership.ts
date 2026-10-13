import type { AgentRuntime, RuntimeProfile } from "@multica/core/types";

// This is a display filter over the server-authorized response, never an
// authorization policy. Filter individual runtimes before machine grouping.
export function runtimeOwnershipView(
  runtimes: AgentRuntime[],
  profiles: RuntimeProfile[],
  userId: string | null | undefined,
  showAll: boolean,
): { runtimes: AgentRuntime[]; profiles: RuntimeProfile[] } {
  if (!userId) return { runtimes: [], profiles: [] };
  if (showAll) return { runtimes, profiles };
  return {
    runtimes: runtimes.filter((runtime) => runtime.owner_id === userId),
    profiles: profiles.filter((profile) => profile.created_by === userId),
  };
}
