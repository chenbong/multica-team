import { loadConfig, saveRobots } from "./config.js";
import { inlineMarkdown } from "./inline-markdown.js";
import { withReplyMode } from "./reply-format.js";

function sameBinding(a, b) {
  return ["appId", "profile", "workspaceId", "agent"].every(key => String(a?.[key] ?? "").trim() === String(b?.[key] ?? "").trim());
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
    const recipient = String(email).split("@")[0];
    if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(recipient)) throw new Error("invalid notice recipient");
    const text = `智能体 **${inlineMarkdown(robot.agent)}** 绑定如流机器人 **${inlineMarkdown(robot.name)}** 成功`;
    await runtime.sendDirect(current.id, recipient, withReplyMode(text, "chat"));
    return { status: "sent" };
  } catch {
    logger.warn("robot binding saved; confirmation message delivery failed");
    return { status: "failed" };
  }
}
