import { z } from "zod";

// Keep decimal identifiers as strings: InfoFlow IDs may exceed JS safe integers.
const groupIdSchema = z.string().regex(/^[1-9][0-9]{0,18}$/).refine(
  (id) => id.length < 19 || id <= "9223372036854775807",
);

export function infoFlowGroupURL(groupId: string): string {
  if (!groupIdSchema.safeParse(groupId).success) throw new Error("Invalid group ID");
  const data = JSON.stringify({ APIName: "BdHiJs.service.hi.open", version: 65, data: { id: groupId, type: "2" } });
  return `infoflow://APICenter?data=${encodeURIComponent(btoa(data))}`;
}

export const InfoFlowGroupsSchema = z.object({
  groups: z.array(z.object({
    group_id: groupIdSchema,
    name: z.string().max(256).optional(),
    url: z.string(),
  })).max(1000),
});

export interface InfoFlowGroup {
  groupId: string;
  name?: string;
  url: string;
}
