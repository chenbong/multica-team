import { z } from "zod";
import { parseWithFallback } from "../api/schema";
import type { AgentRuntime } from "../types/agent";

const personalNameSchema = z.object({
  personal_name: z.string().nullable().optional().catch(null),
});

export function withPersonalName(runtime: AgentRuntime): AgentRuntime {
  const parsed = parseWithFallback(runtime, personalNameSchema, { personal_name: null as string | null | undefined }, { endpoint: "/api/runtimes" });
  return { ...runtime, personal_name: parsed.personal_name ?? null };
}
