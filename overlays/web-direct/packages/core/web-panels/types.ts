export type WebPanelScope = "workspace" | "issue";

export interface WebPanelResource {
  scope: WebPanelScope;
  resourceId: string;
}

export interface WebPanelQueryContext {
  userId: string;
  workspaceId: string;
  workspaceSlug: string;
}

export interface ResourceWebPanel extends WebPanelResource {
  id: string;
  workspaceId: string;
  createdBy: string;
  createdByAgentId?: string | null;
  sourceTaskId?: string | null;
  name: string;
  url: string;
  createdAt: string;
  updatedAt: string;
  canManage: boolean;
}

export interface ResourceWebPanelList {
  panels: ResourceWebPanel[];
  canAdd: boolean;
  limit: number;
}

export interface WebPanelInput {
  name: string;
  url: string;
}

export interface WebPanelReference extends WebPanelResource {
  panelId: string;
}
