import { describe, expect, it } from "vitest";
import { parseIssuePermissions } from "./issue-permissions";

describe("issue deletion permissions", () => {
 it("accepts only an explicit boolean permission", () => {
  expect(parseIssuePermissions({can_delete: true})).toEqual({canDelete: true});
  expect(parseIssuePermissions({can_delete: false})).toEqual({canDelete: false});
  for (const value of [{}, {can_delete: "true"}, null]) {
   expect(parseIssuePermissions(value)).toEqual({canDelete: false});
  }
 });
});
