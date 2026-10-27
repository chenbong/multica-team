import { z } from "zod";
import { parseWithFallback } from "./schema";

const amount = z.number().finite().nonnegative().nullable().optional();
export const oneAPIQuotaSchema = z.object({
  status: z.string(),
  values: z.object({ is_unlimited: z.boolean(), month_quota_limit: amount, remaining_quota: z.number().finite().nullable().optional(), used_quota: amount }).optional(),
  updated_at: z.string().datetime().optional(),
}).refine(value => !["ready","stale"].includes(value.status) || !!value.values && (value.values.is_unlimited || [value.values.month_quota_limit,value.values.remaining_quota,value.values.used_quota].every(v => typeof v === "number" && Number.isFinite(v)))).transform((value) => ({
  status: value.status,
  values: value.values ? { unlimited: value.values.is_unlimited, total: value.values.month_quota_limit, remaining: value.values.remaining_quota, used: value.values.used_quota } : undefined,
  updatedAt: value.updated_at,
}));
export type OneAPIQuota = z.infer<typeof oneAPIQuotaSchema>;
export function parseOneAPIQuota(raw: unknown): OneAPIQuota {
  return parseWithFallback<OneAPIQuota>(raw, oneAPIQuotaSchema, { status: "unavailable", values: undefined, updatedAt: undefined }, { endpoint: "GET /api/me/oneapi-quota" });
}
