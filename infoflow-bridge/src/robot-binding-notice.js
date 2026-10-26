import { loadConfig, saveRobots } from "./config.js";
import { inlineMarkdown } from "./inline-markdown.js";
import { withReplyMode } from "./reply-format.js";
import {verificationRecipient} from "./company-identity.js";
import { agentReference } from "./agent-binding.js";

export async function deleteRobotBinding({ runtime, robot, email, logger = console,
  persist = saveRobots, readConfig = loadConfig,
  sendDirect = async (...args) => (await import("./infoflow.js")).sendRobotDirect(...args) }) {
  if (!robot) return { status: "skipped" };
  // The ownership check may await a remote API. Do not remove a replacement
  // binding or repeat a deletion that completed while that check was pending.
  const current = runtime.config.robots.find(entry => entry.id === robot.id);
  if (!current) return { status: "skipped" };
  if (current !== robot) throw new Error("机器人绑定已变化，请刷新页面后重试");
  const removed = { ...robot };
  persist(runtime.config.robots.filter(entry => entry.id !== removed.id));
  await runtime.sync(readConfig());
  try {
    if (runtime.config.robots.some(entry => entry.id === removed.id)) throw new Error("binding still present");
    const recipient = verificationRecipient(email,runtime.config.admin?.allowedDomains??[String(email).split("@")[1]]);
    if (typeof recipient!=="string" || !/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(recipient)) throw new Error("invalid notice recipient");
    const text = `智能体 **${inlineMarkdown(removed.agentName || removed.agent)}** 解绑如流机器人 **${inlineMarkdown(removed.name)}** 成功`;
    await sendDirect(removed, recipient, withReplyMode(text, "chat"));
    return { status: "sent" };
  } catch {
    logger.warn("robot binding deleted; confirmation message delivery failed");
    return { status: "failed" };
  }
}

function sameBinding(a, b) {
  return agentReference(a || {}) === agentReference(b || {}) &&
    ["appId", "profile", "workspaceId"].every(key => String(a?.[key] ?? "").trim() === String(b?.[key] ?? "").trim());
}

// Called only after the admin route verifies the signed-in owner and credentials.
// A notification failure must never turn an already saved binding into a failed
// save, leak SDK errors, or cause an automatic repeat/rebind from the browser.
export async function saveRobotBinding({ runtime, robots, previous, robot, email, logger = console,
  persist = saveRobots, readConfig = loadConfig }) {
  persist(robots);
  await runtime.sync(readConfig());
  if (robot.enabled === false || (sameBinding(previous, robot) && previous?.enabled !== false)) return { status: "skipped" };
  try {
    const current = runtime.config.robots.find(entry => entry.id === robot.id);
    if (!current || !sameBinding(current, robot) || !current.enabled) throw new Error("binding changed before notification");
    const recipient = verificationRecipient(email,runtime.config.admin?.allowedDomains??[String(email).split("@")[1]]);
    if (typeof recipient!=="string" || !/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(recipient)) throw new Error("invalid notice recipient");
    const text = `智能体 **${inlineMarkdown(robot.agentName || robot.agent)}** 绑定如流机器人 **${inlineMarkdown(robot.name)}** 成功`;
    await runtime.sendDirect(current.id, recipient, withReplyMode(text, "chat"));
    return { status: "sent" };
  } catch {
    logger.warn("robot binding saved; confirmation message delivery failed");
    return { status: "failed" };
  }
}
