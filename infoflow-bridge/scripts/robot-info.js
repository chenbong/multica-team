// Probe: prints what InfoFlow reports about each configured robot, which is where
// the admin page gets a robot's display name from.
import { readFileSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { Client } from "@baidu/infoflow-sdk-nodejs";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const config = JSON.parse(readFileSync(resolve(root, "config.local.json"), "utf8"));
const quiet = { info() {}, warn() {}, error() {}, debug() {}, trace() {}, log() {} };

for (const robot of config.robots) {
  const client = new Client({
    appKey: robot.appKey,
    appSecret: robot.appSecret,
    agentId: robot.appId,
    baseUrl: robot.baseUrl,
    logger: quiet,
  });
  try {
    const info = await client.robot.getInfo();
    console.log(robot.appId, JSON.stringify({ name: info.name, agentId: info.agentId, fields: Object.keys(info) }));
  } catch (error) {
    console.log(robot.appId, "failed:", error.message);
  }
}
