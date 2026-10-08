const assert=require('node:assert/strict');
const test=require('node:test');
const postcss=require('postcss');
const plugin=require('./postcss.cjs');
async function branches(input){const result=await postcss([plugin()]).process(input,{from:undefined});return {modern:result.root.first,legacy:result.root.last};}
test('modern styles retained, branches are complementary feature tests',async()=>{
  const input='@layer theme,utilities;@layer theme{:root{--bg:oklch(.9 .1 120)}}@layer utilities{.a{color:var(--bg)}}';
  const {modern,legacy}=await branches(input);
  assert.equal(modern.params,plugin.MODERN);assert.equal(legacy.params,'not ('+plugin.MODERN+')');
  const unchanged=modern.clone();unchanged.last.remove();assert.equal(unchanged.nodes.map(n=>n.toString()).join(''),postcss.parse(input).nodes.map(n=>n.toString()).join(''));
  let layers=0;legacy.walkAtRules('layer',()=>layers++);assert.equal(layers,0);
  assert.match(legacy.toString(),/--bg:rgb\(/);
});
test('property defaults survive without outranking utilities',async()=>{
  const {legacy}=await branches('@layer properties,utilities;@property --tw-x{syntax:"*";inherits:false;initial-value:0px}@layer utilities{.a{--tw-x:5px}}');
  assert.match(legacy.toString(),/--tw-x:0px/);assert.match(legacy.toString(),/--tw-x:5px/);
  assert(!legacy.toString().includes('@property'));
});
test('translations, viewport units, scale and original transform compose',async()=>{
  const {legacy}=await branches('.a{translate:-50% calc(-50% + 2px);scale:2 3;rotate:20deg;transform:skew(5deg);height:100dvh}');
  const css=legacy.toString();assert.match(css,/--mc-translate:translate\(-50%, calc\(-50% \+ 2px\)\)/);
  assert.match(css,/--mc-scale:scale\(2, 3\)/);assert.match(css,/--mc-transform:skew\(5deg\)/);assert.match(css,/height:100vh/);
});
test('has selectors produce polyfill attributes for dynamic content',async()=>{
  const {legacy}=await branches('.card:has([data-state="open"]){color:red}');assert.match(legacy.toString(),/csstools-has-/);
});
test('runtime and CSS gate remain identical',()=>{
  assert(require('fs').readFileSync(__dirname+'/runtime.js','utf8').includes(plugin.MODERN));
});
test('transparent theme colors retain alpha instead of becoming opaque',async()=>{
  const {legacy}=await branches('@layer theme{:root{--success:oklch(.6 .15 140)}.dark{--success:oklch(.8 .1 140)}}@layer utilities{.tint{background:var(--success)}@supports (color:color-mix(in lab,red,red)){.tint{background:color-mix(in oklab,var(--success) 5%,transparent)}}}');
  const css=legacy.toString();assert.match(css,/--success-mc-rgb:/);assert.match(css,/rgba\(var\(--success-mc-rgb\), 0.05\)/);assert(!css.includes('@supports (color:color-mix'));
});
test('structuredClone fallback preserves rich data and cycles',()=>{
  const {spawnSync}=require('node:child_process');
  const result=spawnSync(process.execPath,['-e',`
    const assert=require('node:assert/strict');delete global.structuredClone;
    require('./es-builtins.cjs');
    const source={map:new Map([['a',new Set([1,2])]]),date:new Date('2026-01-01'),bytes:new Uint8Array([3,4])};source.self=source;
    const copy=structuredClone(source);
    assert.notEqual(copy,source);assert.equal(copy.self,copy);
    assert(copy.map.get('a') instanceof Set);assert.equal(copy.date.getTime(),source.date.getTime());
    assert.deepEqual([...copy.bytes],[3,4]);assert.throws(()=>structuredClone(()=>{}));
  `],{cwd:__dirname,encoding:'utf8'});
  assert.equal(result.status,0,result.stderr);
});
test('Chrome 97 missing array methods are available before route code',()=>{
  const {spawnSync}=require('node:child_process');
  const result=spawnSync(process.execPath,['-e',`
    const assert=require('node:assert/strict');
    for(const key of ['toSorted','findLast','findLastIndex'])delete Array.prototype[key];
    require('./es-builtins.cjs');
    const source=Object.freeze([3,1,2]);
    assert.deepEqual(source.toSorted((a,b)=>a-b),[1,2,3]);
    assert.deepEqual(source,[3,1,2]);
    assert.deepEqual([3,,1].toSorted(),[1,3,undefined]);
    const tasks=[{rank:2,id:'b'},{rank:1,id:'a'},{rank:1,id:'c'}];
    assert.deepEqual(tasks.toSorted((a,b)=>a.rank-b.rank).map(x=>x.id),['a','c','b']);
    const steps=[{kind:'text',value:''},{kind:'call',pending:true},{kind:'text',value:'done'}];
    assert.equal(steps.findLast(x=>x.kind!=='text'||x.value.trim()).value,'done');
    assert.equal(steps.findLast(x=>x.pending).kind,'call');
    assert.equal([1,1000,1000000].findLastIndex(x=>x<=9000),1);
    assert.equal([].findLast(()=>true),undefined);
    assert.equal([].findLastIndex(()=>true),-1);
    assert.equal(Object.keys([]).includes('toSorted'),false);
  `],{cwd:__dirname,encoding:'utf8'});
  assert.equal(result.status,0,result.stderr);
});
test('modern native array methods are retained',()=>{
  const {spawnSync}=require('node:child_process');
  const result=spawnSync(process.execPath,['-e',`
    const assert=require('node:assert/strict');
    const keys=['toSorted','findLast','findLastIndex'];
    const native=keys.map(key=>Array.prototype[key]);
    require('./es-builtins.cjs');
    keys.forEach((key,i)=>assert.equal(Array.prototype[key],native[i]));
  `],{cwd:__dirname,encoding:'utf8'});
  assert.equal(result.status,0,result.stderr);
});
test('lazy task editor CSS can use tokens from the root stylesheet',async()=>{
  const theme=await branches(':root{--foreground:oklch(.2 .01 280);--muted:var(--foreground)}');
  const editor=await branches('.editor code{background:var(--foreground)}@supports (color:color-mix(in srgb,red,red)){.editor code{background:color-mix(in srgb,var(--foreground) 3%,transparent);color:color-mix(in srgb,var(--foreground) 75%,transparent)}}');
  assert.match(theme.legacy.toString(),/--foreground-mc-rgb:/);
  assert.match(theme.legacy.toString(),/--muted-mc-rgb:var\(--foreground-mc-rgb\)/);
  assert.match(editor.legacy.toString(),/background:rgba\(var\(--foreground-mc-rgb\), 0.03\)/);
  assert.match(editor.legacy.toString(),/color:rgba\(var\(--foreground-mc-rgb\), 0.75\)/);
  assert(!editor.legacy.toString().includes('@supports (color:color-mix'));
});
