import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { useAuthStore } from "../auth";
import type { IssueTransferRequest } from "../api/issue-transfer-schema";

export function useIssueTransferPreview(source: string, issue: string, target: string, status: string, includeSubissues = false, statusMappings: Record<string,string> = {}) {
  const user = useAuthStore((s) => s.user?.id);
  return useQuery({
    queryKey: ["issue-transfer", user, source, issue, target, status, includeSubissues, statusMappings],
    enabled: !!user && !!source && !!issue && !!target && target !== source,
    queryFn: () => api.previewIssueTransfer(issue, { target_workspace_id: target, target_status: status, include_subissues: includeSubissues, status_mappings: statusMappings }),
    retry: false,
    refetchOnWindowFocus: false,
    staleTime: 0,
  });
}

export function useTransferIssue(issue: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (request: IssueTransferRequest) => api.transferIssue(issue, request),
    onSuccess: () => {
      for (const key of ["issues", "inbox", "workspaces", "issue-transfer"]) {
        void qc.invalidateQueries({ queryKey: [key] });
      }
    },
  });
}
