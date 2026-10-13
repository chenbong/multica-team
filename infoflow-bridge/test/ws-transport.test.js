import test from "node:test";
import assert from "node:assert/strict";
import http from "node:http";
import https from "node:https";
import { once } from "node:events";
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { createWSClient } from "../src/ws-transport.js";
import { patchSdkTransport } from "../scripts/patch-sdk-transport.js";

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
