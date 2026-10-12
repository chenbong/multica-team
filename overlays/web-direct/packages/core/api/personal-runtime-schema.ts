import { z } from "zod";

// A destructive confirmation must never silently accept a partial plan.
export const PersonalMachineDeletePlanSchema = z.object({
  ids: z.array(z.string().min(1)).min(1),
  agents: z.array(z.object({
    id: z.string().min(1),
    name: z.string(),
    workspace_id: z.string().min(1),
  })),
}).refine((plan) =>
  new Set(plan.ids).size === plan.ids.length &&
  new Set(plan.agents.map((agent) => agent.id)).size === plan.agents.length,
);

export type PersonalMachineDeletePlan = z.infer<typeof PersonalMachineDeletePlanSchema>;
