// @vitest-environment node
import {describe,it,expect,vi} from "vitest";
import {parsePersonalSecretValue,parseSecretMetadata,parseSecretList,parseCopySource} from "./platform-expansion";
import {setSchemaLogger} from "./schema";

describe("personal secret API boundaries",()=>{
 it("does not include secret values in malformed-response diagnostics",()=>{
  const warn=vi.fn();setSchemaLogger({warn,info(){},error(){},debug(){}});
  const raw={id:4,value:"fixture-sensitive-do-not-log",secrets:[{value:"fixture-sensitive-do-not-log"}]};
  expect(parseSecretMetadata(raw)).toBeNull();
  expect(parseSecretList(raw)).toEqual({secrets:[]});
  expect(()=>parsePersonalSecretValue(raw)).toThrow("Invalid personal secret response");
  expect(JSON.stringify(warn.mock.calls)).not.toContain(raw.value);
 });
 it("requires a valid value and strips unexpected fields",()=>{
  const raw={id:"11111111-1111-4111-8111-111111111111",name:"ssh",description:"",updated_at:"today",value:"fixture",other:"no"};
  expect(parsePersonalSecretValue(raw)).toEqual({...raw,other:undefined});
 });
 it("allows a source with an existing avatar but does not carry the avatar over",()=>{
  const source=parseCopySource({id:"agent",workspace_id:"ws",name:"Test",avatar_url:"emoji:old"});
  expect(source?.avatar_url).toBeNull();
 });
});
