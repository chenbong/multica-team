import { z } from "zod";
import { parseWithFallback } from "./schema";

const schema = z.object({ can_delete: z.boolean() }).transform(value => ({ canDelete: value.can_delete }));
export function parseIssuePermissions(raw: unknown): { canDelete: boolean } {
 return parseWithFallback(raw, schema, { canDelete: false }, { endpoint: "issue-permissions" });
}
