import { z } from "zod";
import { webPanelUrl } from "../web-panels/url";

export const ResourceWebPanelSchema = z.object({
  id: z.string().uuid(),
  scope: z.enum(["workspace", "issue"]),
  resource_id: z.string().uuid(),
  workspace_id: z.string().uuid(),
  created_by: z.string().uuid(),
  name: z.string().min(1),
  url: z.string().refine((value) => webPanelUrl(value) !== null),
  created_at: z.string().datetime({ offset: true }),
  updated_at: z.string().datetime({ offset: true }),
  can_manage: z.boolean().default(false),
  created_by_agent_id: z.string().uuid().nullable().default(null),
  source_task_id: z.string().uuid().nullable().default(null),
}).refine((value) => value.name === value.url || [...value.name].length <= 120, {
  path: ["name"], message: "Custom page names are limited to 120 characters",
}).transform((value) => ({
  id: value.id,
  scope: value.scope,
  resourceId: value.resource_id,
  workspaceId: value.workspace_id,
  createdBy: value.created_by,
  name: value.name,
  url: value.url,
  createdAt: value.created_at,
  updatedAt: value.updated_at,
  canManage: value.can_manage,
  createdByAgentId: value.created_by_agent_id,
  sourceTaskId: value.source_task_id,
}));

export const ResourceWebPanelListSchema = z.object({
  panels: z.array(ResourceWebPanelSchema).max(50),
  can_add: z.boolean().default(false),
  limit: z.number().int().positive().max(50).default(50),
}).transform((value) => ({ panels: value.panels, canAdd: value.can_add, limit: value.limit }));

export const WebPanelChangedSchema = z.object({
  scope: z.enum(["workspace", "issue"]),
  resource_id: z.string().uuid(),
  workspace_id: z.string().uuid(),
}).transform((value) => ({ scope: value.scope, resourceId: value.resource_id, workspaceId: value.workspace_id }));
