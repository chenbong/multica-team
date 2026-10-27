import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { useAuthStore } from "../auth";
export function useIssueAdministrators(workspaceId: string, issueId: string) {
 const userId = useAuthStore(s => s.user?.id);
 return useQuery({ queryKey: ["issue-administrators", workspaceId, issueId, userId], queryFn: () => api.getIssueAdministrators(issueId), enabled: !!workspaceId && !!issueId && !!userId, refetchInterval: 10000 });
}
export function useUpdateIssueAdministrators(workspaceId: string, issueId: string) {
 const qc = useQueryClient();
 return useMutation({ mutationFn: (userIds: string[]) => api.updateIssueAdministrators(issueId, userIds), onSuccess: () => qc.invalidateQueries({ queryKey: ["issue-administrators", workspaceId, issueId] }) });
}
