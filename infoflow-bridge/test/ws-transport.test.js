import test from "node:test";
import assert from "node:assert/strict";
import http from "node:http";
import https from "node:https";
import { once } from "node:events";
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { createWSClient } from "../src/ws-transport.js";
import { patchSdkTransport, patchSdkMessageIds } from "../scripts/patch-sdk-transport.js";

const logger = Object.fromEntries(["info", "debug", "trace", "warn", "error"].map(k => [k, () => {}]));
const options = { appKey: "test-key", appSecret: "test-secret", logger, autoRegister: false, endpointTimeout: 1000 };

test("SDK patch is idempotent and rejects unreviewed SDKs/anchors", () => {
  const source = readFileSync(createRequire(import.meta.url).resolve("@baidu/infoflow-sdk-nodejs"), "utf8");
  assert.equal(patchSdkTransport(source, "2026.8.28"), source);
  assert.throws(() => patchSdkTransport(source, "next"), /Unsupported/);
  assert.throws(() => patchSdkTransport("changed source", "2026.8.28"), /anchor/);
});

test("no proxy preserves SDK defaults and rejects unsafe proxy schemes", () => {
  const client = createWSClient(options, "");
  assert.equal(client.wsProxyAgent, undefined);
  assert.throws(() => createWSClient(options, "socks5://localhost:1000"), /HTTP/);
  client.disconnect();
});

test("SDK preserves private and group IDs through nested Encrypt without network", async () => {
  const source = readFileSync(createRequire(import.meta.url).resolve("@baidu/infoflow-sdk-nodejs"), "utf8");
  assert.equal(patchSdkMessageIds(source, "2026.8.28"), source);
  assert.throws(() => patchSdkMessageIds("changed", "2026.8.28"), /anchor/);
  assert.throws(() => patchSdkMessageIds(source, "next"), /Unsupported/);
  const client = createWSClient({ ...options, autoAck: false }, "");
  let received;
  client.frameCodec.decode = () => ({ method: 1, headers: [], seqId: 1 });
  client.frameCodec.parsePayload = () => ({ Encrypt: '{"MsgId":1836339350307196087,"Reply":[{"ReplyMsgId":1836339350307196090}],"message":{"header":{"messageid":1836339350307196088},"body":[{"type":"replyData","sBasemsgId":1836339350307196089}]}}' });
  client.eventDispatcher.dispatchFrame = async payload => { received = payload; };
  await client.handleMessage(Buffer.from("fixture"));
  client.disconnect();
  assert.equal(received.MsgId, "1836339350307196087");
  assert.equal(received.message.header.messageid, "1836339350307196088");
  assert.equal(received.message.body[0].sBasemsgId, "1836339350307196089");
  assert.equal(received.Reply[0].ReplyMsgId, "1836339350307196090");
});

test("message-ID patch upgrades an existing v1 installation and stays idempotent", () => {
  const source = readFileSync(createRequire(import.meta.url).resolve("@baidu/infoflow-sdk-nodejs"), "utf8");
  const v1 = source.replace("    'ReplyMsgId',\n", "").replace("// bridge-exact-message-ids-v2", "// bridge-exact-message-ids-v1");
  const upgraded = patchSdkMessageIds(v1, "2026.8.28");
  assert.match(upgraded, /'ReplyMsgId'/);
  assert.equal(patchSdkMessageIds(upgraded, "2026.8.28"), upgraded);
});

test("both endpoint and WebSocket use only the client proxy; internal HTTP stays direct", async () => {
  const originalRequest = https.request;
  const originalAgent = https.globalAgent;
  const targets = [];
  const proxy = http.createServer((_req, res) => res.end("internal-direct"));
  proxy.on("connect", (req, socket) => {
    targets.push(req.url);
    socket.end("HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n");
  });
  proxy.listen(0, "127.0.0.1");
  await once(proxy, "listening");
  const port = proxy.address().port;
  const client = createWSClient({ ...options, wsGateway: "gateway.invalid" }, `http://127.0.0.1:${port}`);
  client.on("error", () => {});
  try {
    await assert.rejects(client.fetchWsEndpoint(1000), /HTTP 502/);
    client.state = "connecting";
    client.manualDisconnect = true;
    await assert.rejects(client.connectWebSocket("wss://socket.invalid/ws"), /502/);
    assert.deepEqual(targets, ["gateway.invalid:443", "socket.invalid:443"]);
    // A second attempt must retain the scoped agent, as SDK reconnect does.
    await assert.rejects(client.fetchWsEndpoint(1000), /HTTP 502/);
    assert.equal(targets.length, 3);
    const body = await new Promise((resolve, reject) => {
      http.get(`http://127.0.0.1:${port}/api/health`, res => {
        let body = "";
        res.on("data", chunk => { body += chunk; });
        res.on("end", () => resolve(body));
      }).on("error", reject);
    });
    assert.equal(body, "internal-direct");
    assert.equal(targets.length, 3);
    assert.equal(https.request, originalRequest);
    assert.equal(https.globalAgent, originalAgent);
  } finally {
    client.disconnect();
    client.wsProxyAgent.destroy();
    proxy.closeAllConnections();
    await new Promise(resolve => proxy.close(resolve));
  }
});
