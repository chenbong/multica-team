import { z } from "zod";
import { parseWithFallback } from "./schema";
import type { Agent } from "../types";

export const personalSecretSchema = z.object({
  id: z.string().uuid(), name: z.string(), description: z.string().default(""), updated_at: z.string(),
});
export type PersonalSecretMetadata = z.infer<typeof personalSecretSchema>;
export const personalSecretListSchema = z.object({ secrets: z.array(personalSecretSchema) });

// Secret values never enter parseWithFallback, whose diagnostics include data.
export function parsePersonalSecretValue(raw: unknown): PersonalSecretMetadata & { value: string } {
  const result = personalSecretSchema.extend({ value: z.string() }).safeParse(raw);
  if (!result.success) throw new Error("Invalid personal secret response");
  return result.data;
}

const copySourceSchema = z.object({
  id:z.string(), workspace_id:z.string(), name:z.string(), description:z.string().default(""),
  instructions:z.string().default(""), system_key:z.string().optional(), avatar_url:z.string().nullable().optional().transform(()=>null),
  runtime_id:z.string().default(""), model:z.string().default(""), thinking_level:z.string().default(""),
  service_tier:z.string().default(""), custom_args:z.array(z.string()).default([]),
  max_concurrent_tasks:z.number().default(1), permission_mode:z.string().default("private"),
  invocation_targets:z.array(z.object({target_type:z.string(),target_id:z.string().optional()})).default([]),
  skills:z.array(z.object({id:z.string(), name:z.string(), description:z.string().default(""),enabled:z.boolean().default(true)})).default([]),
  conversation_starters:z.array(z.object({label:z.string(),prompt:z.string()})).default([]),
});
export function parseCopySource(raw:unknown):Agent|null {
  const result=copySourceSchema.safeParse(raw);
  return parseWithFallback<Agent|null>(result.success?result.data:{},copySourceSchema,null,{endpoint:"agent-copy-source"});
}

export function parseSecretMetadata(raw:unknown):PersonalSecretMetadata|null {
 const parsed=personalSecretSchema.safeParse(raw);
 return parseWithFallback<PersonalSecretMetadata|null>(parsed.success?parsed.data:{},personalSecretSchema,null,{endpoint:"personal-secret-metadata"});
}
export function parseSecretList(raw:unknown):{secrets:PersonalSecretMetadata[]} {
 const parsed=personalSecretListSchema.safeParse(raw);
 return parseWithFallback(parsed.success?parsed.data:{},personalSecretListSchema,{secrets:[]},{endpoint:"personal-secret-list"});
}
