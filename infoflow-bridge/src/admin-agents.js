// The agent API records the creator as owner_id. Shared access, including
// workspace administration, does not make an agent a binding candidate here.
export function ownedAgents(agents, userId) {
  if (!userId || !Array.isArray(agents)) return [];
  return agents.filter((agent) => agent?.owner_id === userId);
}

export function agentBindingIds(robots, workspaceId, agentName, defaultWorkspaceId = "") {
  return robots.filter(robot => robot.purpose !== "verification" &&
    (robot.workspaceId || defaultWorkspaceId) === workspaceId && robot.agent === agentName).map(robot => robot.id);
}

export function assertBindingAvailable(robots, robot, defaultWorkspaceId = "") {
  const previous = robots.find(entry => entry.id === robot.id);
  const workspaceId = robot.workspaceId || defaultWorkspaceId;
  // Existing duplicates remain editable, but may not create new duplicate pairs.
  const sameAgent = previous && (previous.workspaceId || defaultWorkspaceId) === workspaceId && previous.agent === robot.agent;
  if (!sameAgent && agentBindingIds(robots, workspaceId, robot.agent, defaultWorkspaceId).some(id => id !== robot.id)) {
    throw new Error("这个智能体已绑定如流机器人，请先解绑后再绑定");
  }
  const appId = String(robot.appId ?? "").trim();
  if (appId && (!previous || String(previous.appId).trim() !== appId) &&
      robots.some(entry => entry.id !== robot.id && String(entry.appId).trim() === appId)) {
    throw new Error("这个如流机器人已被绑定，请先解绑后再绑定");
  }
}

export async function resolveOwnedAgent({ account, workspaces, workspaceId, agentName, listAgents }) {
  const workspace = workspaces.find((entry) => entry.id === workspaceId);
  if (!workspace) throw new Error("请选择当前账号有权访问的工作区");
  const name = String(agentName ?? "").trim();
  if (!name) throw new Error("请选择当前账号创建的智能体");
  const candidates = ownedAgents(await listAgents(workspace.id), account.userId);
  const agent = candidates.find((entry) => entry.name === name);
  if (!agent) throw new Error("只能绑定当前账号创建的智能体，请刷新列表后重新选择");
  return { agent, workspace };
}
