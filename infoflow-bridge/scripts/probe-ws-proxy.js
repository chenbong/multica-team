// Explicit operational diagnostic. Does not register callbacks, acknowledge
// messages, send chat messages, or print credentials/signed WebSocket URLs.
import { readFileSync } from "node:fs";
import { setTimeout as delay } from "node:timers/promises";
import { createWSClient } from "../src/ws-transport.js";

const config = JSON.parse(readFileSync(process.env.INFOFLOW_CONFIG_PATH, "utf8"));
const robot = config.robots.find(r => r.name === process.env.PROBE_ROBOT && r.enabled && r.purpose !== "verification");
if (!robot) throw new Error("Select an enabled task robot with PROBE_ROBOT");
const proxyUrl = process.env.PROBE_PROXY;
if (!proxyUrl) throw new Error("PROBE_PROXY is required");
const proxyPort = new URL(proxyUrl).port;
const logger = Object.fromEntries(["info", "debug", "trace", "warn", "error"].map(k => [k, () => {}]));
for (let attempt = 1; attempt <= 3; attempt++) {
  const result = { proxyPort, attempt, phase1: false, connected: false, reconnect: false, ping: 0, pong: 0 };
  const client = createWSClient({
    appKey: robot.appKey, appSecret: robot.appSecret, baseUrl: robot.baseUrl,
    ...(process.env.INFOFLOW_WS_GATEWAY ? { wsGateway: process.env.INFOFLOW_WS_GATEWAY } : {}),
    ...(process.env.INFOFLOW_WS_CONNECT_DOMAIN ? { wsConnectDomain: process.env.INFOFLOW_WS_CONNECT_DOMAIN } : {}),
    logger, autoRegister: false, autoAck: false, endpointTimeout: 10000,
  }, proxyUrl);
  client.on("error", () => {});
  client.on("heartbeat", event => { if (event.data?.type in result) result[event.data.type]++; });
  const fetch = client.fetchWsEndpoint.bind(client);
  client.fetchWsEndpoint = async (...args) => {
    const endpoint = await fetch(...args);
    result.phase1 = true;
    result.wsHost = new URL(endpoint.url).hostname;
    return endpoint;
  };
  const started = Date.now();
  try {
    await client.connect();
    result.connected = client.getState() === "connected";
    result.connectMs = Date.now() - started;
    if (attempt === 3 && process.env.PROBE_RECONNECT === "true") {
      let connectedCount = 0;
      client.on("connected", () => { connectedCount++; });
      client.serverConfig = { ...client.serverConfig, reconnect_interval: 1, reconnect_nonce: 0 };
      client.ws.terminate();
      const end = Date.now() + 15000;
      while (!connectedCount && Date.now() < end) await delay(100);
      result.reconnect = connectedCount > 0 && client.getState() === "connected";
    }
    const holdMs = attempt === 3 ? Number(process.env.PROBE_HOLD_MS ?? 2000) : 1000;
    await delay(holdMs);
    result.holdMs = holdMs;
    result.finalState = client.getState();
  } catch (error) {
    result.errorCode = error.code ?? "CONNECTION_FAILED";
    result.elapsedMs = Date.now() - started;
  } finally {
    client.disconnect();
    client.wsProxyAgent?.destroy();
    console.log(JSON.stringify(result));
    await delay(300);
  }
}
