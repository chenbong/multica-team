// @vitest-environment node
import { expect,it } from "vitest";
import { parseWithFallback } from "./schema";
import { DuccStatusSchema,emptyDuccStatus } from "./ducc-schema";
it("fails closed on malformed credential metadata",()=>{
 expect(parseWithFallback({enabled:"true",machines:null},DuccStatusSchema,emptyDuccStatus,{endpoint:"GET /api/me/ducc"})).toEqual(emptyDuccStatus);
 expect(DuccStatusSchema.parse({...emptyDuccStatus,available:true,credential:"must-not-surface"})).not.toHaveProperty("credential");
});
