import { z } from "zod";

// Keep decimal identifiers as strings: InfoFlow IDs may exceed JS safe integers.
export const InfoFlowGroupIdSchema = z.string().regex(/^[1-9][0-9]{0,18}$/).refine(
  (id) => id.length < 19 || id <= "9223372036854775807",
);

export function infoFlowGroupURL(groupId: string): string {
  if (!InfoFlowGroupIdSchema.safeParse(groupId).success) throw new Error("Invalid group ID");
  const data = JSON.stringify({ APIName: "BdHiJs.service.hi.open", version: 65, data: { id: groupId, type: "2" } });
  return `infoflow://APICenter?data=${encodeURIComponent(btoa(data))}`;
}

export const InfoFlowGroupsSchema = z.object({
  groups: z.array(z.object({
    group_id: InfoFlowGroupIdSchema,
    name: z.string().max(256).optional(),
    url: z.string(),
  })).max(1),
});

export interface InfoFlowGroup {
  groupId: string;
  name?: string;
  url: string;
}

export function infoFlowGroupLabel(groupId: string, name?: string): string {
  return name?.trim() ? `${groupId} · ${name.trim()}` : groupId;
}

export const InfoFlowGroupBindingSchema = z.object({
  enabled: z.boolean().default(false),
  can_manage: z.boolean().default(false),
  binding: z.object({
    group_id: InfoFlowGroupIdSchema,
    name: z.string().max(256).default(""),
    state: z.enum(["pending", "active"]),
    reason: z.string().max(256).default(""),
  }).nullable().default(null),
  request: z.object({
    request_id: z.string().uuid(),
    group_id: InfoFlowGroupIdSchema,
    state: z.enum(["queued", "pending", "failed", "active"]),
    reason: z.string().max(256).default(""),
  }).nullable().default(null),
  groups: z.array(z.object({
    group_id: InfoFlowGroupIdSchema,
    name: z.string().max(256).default(""),
  })).max(100).default([]),
}).superRefine((value, context) => {
  if (new Set(value.groups.map((group) => group.group_id)).size !== value.groups.length ||
    (value.binding && value.request && value.binding.group_id !== value.request.group_id)) {
    context.addIssue({ code: "custom", message: "Ambiguous InfoFlow group metadata" });
  }
});

export type InfoFlowGroupBindingWire = z.infer<typeof InfoFlowGroupBindingSchema>;
export interface InfoFlowGroupBindingStatus {
  enabled: boolean;
  canManage: boolean;
  binding: { groupId: string; name?: string; state: "pending" | "active"; reason: string } | null;
  request: { requestId: string; groupId: string; state: "queued" | "pending" | "failed" | "active"; reason: string } | null;
  groups: { groupId: string; name: string }[];
}

export function infoFlowGroupBindingStatus(value: InfoFlowGroupBindingWire): InfoFlowGroupBindingStatus {
  const canManage = value.enabled && value.can_manage;
  const binding = value.enabled && (value.binding?.state === "active" || canManage) ? value.binding : null;
  return {
    enabled: value.enabled, canManage,
    binding: binding ? { groupId: binding.group_id, ...(binding.name ? { name: binding.name } : {}), state: binding.state, reason: binding.reason } : null,
    request: canManage && value.request ? { requestId: value.request.request_id, groupId: value.request.group_id, state: value.request.state, reason: value.request.reason } : null,
    groups: canManage ? value.groups.map((group) => ({ groupId: group.group_id, name: group.name })) : [],
  };
}

export const InfoFlowGroupTargetSchema = z.object({ group_id: InfoFlowGroupIdSchema }).strict();
export type InfoFlowGroupTarget = z.infer<typeof InfoFlowGroupTargetSchema>;

export function parseInfoFlowGroupTarget(input: string): InfoFlowGroupTarget | null {
  const value = input.trim();
  if (InfoFlowGroupIdSchema.safeParse(value).success) return { group_id: value };
  return null;
}

export function isInfoFlowGroupBindingWaiting(status?: InfoFlowGroupBindingStatus): boolean {
  if (!status || status.binding?.state === "active" || status.request?.state === "failed") return false;
  return status.binding?.state === "pending" || !!status.request;
}
