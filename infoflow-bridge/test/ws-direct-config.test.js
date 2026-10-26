import test from 'node:test';
import assert from 'node:assert/strict';
import {createWSClient} from '../src/ws-transport.js';

test('explicit direct connection does not inherit production proxy and preserves server timing',()=>{
 const previous=process.env.INFOFLOW_WS_PROXY;
 process.env.INFOFLOW_WS_PROXY='http://proxy.invalid:8188';
 const logger=Object.fromEntries(['info','debug','trace','warn','error'].map(k=>[k,()=>{}]));
 const c=createWSClient({appKey:'test',appSecret:'test',logger,autoRegister:false,
  wsGateway:'internal.example.test',wsConnectDomain:'internal.example.test:8869'},'');
 try{
  assert.equal(c.wsProxyAgent,undefined);
  assert.equal(c.wsEndpointDomain,'internal.example.test');
  assert.equal(c.wsConnectDomain,'internal.example.test:8869');
  c.serverConfig={ping_interval:120,reconnect_interval:120,reconnect_nonce:30,reconnect_count:-1};
  assert.equal(c.getHeartbeatInterval(),120000);
  const ms=c.getReconnectInterval();assert(ms>=120000&&ms<=150000);
  assert.equal(c.getMaxReconnectAttempts(),-1);
 }finally{c.disconnect();if(previous===undefined)delete process.env.INFOFLOW_WS_PROXY;else process.env.INFOFLOW_WS_PROXY=previous;}
});
