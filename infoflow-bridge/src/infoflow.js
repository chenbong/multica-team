import { Client } from "@baidu/infoflow-sdk-nodejs";

// The SDK calls logger.trace, which console does not have.
const QUIET = { info() {}, warn() {}, error() {}, debug() {}, trace() {}, log() {} };

// Asks InfoFlow what an application is called. The admin page names a robot from
// its credentials instead of asking someone to type a label, so the name always
// matches the application in 如流 — and a wrong App Key or Secret fails while
// saving instead of turning up later as a dead connection.
export async function fetchRobotName({ appId, appKey, appSecret, baseUrl }) {
  const client = new Client({ appKey, appSecret, agentId: appId, baseUrl, logger: QUIET });
  const info = await client.robot.getInfo();
  const name = String(info?.name ?? "").trim();
  if (name === "") throw new Error("如流没有返回这个应用的名称");
  return name;
}

export function generateInfoflowRobotLink(accountId) {
  const id = String(accountId ?? "").trim();
  if (!/^\d+$/.test(id)) throw new Error("如流没有返回有效的机器人 accountId");
  const payload = { APIName: "BdHiJs.service.hi.open", version: 65, data: { type: "4", id } };
  return `infoflow://APICenter?data=${Buffer.from(JSON.stringify(payload), "utf8").toString("base64")}`;
}

export async function fetchRobotLink({ appId, appKey, appSecret, baseUrl }) {
  const client = new Client({ appKey, appSecret, agentId: appId, baseUrl, logger: QUIET });
  const info = await client.robot.getInfo();
  return generateInfoflowRobotLink(info?.puid);
}
