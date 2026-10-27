// @vitest-environment node
import { describe, expect, it } from "vitest";
import { parseOneAPIQuota } from "./oneapi-quota";
import { parseIssueAdministrators } from "./issue-administrators";

describe("personal quota response boundary", () => {
 it("parses used/total and timestamps", () => {
  expect(parseOneAPIQuota({ status:"ready", values:{is_unlimited:false,used_quota:3800,month_quota_limit:9000,remaining_quota:5200},updated_at:"2026-10-27T00:00:00Z" })).toMatchObject({status:"ready",values:{used:3800,total:9000,remaining:5200}});
 });
 it.each([null,[],{}, {status:"ready",values:{is_unlimited:false,used_quota:"3800"}}, {status:"ready",values:{is_unlimited:false,used_quota:Infinity}}, {status:"ready",updated_at:"invalid"}])("fails safely for malformed data %j", raw => { expect(parseOneAPIQuota(raw).status).toBe("unavailable"); });
 it("allows unlimited with absent amounts without inventing zero",()=>{const quota=parseOneAPIQuota({status:"ready",values:{is_unlimited:true}});expect(quota.values?.unlimited).toBe(true);expect(quota.values?.total).toBeUndefined();});
 it("preserves stale values without turning missing values into zero",()=>{expect(parseOneAPIQuota({status:"stale",values:{is_unlimited:false,used_quota:1,month_quota_limit:2,remaining_quota:1}})).toMatchObject({status:"stale",values:{used:1}});expect(parseOneAPIQuota({status:"not_linked"}).values).toBeUndefined();});
});
describe("administrator response boundary",()=>{
 it("denies deletion and management for malformed responses",()=>{expect(parseIssueAdministrators({administrators:[],can_delete:"yes",can_manage:true})).toMatchObject({canDelete:false,canManage:false});});
 it("parses member identities and independent permissions",()=>{expect(parseIssueAdministrators({administrators:[{user_id:"alice",name:"Alice"}],can_delete:false,can_manage:true})).toEqual({administrators:[{userId:"alice",name:"Alice"}],canDelete:false,canManage:true});});
});
