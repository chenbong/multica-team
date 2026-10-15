// The agent API records the creator as owner_id. Shared access, including
// workspace administration, does not make an agent a binding candidate here.
export function ownedAgents(agents, userId) {
  if (!userId || !Array.isArray(agents)) return [];
  return agents.filter((agent) => agent?.owner_id === userId);
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
