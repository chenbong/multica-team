import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api";
import { useAuthStore } from "../auth";
import { isInfoFlowGroupBindingWaiting, type InfoFlowGroupBindingStatus, type InfoFlowGroupTarget } from "../api/infoflow-groups-schema";

export { infoFlowGroupURL, infoFlowGroupLabel, parseInfoFlowGroupTarget, isInfoFlowGroupBindingWaiting } from "../api/infoflow-groups-schema";
export type { InfoFlowGroupBindingStatus, InfoFlowGroupTarget } from "../api/infoflow-groups-schema";

export const infoFlowGroupBindingKeys = {
  status: (userId: string | undefined, workspaceId: string, issueId: string) =>
    ["issues", "infoflow-group-binding", userId, workspaceId, issueId] as const,
};

const hiddenStatus: InfoFlowGroupBindingStatus = { enabled: false, canManage: false, binding: null, request: null, groups: [] };

export function isInfoFlowGroupBindingUnavailable(error: unknown): boolean {
  return error instanceof ApiError && error.status === 404;
}

export function useIssueInfoFlowGroupBinding(workspaceId: string, issueId: string) {
  const userId = useAuthStore((state) => state.user?.id);
  const queryClient = useQueryClient();
  const queryKey = infoFlowGroupBindingKeys.status(userId, workspaceId, issueId);
  return useQuery({
    queryKey,
    enabled: !!userId && !!workspaceId && !!issueId,
    queryFn: async () => {
      try { return await api.getIssueInfoFlowGroupBinding(issueId); }
      catch (error) {
        // Erase membership-sensitive cached metadata, including after a
        // background refetch failure. The error still propagates to the UI.
        queryClient.setQueryData(queryKey, hiddenStatus);
        throw error;
      }
    },
    staleTime: 0,
    retry: false,
    refetchInterval: (query) => query.state.error ? false : isInfoFlowGroupBindingWaiting(query.state.data) ? 5_000 : 30_000,
  });
}

export function useBindIssueInfoFlowGroup(workspaceId: string, issueId: string) {
  const userId = useAuthStore((state) => state.user?.id);
  const queryClient = useQueryClient();
  const queryKey = infoFlowGroupBindingKeys.status(userId, workspaceId, issueId);
  return useMutation({
    mutationKey: queryKey,
    // No automatic write retries: re-submission is an explicit user action.
    retry: false,
    onMutate: () => ({ userId, workspaceId, issueId }),
    mutationFn: (target: InfoFlowGroupTarget) => {
      if (!userId || useAuthStore.getState().user?.id !== userId) throw new Error("Authentication changed");
      return api.bindIssueInfoFlowGroup(issueId, target);
    },
    onSuccess: (data, _target, scope) => {
      // A late write from a former account must not populate the new account's
      // cache, even if the observer's options changed during the request.
      if (!scope || useAuthStore.getState().user?.id !== scope.userId) return;
      const scopedKey = infoFlowGroupBindingKeys.status(scope.userId, scope.workspaceId, scope.issueId);
      queryClient.setQueryData(scopedKey, data);
      void queryClient.invalidateQueries({ queryKey: ["issues", "infoflow-groups", scope.userId, scope.workspaceId, scope.issueId] });
      void queryClient.invalidateQueries({ queryKey: scopedKey });
    },
    onError: (error, _target, scope) => {
      // Refresh permissions and reserved-group state after conflicts or a
      // rejected write; never keep an optimistic binding in the cache.
      if (!scope) return;
      const scopedKey = infoFlowGroupBindingKeys.status(scope.userId, scope.workspaceId, scope.issueId);
      if (error instanceof ApiError && (error.status === 401 || error.status === 403)) {
        queryClient.setQueryData(scopedKey, hiddenStatus);
      }
      if (useAuthStore.getState().user?.id === scope.userId) void queryClient.invalidateQueries({ queryKey: scopedKey });
    },
  });
}
