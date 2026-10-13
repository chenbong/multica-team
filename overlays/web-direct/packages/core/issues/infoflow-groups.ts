import { useQuery } from "@tanstack/react-query";
import { api } from "../api";
import { useAuthStore } from "../auth";
export type { InfoFlowGroup } from "../api/infoflow-groups-schema";

export function useIssueInfoFlowGroups(workspaceId: string, issueId: string) {
  const userId = useAuthStore((state) => state.user?.id);
  return useQuery({
    queryKey: ["issues", "infoflow-groups", userId, workspaceId, issueId],
    enabled: !!userId && !!workspaceId && !!issueId,
    queryFn: () => api.getIssueInfoFlowGroups(issueId),
    staleTime: 0,
    refetchInterval: 30_000,
    retry: false,
  });
}
