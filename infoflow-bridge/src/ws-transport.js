import { WSClient } from "@baidu/infoflow-sdk-nodejs";
import { HttpsProxyAgent } from "https-proxy-agent";
import { EventEmitter } from "node:events";

function guardSdkErrorEmitter(client) {
  // SDK on() registers async handlers separately, then also emits 'error' on
  // EventEmitter. Consume that second emission to avoid an unhandled rejection
  // on transport failures; the regular SDK error handlers still report it.
  EventEmitter.prototype.on.call(client.eventDispatcher, "error", () => {});
  return client;
}

// Deliberately do not inherit HTTP(S)_PROXY: this setting applies only to the
// authenticated endpoint request and its WebSocket, including reconnects.
export function createWSClient(options, proxyUrl = process.env.INFOFLOW_WS_PROXY ?? "") {
  if (!proxyUrl.trim()) return guardSdkErrorEmitter(new WSClient(options));
  let proxy;
  try {
    proxy = new URL(proxyUrl.trim());
    if (!["http:", "https:"].includes(proxy.protocol)) throw new Error();
  } catch {
    throw new Error("INFOFLOW_WS_PROXY must be an HTTP(S) proxy URL");
  }
  if (WSClient.prototype.bridgeProxyTransportVersion !== 1) {
    throw new Error("InfoFlow proxy transport is not installed; run npm run postinstall");
  }
  const agent = new HttpsProxyAgent(proxy, { timeout: options.endpointTimeout ?? 30000 });
  return guardSdkErrorEmitter(new WSClient({ ...options, wsProxyAgent: agent }));
}
