import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { api } from "../api";
export function useDuccCredential(userId: string | undefined) {
  const qc=useQueryClient();
  const query=useQuery({queryKey:["ducc-credential",userId],queryFn:()=>api.getDuccCredential(),enabled:!!userId,refetchInterval:10000});
  const version=query.data?.version;
  useEffect(()=>{if(userId&&version!==undefined)void qc.invalidateQueries({queryKey:["oneapi-quota",userId]});},[qc,userId,version]);
  return query;
}
export function useUpdateDuccCredential(userId: string | undefined) {
  const qc=useQueryClient();
  return useMutation({mutationFn:(input:{action:"enable"|"import"|"remove"; enabled?:boolean; daemon_id?:string})=>api.updateDuccCredential(input),onSettled:()=>qc.invalidateQueries({queryKey:["ducc-credential",userId]})});
}
