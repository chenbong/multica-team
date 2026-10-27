import { z } from "zod";

export const IssueTransferPlanSchema = z.object({
  affected_issues: z.number().int().positive().default(1),
  issue_ids: z.array(z.string().uuid()).default([]),
  include_subissues: z.boolean().default(false),
  requires_cleanup: z.boolean().default(false),
  status_mappings: z.array(z.object({source:z.string(),name:z.string(),target:z.string()})).default([]),
  warnings: z.array(z.string()).default([]),
  issue_id: z.string().uuid(),
  source_workspace_id: z.string().uuid(),
  target_workspace_id: z.string().uuid(),
  source_identifier: z.string().min(1),
  target_name: z.string().min(1),
  target_slug: z.string().regex(/^[a-zA-Z0-9_-]+$/),
  target_prefix: z.string().min(1),
  target_status: z.string(),
  statuses: z.array(z.object({ key: z.string(), name: z.string() })),
  revision: z.number().int().nonnegative(),
  comments: z.number().int().nonnegative(),
  runs: z.number().int().nonnegative(),
  messages: z.number().int().nonnegative(),
  unassign: z.boolean(),
  retired_subscriptions: z.number().int().nonnegative(),
  blockers: z.array(z.string()),
  fingerprint: z.string().regex(/^[a-f0-9]{64}$/),
  identifier: z.string().optional(),
  path: z.string().optional(),
});
export type IssueTransferPlan = z.infer<typeof IssueTransferPlanSchema>;
export interface IssueTransferRequest {
  target_workspace_id: string;
  target_status?: string;
  fingerprint?: string;
  confirm_unassign?: boolean;
  confirm_cleanup?: boolean;
  include_subissues?: boolean;
  status_mappings?: Record<string,string>;
}
