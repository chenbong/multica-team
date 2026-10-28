import { useQuery } from "@tanstack/react-query";
import { api } from "../api";
import { useAuthStore } from "../auth";

export function useIssuePermissions(workspaceId: string, issueId: string) {
 const userId = useAuthStore(s => s.user?.id);
 return useQuery({ queryKey: ["issue-permissions", workspaceId, issueId, userId], queryFn: () => api.getIssuePermissions(issueId), enabled: !!workspaceId && !!issueId && !!userId, refetchInterval: 10000 });
}
