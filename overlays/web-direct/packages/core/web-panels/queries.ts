import { useMutation, useQueryClient, queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import type { WebPanelInput, WebPanelQueryContext, WebPanelResource } from "./types";

export const webPanelKeys = {
  workspace: (context: WebPanelQueryContext) => ["resource-web-panels", context.userId, context.workspaceId] as const,
  resource: (context: WebPanelQueryContext, resource: WebPanelResource) =>
    [...webPanelKeys.workspace(context), resource.scope, resource.resourceId] as const,
};

export function webPanelListOptions(context: WebPanelQueryContext, resource: WebPanelResource) {
  return queryOptions({
    queryKey: webPanelKeys.resource(context, resource),
    queryFn: ({ signal }) => api.listResourceWebPanels(resource, context.workspaceSlug, signal),
    enabled: !!(context.userId && context.workspaceId && context.workspaceSlug && resource.resourceId),
    staleTime: 15_000,
    retry: false,
    refetchOnWindowFocus: true,
  });
}

export function useWebPanelMutations(context: WebPanelQueryContext, resource: WebPanelResource) {
  const client = useQueryClient();
  const refresh = () => client.invalidateQueries({ queryKey: webPanelKeys.resource(context, resource) });
  return {
    create: useMutation({ mutationFn: (input: WebPanelInput) => api.createResourceWebPanel(resource, input, context.workspaceSlug), onSuccess: refresh }),
    update: useMutation({ mutationFn: ({ panelId, input }: { panelId: string; input: WebPanelInput }) => api.updateResourceWebPanel(resource, panelId, input, context.workspaceSlug), onSuccess: refresh }),
    remove: useMutation({ mutationFn: (panelId: string) => api.deleteResourceWebPanel(resource, panelId, context.workspaceSlug), onSuccess: refresh }),
  };
}
