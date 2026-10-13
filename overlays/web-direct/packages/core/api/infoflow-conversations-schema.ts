import { z } from "zod";
import { InfoFlowGroupBindingSchema, InfoFlowGroupIdSchema, infoFlowGroupBindingStatus } from "./infoflow-groups-schema";

// Both robot chat and group identifiers are exact signed-int64 decimal strings.
export function infoFlowConversationURL(id: string, kind: "group" | "private"): string {
  if (!InfoFlowGroupIdSchema.safeParse(id).success || (kind !== "group" && kind !== "private")) {
    throw new Error("Invalid InfoFlow conversation");
  }
  const data = JSON.stringify({ APIName: "BdHiJs.service.hi.open", version: 65, data: { id, type: kind === "group" ? "2" : "4" } });
  return `infoflow://APICenter?data=${encodeURIComponent(btoa(data))}`;
}

export const InfoFlowConversationTargetSchema = z.discriminatedUnion("kind", [
  z.object({ kind: z.literal("group"), group_id: InfoFlowGroupIdSchema }).strict(),
  z.object({ kind: z.literal("private") }).strict(),
]);
export type InfoFlowConversationTarget = z.infer<typeof InfoFlowConversationTargetSchema>;
export const InfoFlowConversationBindingIdSchema = z.string().uuid();

// The legacy group's normalizer also hides pending destinations and choices
// after loss of management permission. Public readers retain only active links.
const ConversationGroupSchema = z.object({
  can_manage: z.boolean().default(false),
  binding: z.object({
    binding_id: InfoFlowConversationBindingIdSchema.optional(),
    group_id: InfoFlowGroupIdSchema,
    state: z.enum(["pending", "active"]),
    reason: z.string().max(256).default(""),
  }).nullable().default(null),
  request: InfoFlowGroupBindingSchema.shape.request,
  groups: InfoFlowGroupBindingSchema.shape.groups,
}).superRefine((value, context) => {
  if (new Set(value.groups.map((group) => group.group_id)).size !== value.groups.length ||
    (value.binding && value.request && value.binding.group_id !== value.request.group_id)) {
    context.addIssue({ code: "custom", message: "Ambiguous InfoFlow group metadata" });
  }
});

const ConversationPrivateSchema = z.object({
  eligible: z.boolean().default(false),
  can_manage: z.boolean().default(false),
  reason: z.string().max(256).default(""),
  bindings: z.array(z.object({
    binding_id: InfoFlowConversationBindingIdSchema,
    robot_id: z.string().min(1).max(256),
    robot_name: z.string().max(256).default(""),
    robot_chat_id: z.union([z.literal(""), InfoFlowGroupIdSchema]).default(""),
    state: z.enum(["active", "needs_rebind"]),
    reason: z.string().max(256).default(""),
  })).max(100).default([]),
  request: z.object({
    request_id: z.string().uuid(),
    state: z.enum(["queued", "pending", "failed"]),
    reason: z.string().max(256).default(""),
  }).nullable().default(null),
}).superRefine((value, context) => {
  if (new Set(value.bindings.map((binding) => binding.binding_id)).size !== value.bindings.length) {
    context.addIssue({ code: "custom", message: "Ambiguous InfoFlow private metadata" });
  }
});

export const InfoFlowConversationsSchema = z.object({
  enabled: z.boolean().default(false),
  group: ConversationGroupSchema.default({ can_manage: false, binding: null, request: null, groups: [] }),
  private: ConversationPrivateSchema.default({ eligible: false, can_manage: false, reason: "", bindings: [], request: null }),
});
export type InfoFlowConversationsWire = z.infer<typeof InfoFlowConversationsSchema>;
export interface InfoFlowConversationsStatus {
  enabled: boolean;
  group: {
    canManage: boolean;
    binding: { bindingId?: string; groupId: string; state: "pending" | "active"; reason: string } | null;
    request: { requestId: string; groupId: string; state: "queued" | "pending" | "failed" | "active"; reason: string } | null;
    groups: { groupId: string; name: string }[];
  };
  private: {
    eligible: boolean;
    canManage: boolean;
    reason: string;
    bindings: { bindingId: string; robotId: string; robotName: string; robotChatId: string; state: "active" | "needs_rebind"; reason: string }[];
    request: { requestId: string; state: "queued" | "pending" | "failed"; reason: string } | null;
  };
}

export function infoFlowConversationsStatus(value: InfoFlowConversationsWire): InfoFlowConversationsStatus {
  const group = infoFlowGroupBindingStatus({ ...value.group, enabled: value.enabled });
  const privateCanManage = value.enabled && value.private.can_manage;
  return {
    enabled: value.enabled,
    group: {
      canManage: group.canManage,
      binding: group.binding ? { ...group.binding, bindingId: value.group.binding?.binding_id } : null,
      request: group.request,
      groups: group.groups,
    },
    private: {
      eligible: value.enabled && value.private.eligible,
      canManage: privateCanManage,
      reason: value.enabled ? value.private.reason : "",
      bindings: privateCanManage ? value.private.bindings.map((binding) => ({
        bindingId: binding.binding_id, robotId: binding.robot_id, robotName: binding.robot_name,
        robotChatId: binding.robot_chat_id,
        // Ineligible assignments can retain historical bindings, not active destinations.
        state: value.private.eligible ? binding.state : "needs_rebind",
        reason: binding.reason || (!value.private.eligible ? value.private.reason : ""),
      })) : [],
      request: privateCanManage && value.private.request ? {
        requestId: value.private.request.request_id, state: value.private.request.state, reason: value.private.request.reason,
      } : null,
    },
  };
}

export function isInfoFlowConversationWaiting(status?: InfoFlowConversationsStatus): boolean {
  if (!status?.enabled) return false;
  const groupWaiting = status.group.binding?.state !== "active" && status.group.request?.state !== "failed" &&
    (status.group.binding?.state === "pending" || !!status.group.request);
  const privateWaiting = status.private.request?.state === "queued" || status.private.request?.state === "pending";
  return groupWaiting || privateWaiting;
}
