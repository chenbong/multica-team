import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
export function useDuccCredential(userId: string | undefined) {
  return useQuery({queryKey:["ducc-credential",userId],queryFn:()=>api.getDuccCredential(),enabled:!!userId,refetchInterval:10000});
}
export function useUpdateDuccCredential(userId: string | undefined) {
  const qc=useQueryClient();
  return useMutation({mutationFn:(input:{action:"enable"|"import"|"remove"; enabled?:boolean; daemon_id?:string})=>api.updateDuccCredential(input),onSettled:()=>qc.invalidateQueries({queryKey:["ducc-credential",userId]})});
}
