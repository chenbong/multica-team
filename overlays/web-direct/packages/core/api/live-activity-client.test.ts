// @vitest-environment node
import {afterEach,expect,it,vi} from "vitest";
import {createServer} from "node:http";
import {ApiClient,ApiError} from "./client";
afterEach(()=>vi.unstubAllGlobals());
it("sends authenticated activity as JSON over real HTTP and accepts an empty 204",async()=>{
 const received:{headers:Record<string,unknown>;body:unknown}[]=[];
 const server=createServer((req,res)=>{
  const chunks:Buffer[]=[];req.on("data",chunk=>chunks.push(chunk));req.on("end",()=>{
   if(req.headers["content-type"]!=="application/json"){res.writeHead(415,{"Content-Type":"application/json"});res.end(JSON.stringify({error:"Expected application/json"}));return;}
   received.push({headers:req.headers,body:JSON.parse(Buffer.concat(chunks).toString())});res.writeHead(204);res.end();
  });
 });
 await new Promise<void>(resolve=>server.listen(0,"127.0.0.1",resolve));
 try{
  const address=server.address();if(!address||typeof address==="string")throw Error("Missing local port");
  const client=new ApiClient(`http://127.0.0.1:${address.port}`);client.setToken("local-fixture-only");
  const event={event_id:"00000000-0000-4000-8000-000000000001",page_view:true};
  await client.recordLiveActivity(event);
  expect(received).toHaveLength(1);expect(received[0]!.headers.authorization).toBe("Bearer local-fixture-only");expect(received[0]!.body).toEqual(event);
 }finally{await new Promise<void>((resolve,reject)=>server.close(error=>error?reject(error):resolve()));}
});
it("preserves permanent HTTP errors for the bounded reporting queue",async()=>{
 vi.stubGlobal("fetch",vi.fn().mockResolvedValue(new Response(JSON.stringify({error:"Forbidden"}),{status:403,headers:{"Content-Type":"application/json"}})));
 try{await new ApiClient("http://127.0.0.1").recordLiveActivity({event_id:"00000000-0000-4000-8000-000000000001",page_view:false});throw Error("Expected error");}catch(error){expect(error).toBeInstanceOf(ApiError);expect((error as ApiError).status).toBe(403);}
});
