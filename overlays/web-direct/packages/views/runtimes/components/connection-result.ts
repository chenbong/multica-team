import type { AgentRuntime } from "@multica/core/types";

export function hasOnlineRegistration(payload: unknown): boolean {
  if (!payload || typeof payload !== "object") return false;
  const runtimes = (payload as Record<string, unknown>).runtimes;
  return Array.isArray(runtimes) && runtimes.some(row => row && typeof row === "object" && typeof row.id === "string" && row.status === "online" && row.runtime_mode === "local");
}

export function findNewConnectedRuntime(rows: AgentRuntime[], knownComputers: Set<string>, workspace: string, owner: string | undefined): AgentRuntime | undefined {
  if (!owner) return undefined;
  return rows.find(row => row.runtime_mode === "local" && row.status === "online" && row.workspace_id === workspace && row.owner_id === owner && !!row.daemon_id && !knownComputers.has(row.daemon_id));
}
