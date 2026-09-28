import { z } from "zod";
export const DuccStatusSchema = z.object({
  available: z.boolean().default(true), username: z.string(), enabled: z.boolean(), imported: z.boolean(),
  version: z.number(), source_daemon: z.string(), pending_daemon: z.string(), imported_at: z.string().nullable(),
  machines: z.array(z.object({daemon_id:z.string(), name:z.string(), supported:z.boolean(), online:z.boolean(), installed:z.boolean().nullable(), state:z.string().nullable(), error_code:z.string().nullable(), last_seen_at:z.string().nullable()})),
});
export type DuccStatus = z.infer<typeof DuccStatusSchema>;
export const emptyDuccStatus: DuccStatus = {available:false,username:"",enabled:false,imported:false,version:0,source_daemon:"",pending_daemon:"",imported_at:null,machines:[]};
