import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api";
import { useAuthStore } from "../auth";
import { isInfoFlowConversationWaiting, type InfoFlowConversationsStatus, type InfoFlowConversationTarget } from "../api/infoflow-conversations-schema";

export { infoFlowConversationURL, isInfoFlowConversationWaiting } from "../api/infoflow-conversations-schema";
export { parseInfoFlowGroupTarget } from "../api/infoflow-groups-schema";
export type { InfoFlowConversationsStatus, InfoFlowConversationTarget } from "../api/infoflow-conversations-schema";

export const infoFlowConversationKeys = {
  status: (userId: string | undefined, workspaceId: string, issueId: string) =>
    ["issues", "infoflow-conversations", userId, workspaceId, issueId] as const,
};
const hiddenStatus: InfoFlowConversationsStatus = {
  enabled: false,
  group: { canManage: false, binding: null, request: null, groups: [] },
  private: { eligible: false, canManage: false, reason: "", bindings: [], request: null },
};

export function isInfoFlowConversationsUnavailable(error: unknown): boolean {
  return error instanceof ApiError && error.status === 404;
}

export function useIssueInfoFlowConversations(workspaceId: string, issueId: string) {
  const userId = useAuthStore((state) => state.user?.id);
  const queryClient = useQueryClient();
  const queryKey = infoFlowConversationKeys.status(userId, workspaceId, issueId);
  return useQuery({
    queryKey,
    enabled: !!userId && !!workspaceId && !!issueId,
    queryFn: async () => {
      try {
        const result = await api.getIssueInfoFlowConversations(issueId);
        if (useAuthStore.getState().user?.id !== userId) throw new Error("Authentication changed");
        return result;
      } catch (error) {
        queryClient.setQueryData(queryKey, hiddenStatus);
        throw error;
      }
    },
    staleTime: 0,
    retry: false,
    refetchInterval: (query) => query.state.error ? false : isInfoFlowConversationWaiting(query.state.data) ? 5_000 : 30_000,
  });
}

function useConversationMutation<T>(workspaceId: string, issueId: string, write: (issueId: string, input: T) => Promise<InfoFlowConversationsStatus>) {
  const userId = useAuthStore((state) => state.user?.id);
  const queryClient = useQueryClient();
  return useMutation({
    mutationKey: infoFlowConversationKeys.status(userId, workspaceId, issueId),
    retry: false,
    onMutate: () => ({ userId, workspaceId, issueId }),
    mutationFn: (input: T) => {
      if (!userId || !workspaceId || !issueId || useAuthStore.getState().user?.id !== userId) throw new Error("Authentication changed");
      return write(issueId, input);
    },
    onSuccess: (data, _input, scope) => {
      if (!scope || useAuthStore.getState().user?.id !== scope.userId) return;
      const queryKey = infoFlowConversationKeys.status(scope.userId, scope.workspaceId, scope.issueId);
      queryClient.setQueryData(queryKey, data);
      // Existing clients may still have a group-only observer mounted.
      for (const namespace of ["infoflow-groups", "infoflow-group-binding", "infoflow-conversations"]) {
        void queryClient.invalidateQueries({ queryKey: ["issues", namespace, scope.userId, scope.workspaceId, scope.issueId] });
      }
    },
    onError: (error, _input, scope) => {
      if (!scope) return;
      const queryKey = infoFlowConversationKeys.status(scope.userId, scope.workspaceId, scope.issueId);
      if (error instanceof ApiError && (error.status === 401 || error.status === 403)) queryClient.setQueryData(queryKey, hiddenStatus);
      if (useAuthStore.getState().user?.id === scope.userId) void queryClient.invalidateQueries({ queryKey });
    },
  });
}

export function useBindIssueInfoFlowConversation(workspaceId: string, issueId: string) {
  return useConversationMutation<InfoFlowConversationTarget>(workspaceId, issueId, (id, target) => api.bindIssueInfoFlowConversation(id, target));
}

export function useUnbindIssueInfoFlowConversation(workspaceId: string, issueId: string) {
  return useConversationMutation<string>(workspaceId, issueId, (id, bindingId) => api.unbindIssueInfoFlowConversation(id, bindingId));
}
