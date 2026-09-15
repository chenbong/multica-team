// Exercises the Multica half without touching InfoFlow: feeds one message through
// the same Bridge code path and prints what the robot would have sent.
import { Bridge } from "../src/bridge.js";
import { loadConfig } from "../src/config.js";

const args = process.argv.slice(2);
const idFlag = args.indexOf("--msg-id");
const messageId = idFlag === -1 ? undefined : args.splice(idFlag, 2)[1];
const text = args.join(" ").trim();
if (text === "") {
  console.error("usage: node scripts/dry-run.js [--msg-id <id>] <message>");
  process.exit(2);
}

const config = loadConfig();
const robot = config.robots[0];
if (!robot) {
  console.error("no robot configured yet; open the admin page and bind one first");
  process.exit(2);
}

const user = process.env.DRY_RUN_USER ?? robot.allowUsers[0] ?? "unknown";
const bridge = new Bridge(
  {
    robotId: robot.id,
    multica: { ...config.multica, defaultAgent: robot.agent },
    allowUsers: robot.allowUsers,
    allowAllInGroups: robot.allowAllInGroups,
    userMap: {},
    pollIntervalMs: config.pollIntervalMs,
    pollTimeoutMs: config.pollTimeoutMs,
    statePath: config.statePath,
  },
  async (target, content, options = {}) => {
    const at = options.atUser ? ` @${options.atUser}` : "";
    console.log(`-> ${target.kind}:${target.id}${at}:\n${content}\n`);
  },
);

const conversation = process.env.DRY_RUN_GROUP_ID
  ? { kind: "group", id: Number(process.env.DRY_RUN_GROUP_ID) }
  : { kind: "private", id: user };
await bridge.handleMessage({ conversation, user, text, messageId });
