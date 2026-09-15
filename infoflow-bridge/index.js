import { loadConfig, saveRobots } from "./src/config.js";
import { RobotRuntime } from "./src/robots.js";
import { startAdminServer } from "./src/admin.js";

const config = loadConfig();
if (config.migratedFromLegacy) {
  saveRobots(config.robots);
  console.log("migrated the single-robot config into the robots list");
}
const runtime = new RobotRuntime(config);
await runtime.sync();
startAdminServer({ runtime });

const configured = runtime.config.robots.length;
console.log(
  configured === 0
    ? "no robots configured yet — open the admin page to bind one"
    : `${configured} robot(s) configured: ${runtime.config.robots.map((robot) => `${robot.name} -> ${robot.agent}`).join(", ")}`,
);

for (const signal of ["SIGINT", "SIGTERM"]) {
  process.on(signal, async () => {
    console.log(`${signal} received, shutting down`);
    await runtime.stopAll();
    process.exit(0);
  });
}
