import { Multica } from "./multica.js";

const UUID = /^[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}$/i;

// Resolve each configured robot under its own account. A filtered catalog from
// another account is never evidence that a private participant has no robot.
export async function groupRobotEntries(config, instances, workspaceId, agentIds = null, logger = console, { agentNames = null, includeRobotId = null } = {}) {
  const entries = [];
  // Only a complete, server-provided task roster can prove that a legacy name
  // is unrelated. Account-filtered catalogs are not used for this decision.
  const completeRoster = agentIds && agentNames && agentIds.every(id =>
    Object.hasOwn(agentNames, id) && typeof agentNames[id] === "string" && agentNames[id]);
  const rosterNames = new Set(completeRoster ? agentIds.map(id => agentNames[id]) : []);
  for (const robot of config.robots) {
    if (robot.purpose === "verification" || (robot.workspaceId || config.multica.workspaceId) !== workspaceId) continue;
    const instance = instances.get(robot.id);
    let agentId = String(robot.agent || "");
    if (!UUID.test(agentId)) {
      if (completeRoster && robot.id !== includeRobotId && !rosterNames.has(agentId)) continue;
      const resolver = instance?.bridge?.multica || new Multica({
        ...config.multica, profile: robot.profile ?? "", workspaceId,
      }, logger);
      const catalog = await resolver.listAgents();
      if (!Array.isArray(catalog)) throw new Error("robot agent catalog is unavailable");
      const matches = catalog.filter(agent => agent.id === agentId || agent.name === agentId);
      // Missing/hidden/deleted and ambiguous names cannot safely be classified as
      // absent. Require a repaired name or an explicit UUID before any routing.
      if (matches.length !== 1) throw new Error("robot agent mapping is missing or ambiguous");
      agentId = matches[0].id;
      if (typeof agentId !== "string" || !UUID.test(agentId)) throw new Error("robot agent catalog returned an invalid mapping");
    }
    if (!agentId || (agentIds && !agentIds.includes(agentId) && robot.id !== includeRobotId)) continue;
    const available = robot.enabled && instance && !instance.stopping;
    entries.push({ robotId: robot.id, agentId, appId: robot.appId,
      client: available ? instance.client : null, send: available ? instance.send : null });
  }
  return entries;
}
