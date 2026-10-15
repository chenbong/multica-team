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

export const InfoFlowConversationRequestTargetSchema = z.object({
  kind: z.enum(["group", "private"]),
  request_id: z.string().uuid(),
  expected_generation: z.string().uuid(),
}).strict();
export type InfoFlowConversationRequestTarget = z.infer<typeof InfoFlowConversationRequestTargetSchema>;
const RequestFields = {
  request_id: z.string().uuid(),
  reason: z.string().max(256).default(""),
  generation: z.string().uuid().optional(),
  can_cancel: z.boolean().default(false),
};
const versionedCancellation = (value: { can_cancel: boolean; generation?: string }) => !value.can_cancel || !!value.generation;
const GroupRequestSchema = z.object({ ...RequestFields, group_id: InfoFlowGroupIdSchema,
  state: z.enum(["queued", "pending", "failed", "active"]),
}).refine(versionedCancellation, "Missing request generation").nullable().default(null);
const PrivateRequestSchema = z.object({ ...RequestFields,
  state: z.enum(["queued", "pending", "failed"]),
}).refine(versionedCancellation, "Missing request generation").nullable().default(null);

// The legacy group's normalizer also hides pending destinations and choices
// after loss of management permission. Public readers retain only active links.
const ConversationGroupSchema = z.object({
  can_manage: z.boolean().default(false),
  binding: z.object({
    binding_id: InfoFlowConversationBindingIdSchema.optional(),
    group_id: InfoFlowGroupIdSchema,
    name: z.string().max(256).default(""),
    state: z.enum(["pending", "active"]),
    reason: z.string().max(256).default(""),
  }).nullable().default(null),
  request: GroupRequestSchema,
  groups: InfoFlowGroupBindingSchema.shape.groups,
}).superRefine((value, context) => {
  if (new Set(value.groups.map((group) => group.group_id)).size !== value.groups.length ||
    (value.binding && value.request && value.binding.group_id !== value.request.group_id)) {
    context.addIssue({ code: "custom", message: "Ambiguous InfoFlow group metadata" });
  }
});

const PrivateRobotSchema = z.object({
  state: z.enum(["unknown", "ready", "missing", "ambiguous", "unavailable"]).catch("unknown"),
  robot_id: z.string().min(1).max(256).optional(),
  name: z.string().max(256).optional(),
  robot_chat_id: z.union([z.literal(""), InfoFlowGroupIdSchema]).optional(),
}).refine((robot) => robot.state !== "ready" || !!(robot.robot_id && robot.name && robot.robot_chat_id), "Incomplete robot preview")
  .default({ state: "unknown" });

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
  request: PrivateRequestSchema,
  robot: PrivateRobotSchema,
}).superRefine((value, context) => {
  if (new Set(value.bindings.map((binding) => binding.binding_id)).size !== value.bindings.length) {
    context.addIssue({ code: "custom", message: "Ambiguous InfoFlow private metadata" });
  }
});

export const InfoFlowConversationsSchema = z.object({
  enabled: z.boolean().default(false),
  group: ConversationGroupSchema.default({ can_manage: false, binding: null, request: null, groups: [] }),
  private: ConversationPrivateSchema.default({ eligible: false, can_manage: false, reason: "", bindings: [], request: null, robot: { state: "unknown" } }),
});
export type InfoFlowConversationsWire = z.infer<typeof InfoFlowConversationsSchema>;
export interface InfoFlowConversationsStatus {
  enabled: boolean;
  group: {
    canManage: boolean;
    binding: { bindingId?: string; groupId: string; name?: string; state: "pending" | "active"; reason: string } | null;
    request: { requestId: string; groupId: string; state: "queued" | "pending" | "failed" | "active"; reason: string; generation?: string; canCancel?: boolean } | null;
    groups: { groupId: string; name: string }[];
  };
  private: {
    eligible: boolean;
    canManage: boolean;
    reason: string;
    bindings: { bindingId: string; robotId: string; robotName: string; robotChatId: string; state: "active" | "needs_rebind"; reason: string }[];
    request: { requestId: string; state: "queued" | "pending" | "failed"; reason: string; generation?: string; canCancel?: boolean } | null;
    robot?: { state: "unknown" | "ready" | "missing" | "ambiguous" | "unavailable"; robotId?: string; name?: string; robotChatId?: string };
  };
}

export function infoFlowConversationsStatus(value: InfoFlowConversationsWire): InfoFlowConversationsStatus {
  const group = infoFlowGroupBindingStatus({ ...value.group, enabled: value.enabled });
  const privateCanManage = value.enabled && value.private.can_manage;
  const groupRequest = value.group.request;
  const privateRequest = value.private.request;
  const groupCancel = value.enabled && groupRequest?.can_cancel && !!groupRequest.generation && ["queued", "pending"].includes(groupRequest.state);
  const privateCancel = value.enabled && privateRequest?.can_cancel && !!privateRequest.generation && ["queued", "pending"].includes(privateRequest.state);
  return {
    enabled: value.enabled,
    group: {
      canManage: group.canManage,
      binding: group.binding ? { ...group.binding, bindingId: value.group.binding?.binding_id } : null,
      request: groupRequest && (group.request || groupCancel) ? {
        requestId: groupRequest.request_id, groupId: groupRequest.group_id, state: groupRequest.state, reason: groupRequest.reason,
        ...(groupRequest.generation ? { generation: groupRequest.generation } : {}), ...(groupCancel ? { canCancel: true } : {}),
      } : null,
      groups: group.groups,
    },
    private: {
      eligible: value.enabled && value.private.eligible,
      canManage: privateCanManage,
      reason: value.enabled ? value.private.reason : "",
      robot: privateCanManage && value.private.eligible ? {
        state: value.private.robot.state,
        ...(value.private.robot.robot_id ? { robotId: value.private.robot.robot_id } : {}),
        ...(value.private.robot.name ? { name: value.private.robot.name } : {}),
        ...(value.private.robot.robot_chat_id ? { robotChatId: value.private.robot.robot_chat_id } : {}),
      } : { state: "unknown" },
      bindings: privateCanManage ? value.private.bindings.map((binding) => ({
        bindingId: binding.binding_id, robotId: binding.robot_id, robotName: binding.robot_name,
        robotChatId: binding.robot_chat_id,
        // Ineligible assignments can retain historical bindings, not active destinations.
        state: value.private.eligible ? binding.state : "needs_rebind",
        reason: binding.reason || (!value.private.eligible ? value.private.reason : ""),
      })) : [],
      request: privateRequest && (privateCanManage || privateCancel) ? {
        requestId: privateRequest.request_id, state: privateRequest.state, reason: privateRequest.reason,
        ...(privateRequest.generation ? { generation: privateRequest.generation } : {}), ...(privateCancel ? { canCancel: true } : {}),
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
