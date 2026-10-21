import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { privateContent, createInboundMessageReader } from "./src/inbound-content.js";
import { stageInboundImages } from "./src/bridge.js";
import { registerHandlers } from "./src/robots.js";

const png = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aNgAAAABJRU5ErkJggg==", "base64");
const quiet = { warn() {}, log() {} };

test("private text/image callback fragments resolve one complete original message without waiting", async () => {
  let calls=0;
  const read=createInboundMessageReader({httpClient:{get:async(url,options)=>{
    calls++;assert.match(url,/receiverType=7/);assert.equal(options.disableRetry,true);
    return {code:'ok',data:{code:200,data:{items:[{msgId:'123',sender:{uuapId:'alice'},content:{blocks:[
      {type:'reply',msgId:'122',text:'/bind WRONG-999'}, {type:'text',text:'Analyze both'},
      {type:'image',downloadUrl:'https://example.test/a'}, {type:'image',downloadUrl:'https://example.test/b'},
    ]}}]}}};
  }}},'12');
  const a=await read({FromUserId:'alice',MsgId:'123',Content:'Analyze both'},'private.text');
  const b=await read({FromUserId:'alice',MsgId:'123',ImgDownloadurl:'https://example.test/a'},'private.image');
  assert.equal(calls,1);assert.deepEqual(a,b);assert.equal(a.images.length,2);
  assert.equal(a.replyToMessageId,'122');assert.equal(a.text,'Analyze both');
});

test("private original-message failure never falls back to a potentially incomplete text input", async () => {
  let calls=0;
  const read=createInboundMessageReader({httpClient:{get:async()=>{calls++;throw Error('network');}}},'12');
  const parsed=await read({FromUserId:'alice',MsgId:'123',Content:'Only text fragment'},'private.text');
  assert.equal(calls,3);assert.match(parsed.mediaRejection,/未提交到任务/);assert.deepEqual(parsed.images,[]);
});

test("directed group images resolve the complete scoped original instead of legacy proxy URLs", async () => {
  const handlers=new Map(),received=[];
  let queries=0;
  const client={httpClient:{get:async(url)=>{
    queries++;
    const params=new URL(url,'https://example.test').searchParams;
    assert.equal(params.get('receiverType'),'2');
    assert.equal(params.get('receiverId'),'42');
    assert.equal(params.get('fromId'),'alice');
    return {code:'ok',data:{code:200,data:{items:[{msgId:'123',sender:{uuapId:'alice'},content:{blocks:[
      {type:'at',agentId:12,robotName:'test-bot'},
      {type:'text',text:'TEST-1 Analyze both'},
      {type:'at',userId:'bob',userName:'Bob'},
      {type:'image',downloadUrl:'https://apiin.im.baidu.com/image-a'},
      {type:'image',downloadUrl:'https://apiin.im.baidu.com/image-b'},
    ]}}]}}};
  }}};
  registerHandlers({wsClient:{on:(event,handler)=>handlers.set(event,handler)},client,
    robot:{name:'test-bot',appId:'12'},bridge:{handleMessage:async message=>received.push(message)},logger:quiet});
  const raw={eventtype:'MESSAGE_RECEIVE',groupid:'42',message:{header:{fromuserid:'alice',messageid:'123'},body:[
    {type:'AT',robotid:'12'}, {type:'TEXT',content:'TEST-1 Analyze both'},
    {type:'IMAGE',downloadurl:'https://legacy.example.test/expired-a'},
    {type:'IMAGE',downloadurl:'https://legacy.example.test/expired-b'},
  ]}};
  await handlers.get('group.mixed')({data:{raw}});
  assert.equal(received.length,1);
  assert.equal(received[0].images.length,2);
  assert.equal(received[0].images[0].downloadUrl,'https://apiin.im.baidu.com/image-a');
  assert.match(received[0].displayText,/@Bob/);
  assert.deepEqual(received[0].notifyUsers,['alice','bob']);
  raw.eventtype='ALL_MESSAGE_FORWARD';
  await handlers.get('group.mixed')({data:{raw}});
  raw.eventtype='MESSAGE_RECEIVE';raw.message.body.shift();
  await handlers.get('group.mixed')({data:{raw}});
  assert.equal(queries,1);
  assert.equal(received.length,1);
});

test("official authenticated image URLs carry the robot token, not an anonymous request", async t => {
  const previous=globalThis.fetch;t.after(()=>{globalThis.fetch=previous;});
  globalThis.fetch=async(url,options)=>{assert.equal(options.headers.Authorization,'Bearer-test-robot-access');return new Response(png);};
  const staged=await stageInboundImages([{downloadUrl:'https://apiin.im.baidu.com/image'}],quiet,async()=>'test-robot-access');
  t.after(staged.cleanup);assert.equal(staged.paths.length,1);assert.deepEqual(staged.failed,[]);
});

test("private image URLs, descriptors, base64 and richtext preserve every image", () => {
  for (const value of ["https://example.test/image", { downloadurl: "https://example.test/image" }, png.toString("base64")]) {
    assert.equal(privateContent({ Content: value }, "private.image").images.length, 1);
  }
  const content = JSON.stringify({ body: [{ type: "TEXT", content: "查看两张图" },
    { type: "IMAGE", downloadurl: "https://example.test/a" }, { type: "IMAGE", downloadurl: "https://example.test/b" }] });
  const parsed = privateContent({ Content: content }, "private.richtext");
  assert.equal(parsed.text, "查看两张图");
  assert.equal(parsed.images.length, 2);
  assert.equal(privateContent({ Content: "report.pdf" }, "private.file").unsupportedFile, true);
});

test("group quote without AT must be verified by the server; plain pictures stay ignored", async () => {
  const handlers = new Map(), inputs = [];
  registerHandlers({ wsClient: { on: (e, f) => handlers.set(e, f) }, client: {}, robot: { name: "test-bot", appId: "12" },
    getGroupMessage: async(raw,event)=>privateContent(raw,event),
    bridge: { issueRelay: {enabled:true}, handleMessage: async v => inputs.push(v) }, logger: quiet });
  const raw = { eventtype: "MESSAGE_RECEIVE", groupid: "42", message: {
    header: { fromuserid: "alice", messageid: "123" }, body: [{ type: "IMAGE", downloadurl: "https://example.test/a" }] } };
  await handlers.get("group.image")({ data: { raw } });
  assert.equal(inputs.length, 0);
  raw.message.body.unshift({ type: "replyData", sBasemsgId: "122", content: "/bind OTHER-999" });
  await handlers.get("group.mixed")({ data: { raw } });
  assert.equal(inputs.length, 1);
  assert.equal(inputs[0].requireRobotQuote, true);
  assert.equal(inputs[0].replyToMessageId, "122");
  assert.doesNotMatch(inputs[0].text, /OTHER-999/);
  raw.eventtype = "ALL_MESSAGE_FORWARD";
  await handlers.get("group.mixed")({ data: { raw } });
  assert.equal(inputs.length, 2);
  assert.equal(inputs[1].requireRobotQuote,true);
  assert.equal(inputs[1].replyToMessageId,'122');
  raw.message.body.shift();
  await handlers.get("group.image")({data:{raw}});
  assert.equal(inputs.length,2);
});

test("forwarded group quotes cannot fall through to unbound Chat", async () => {
  const handlers=new Map(),inputs=[];
  registerHandlers({wsClient:{on:(e,f)=>handlers.set(e,f)},client:{},robot:{name:'test-bot',appId:'12'},
    bridge:{handleMessage:async v=>inputs.push(v)},logger:quiet});
  const raw={eventtype:'ALL_MESSAGE_FORWARD',groupid:'42',message:{header:{fromuserid:'alice',messageid:'123'},
    body:[{type:'replyData',sBasemsgId:'122'},{type:'TEXT',content:'ordinary quoted conversation'}]}};
  await handlers.get('group.mixed')({data:{raw}});assert.equal(inputs.length,0);
});

test("temporary image downloads retry twice and stage the original bytes", async t => {
  const previous = globalThis.fetch;
  t.after(() => { globalThis.fetch = previous; });
  let calls = 0;
  globalThis.fetch = async () => ++calls < 3 ? new Response("busy", { status: 503 }) : new Response(png, { headers: { "content-type": "image/png" } });
  const staged = await stageInboundImages([{ downloadurl: "https://example.test/a" }], quiet);
  t.after(staged.cleanup);
  assert.equal(calls, 3);
  assert.deepEqual(staged.failed, []);
  assert.deepEqual(await readFile(staged.paths[0]), png);
});

test("HTTP-200 image rate limits retry with backoff without losing a multi-image batch", async t => {
  const previous=globalThis.fetch;t.after(()=>{globalThis.fetch=previous;});
  let calls=0,limitedAt=0;
  globalThis.fetch=async()=>{
    calls++;
    if(calls===2){limitedAt=Date.now();return Response.json({code:1300019,msg:'rate limit'});}
    if(calls===3)assert(Date.now()-limitedAt>=900,'rate-limited downloads must back off');
    return new Response(png,{headers:{'content-type':'image/png'}});
  };
  const staged=await stageInboundImages([{downloadUrl:'https://apiin.im.baidu.com/a'},
    {downloadUrl:'https://apiin.im.baidu.com/b'}],quiet,async()=>'test-token');
  t.after(staged.cleanup);assert.equal(calls,3);assert.equal(staged.paths.length,2);assert.deepEqual(staged.failed,[]);
});

test("persistent image rate limits stop after three attempts", async t => {
  const previous=globalThis.fetch;t.after(()=>{globalThis.fetch=previous;});
  let calls=0;globalThis.fetch=async()=>{calls++;return Response.json({code:1300019});};
  const staged=await stageInboundImages([{downloadUrl:'https://apiin.im.baidu.com/a'}],quiet,async()=>'test-token');
  t.after(staged.cleanup);assert.equal(calls,3);assert.equal(staged.paths.length,0);assert.match(staged.failed[0],/限流/);
});

test("invalid image format and permanent HTTP failures are not retried", async t => {
  const previous = globalThis.fetch;
  t.after(() => { globalThis.fetch = previous; });
  let calls = 0;
  globalThis.fetch = async () => { calls++; return new Response("denied", { status: 403 }); };
  const denied = await stageInboundImages([{ downloadurl: "https://example.test/a" }], quiet);
  t.after(denied.cleanup);
  assert.equal(calls, 1);
  assert.equal(denied.failed.length, 1);
  const invalid = await stageInboundImages([{ inline: true, content: Buffer.from("not an image").toString("base64") }], quiet);
  t.after(invalid.cleanup);
  assert.equal(invalid.paths.length, 0);
  assert.equal(invalid.failed.length, 1);
  const tooMany = await stageInboundImages(Array.from({length:6}, () => ({ inline:true, content:png.toString("base64") })), quiet);
  assert.match(tooMany.failed[0], /5/);
});

test("streamed downloads stop at the size limit even without content length", async t => {
  const previous = globalThis.fetch;
  t.after(() => { globalThis.fetch = previous; });
  let calls = 0;
  globalThis.fetch = async () => { calls++; return new Response(Buffer.alloc(20*1024*1024+1), { headers: { "content-type": "image/png" } }); };
  const staged = await stageInboundImages([{ downloadurl: "https://example.test/a" }], quiet);
  t.after(staged.cleanup);
  assert.equal(calls, 1);
  assert.equal(staged.paths.length, 0);
  assert.match(staged.failed[0], /20MB/);
});
