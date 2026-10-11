#!/usr/bin/env node
// Sends a 如流 message as one of this bridge's robots, so an agent can post to a
// group (or a person) on its own instead of only answering the message it got.
//
//   node scripts/infoflow-send.js --group <群ID> [--at u1,u2] --text "内容" [--robot <名称或ID>] [--when <时间>]
//   node scripts/infoflow-send.js --user <userid> --text "内容"
//
// --when schedules the same send as a one-shot systemd timer instead of sending
// now; it survives the agent's run ending (not a reboot). List / cancel with
//   systemctl list-timers 'infoflow-send-*'   /   systemctl stop <unit>.timer
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";
import { Client } from "@baidu/infoflow-sdk-nodejs";
import { loadConfig } from "../src/config.js";

const QUIET = { info() {}, warn() {}, error() {}, debug() {}, trace() {}, log() {} };

function fail(message) {
  console.error("infoflow-send: " + message);
  process.exit(2);
}

const { values } = parseArgs({
  options: {
    group: { type: "string" },
    user: { type: "string" },
    at: { type: "string" },
    text: { type: "string" },
    robot: { type: "string" },
    when: { type: "string" },
  },
  strict: true,
});

const text = String(values.text ?? "").trim();
if (text === "") fail("--text 不能为空");
if (Boolean(values.group) === Boolean(values.user)) fail("--group 和 --user 必须二选一");
if (values.group && !/^\d+$/.test(values.group)) fail("--group 应该是数字群 ID");
const at = String(values.at ?? "").split(/[\s,，、]+/).map((id) => id.replace(/^@/, "").trim()).filter(Boolean);
if (at.length > 50) fail("--at 最多 50 人");

const robots = loadConfig().robots.filter((robot) => robot.purpose !== "verification" && robot.enabled !== false);
const robot = values.robot
  ? robots.find((entry) => entry.id === values.robot || entry.name === values.robot)
  : robots.length === 1 ? robots[0] : null;
if (!robot) fail(values.robot ? `没有名为 ${values.robot} 的机器人` : `请用 --robot 指定机器人：${robots.map((entry) => entry.name).join("、") || "（未配置）"}`);

if (values.when) {
  schedule(parseWhen(values.when));
} else {
  await send().catch((error) => fail("发送失败：" + error.message));
}

// "19:28" means the next 19:28 today (local time); anything else must be a full
// date-time Date can parse, e.g. "2026-10-11 19:28" or an ISO string.
function parseWhen(input) {
  const clock = /^(\d{1,2}):(\d{2})$/.exec(input.trim());
  const date = clock ? new Date() : new Date(input.trim().replace(" ", "T"));
  if (clock) date.setHours(Number(clock[1]), Number(clock[2]), 0, 0);
  if (Number.isNaN(date.getTime())) fail(`无法识别时间：${input}`);
  if (date.getTime() <= Date.now()) fail(`时间已经过去：${date.toLocaleString("zh-CN")}`);
  if (date.getTime() - Date.now() > 30 * 86_400_000) fail("最多只能提前 30 天定时");
  return date;
}

function schedule(date) {
  const unit = "infoflow-send-" + date.getTime().toString(36) + "-" + Math.random().toString(36).slice(2, 6);
  const calendar = date.toISOString().replace("T", " ").replace(/\.\d+Z$/, " UTC");
  const args = [];
  for (const [flag, value] of [["--group", values.group], ["--user", values.user], ["--at", at.join(",")], ["--text", text], ["--robot", robot.id]]) {
    if (value) args.push(flag, value);
  }
  const env = ["INFOFLOW_CONFIG_PATH", "INFOFLOW_BASE_URL"].filter((name) => process.env[name]).map((name) => `--setenv=${name}=${process.env[name]}`);
  const result = spawnSync("systemd-run", [
    "--unit", unit, "--on-calendar", calendar, "--timer-property=AccuracySec=1s", "--collect", ...env,
    process.execPath, fileURLToPath(import.meta.url), ...args,
  ], { encoding: "utf8" });
  if (result.status !== 0) fail("定时失败：" + (result.stderr || result.error?.message || "systemd-run 出错").trim());
  console.log(JSON.stringify({ ok: true, scheduled: date.toLocaleString("zh-CN", { hour12: false }), unit, cancel: `systemctl stop ${unit}.timer` }));
}

async function send() {
  const client = new Client({ appKey: robot.appKey, appSecret: robot.appSecret, agentId: robot.appId, baseUrl: robot.baseUrl, logger: QUIET });
  if (values.user) {
    await client.im.message.sendToUser(values.user, text, "md");
  } else {
    // 如流 shows the mention where @userid appears in MD text; the AT block is what notifies.
    const missing = at.filter((id) => !text.includes("@" + id));
    const content = (missing.length > 0 ? missing.map((id) => "@" + id).join(" ") + " " : "") + text;
    const body = [...(at.length > 0 ? [{ type: "AT", atall: false, atuserids: at }] : []), { type: "MD", content }];
    await client.im.message.sendToGroupWithOptions({ groupId: values.group, msgtype: "MD", body });
  }
  console.log(JSON.stringify({ ok: true, robot: robot.name, to: values.group ? "group " + values.group : "user " + values.user, at }));
}
