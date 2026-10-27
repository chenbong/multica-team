import { z } from "zod";
import { parseWithFallback } from "./schema";
export const issueAdministratorsSchema = z.object({
 administrators: z.array(z.object({ user_id: z.string(), name: z.string() })),
 can_manage: z.boolean(), can_delete: z.boolean(),
}).transform(value => ({ administrators: value.administrators.map(v => ({ userId: v.user_id, name: v.name })), canManage: value.can_manage, canDelete: value.can_delete }));
export type IssueAdministrators = z.infer<typeof issueAdministratorsSchema>;
export function parseIssueAdministrators(raw: unknown): IssueAdministrators {
 return parseWithFallback<IssueAdministrators>(raw, issueAdministratorsSchema, { administrators: [], canManage: false, canDelete: false }, { endpoint: "issue-administrators" });
}
