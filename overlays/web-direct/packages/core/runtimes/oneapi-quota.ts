import { useQuery } from "@tanstack/react-query";
import { api } from "../api";
export const quotaRefreshInterval = 10 * 60 * 1000;
export function useOneAPIQuota(userId: string | undefined) {
  return useQuery({
    queryKey: ["oneapi-quota", userId],
    queryFn: () => api.getOneAPIQuota(),
    enabled: !!userId,
    staleTime: quotaRefreshInterval,
    refetchInterval: quotaRefreshInterval,
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: true,
    retry: false,
  });
}
