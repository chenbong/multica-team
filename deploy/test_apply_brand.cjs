const {test}=require("node:test");
const assert=require("node:assert/strict");
const fs=require("node:fs");
const os=require("node:os");
const path=require("node:path");
const {spawnSync}=require("node:child_process");

function fixture(t,titleTest){
  const root=fs.mkdtempSync(path.join(os.tmpdir(),"tmesh-brand-test-"));
  t.after(()=>fs.rmSync(root,{recursive:true,force:true}));
  const files={
    "apps/web/package.json":"{}",
    "apps/web/platform/document-title.ts":'export const SITE_TITLE = "Multica";\nexport const TITLE_SUFFIX = " | Multica";\n',
    "apps/web/platform/document-title.test.tsx":titleTest,
    "apps/web/app/manifest.ts":'export default {\n    name: "Multica",\n    short_name: "Multica",\n};\n',
    "apps/web/app/layout.tsx":'export const metadata = {appleWebApp: {capable: true, title: "Multica",},\n    siteName: "Multica",\n};\n',
  };
  for(const locale of ["zh-Hans","en"])files[`packages/views/locales/${locale}/auth.json`]=JSON.stringify({signin:{title:"Old title",description:"Keep original instructions"},verify:{title_link:"Fixture robot"}});
  for(const [rel,content] of Object.entries(files)){
    const dest=path.join(root,rel);fs.mkdirSync(path.dirname(dest),{recursive:true});fs.writeFileSync(dest,content);
  }
  function run(...args){return spawnSync(process.execPath,[path.join(__dirname,"apply-brand.cjs"),root,...args],{encoding:"utf8",env:{...process.env,MULTICA_BRAND_NAME:"Acme Mesh",MULTICA_LOGIN_TITLE:"Acme: collaboration",MULTICA_BRAND_BRIDGE_URL:""}})}
  return {root,files,run,read:rel=>fs.readFileSync(path.join(root,rel),"utf8")};
}

test("configured branding supports dynamic suffix expectations and preserves login instructions",t=>{
  const titleTest='import { TITLE_SUFFIX } from "./document-title";\nexpect(value).toBe(`Task${TITLE_SUFFIX}`);\n';
  const f=fixture(t,titleTest),result=f.run();
  assert.equal(result.status,0,result.stderr);
  assert.match(f.read("apps/web/platform/document-title.ts"),/TITLE_SUFFIX = " \| Acme Mesh"/);
  assert.equal(f.read("apps/web/platform/document-title.test.tsx"),titleTest);
  for(const locale of ["zh-Hans","en"]){
    const auth=JSON.parse(f.read(`packages/views/locales/${locale}/auth.json`));
    assert.equal(auth.signin.title,"Acme: collaboration");assert.equal(auth.signin.description,"Keep original instructions");assert.equal(auth.verify.title_link,"Fixture robot");
  }
  assert.match(f.read("apps/web/app/manifest.ts"),/short_name: "Acme Mesh"/);
});

test("configured branding still updates legacy literal expectations",t=>{
  const f=fixture(t,'expect(value).toBe("Task | Multica");\n');
  assert.equal(f.run().status,0);
  assert.match(f.read("apps/web/platform/document-title.test.tsx"),/Task \| Acme Mesh/);
});

test("unknown title tests fail before writing any branding",t=>{
  const f=fixture(t,'expect(value).toBe("unknown");\n');
  assert.notEqual(f.run().status,0);
  for(const [rel,content] of Object.entries(f.files))assert.equal(f.read(rel),content);
});

test("check mode validates dynamic tests without writing files",t=>{
  const f=fixture(t,'import { TITLE_SUFFIX } from "./document-title";\nexpect(value).toBe(`Task${TITLE_SUFFIX}`);\n');
  assert.equal(f.run("--check").status,0);
  for(const [rel,content] of Object.entries(f.files))assert.equal(f.read(rel),content);
});
