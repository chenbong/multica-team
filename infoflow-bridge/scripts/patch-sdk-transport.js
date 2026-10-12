// The pinned SDK exposes no agent option. Add a per-client transport hook;
// never replace global HTTP agents (platform/internal requests stay direct).
import { readFileSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";
import { resolve } from "node:path";

const MARKER = "WSClient.prototype.bridgeProxyTransportVersion = 1;";
export function patchSdkTransport(source, version) {
  if (version !== "2026.8.28") throw new Error("Unsupported InfoFlow SDK version; review transport patch before upgrading");
  if (source.includes(MARKER)) return source;
  const edits = [
    ["        this.wsGateway =\n", "        this.wsProxyAgent = options.wsProxyAgent;\n        this.wsGateway =\n"],
    ["                path: '/open/ws/endpoint',", "                agent: this.wsProxyAgent,\n                path: '/open/ws/endpoint',"],
    ["            const agent = isSecure\n                ? new https__namespace.Agent({\n                    secureOptions: crypto__namespace.constants.SSL_OP_LEGACY_SERVER_CONNECT,\n                })\n                : new http__namespace.Agent();",
     "            const agent = this.wsProxyAgent ?? (isSecure\n                ? new https__namespace.Agent({\n                    secureOptions: crypto__namespace.constants.SSL_OP_LEGACY_SERVER_CONNECT,\n                })\n                : new http__namespace.Agent());"],
    ["                agent: agent,\n                headers: wsHeaders,", "                agent: agent,\n                handshakeTimeout: this.endpointTimeout,\n                secureOptions: crypto__namespace.constants.SSL_OP_LEGACY_SERVER_CONNECT,\n                headers: wsHeaders,"],
  ];
  for (const [before, after] of edits) {
    if (source.split(before).length !== 2) throw new Error("InfoFlow SDK transport patch anchor changed; refusing partial patch");
    source = source.replace(before, after);
  }
  return `${source}\n${MARKER}\n`;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const require = createRequire(import.meta.url);
  const entry = require.resolve("@baidu/infoflow-sdk-nodejs");
  const { version } = require("@baidu/infoflow-sdk-nodejs/package.json");
  const before = readFileSync(entry, "utf8");
  const after = patchSdkTransport(before, version);
  if (after !== before) writeFileSync(entry, after);
  console.log("InfoFlow SDK per-client proxy transport ready");
}
