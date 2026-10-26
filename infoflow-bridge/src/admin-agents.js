// The agent API records the creator as owner_id. Shared access, including
// workspace administration, does not make an agent a binding candidate here.
export function ownedAgents(agents, userId) {
  if (!userId || !Array.isArray(agents)) return [];
  return agents.filter((agent) => agent?.owner_id === userId && !agent.archived_at);
}

export function agentBindingIds(robots, workspaceId, agentId, defaultWorkspaceId = "", agentName = "") {
  return robots.filter(robot => robot.purpose !== "verification" &&
    (robot.workspaceId || defaultWorkspaceId) === workspaceId &&
    (String(robot.agentId || robot.agent) === agentId || (!robot.agentId && agentName &&
      String(robot.agent || "").toLowerCase() === agentName.toLowerCase()))).map(robot => robot.id);
}

export function assertBindingAvailable(robots, robot, defaultWorkspaceId = "") {
  const previous = robots.find(entry => entry.id === robot.id);
  const workspaceId = robot.workspaceId || defaultWorkspaceId;
  // Existing duplicates remain editable, but may not create new duplicate pairs.
  const reference = String(robot.agentId || robot.agent || "");
  const sameAgent = previous && (previous.workspaceId || defaultWorkspaceId) === workspaceId && String(previous.agentId || previous.agent || "") === reference;
  if (!sameAgent && agentBindingIds(robots, workspaceId, reference, defaultWorkspaceId, robot.agentName || robot.agent).some(id => id !== robot.id)) {
    throw new Error("这个智能体已绑定如流机器人，请先解绑后再绑定");
  }
  const appId = String(robot.appId ?? "").trim();
  if (appId && (!previous || String(previous.appId).trim() !== appId) &&
      robots.some(entry => entry.id !== robot.id && String(entry.appId).trim() === appId)) {
    throw new Error("这个如流机器人已被绑定，请先解绑后再绑定");
  }
}

export async function resolveOwnedAgent({ account, workspaces, workspaceId, agentId = "", agentName, listAgents }) {
  const workspace = workspaces.find((entry) => entry.id === workspaceId);
  if (!workspace) throw new Error("请选择当前账号有权访问的工作区");
  const name = String(agentId || agentName || "").trim();
  if (!name) throw new Error("请选择当前账号创建的智能体");
  const candidates = ownedAgents(await listAgents(workspace.id), account.userId);
  const matches = candidates.filter(entry => agentId ? entry.id === name :
    entry.id === name || entry.name?.toLowerCase() === name.toLowerCase());
  const agent = matches.length === 1 ? matches[0] : null;
  if (!agent) throw new Error("只能绑定当前账号创建的智能体，请刷新列表后重新选择");
  return { agent, workspace };
}
