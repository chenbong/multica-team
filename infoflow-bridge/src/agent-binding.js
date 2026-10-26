// A robot belongs to one account and one workspace. Names are display metadata;
// once resolved, identity must never fall back from an ID to a matching name.
export const AGENT_UUID = /^[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}$/i;
export const agentReference = robot => String(robot.agentId || robot.agent || "").trim();

export class AgentBindingError extends Error {
  constructor(code, message, state = "binding_error") {
    super(message);
    this.code = code;
    this.state = state;
  }
}

function invalid(code, message) { throw new AgentBindingError(code, message); }

export async function resolveRobotAgent(robot, multica) {
  const workspaceId = String(robot.workspaceId || multica.workspaceId || "");
  if (!AGENT_UUID.test(workspaceId)) invalid("workspace_missing", "绑定缺少原工作区，请在管理页重新选择工作区和智能体。");
  if (!robot.boundUserId && !robot.boundByEmail) {
    invalid("account_missing", "无法确认原绑定账号，请用原账号登录管理页并重新选择智能体。");
  }
  try {
    const me = await multica.request("/api/me");
    if (!AGENT_UUID.test(me?.id || "") || typeof me.email !== "string") throw new Error("invalid account response");
    if ((robot.boundUserId && robot.boundUserId !== me.id) ||
        (robot.boundByEmail && robot.boundByEmail.toLowerCase() !== me.email.toLowerCase())) {
      invalid("account_changed", "当前登录账号与原绑定账号不一致，请恢复原账号登录后重新校验。");
    }
    const reference = agentReference(robot);
    let agent;
    if (robot.agentId || AGENT_UUID.test(reference)) {
      if (!AGENT_UUID.test(reference)) invalid("invalid_id", "绑定的智能体 ID 无效，请重新选择智能体。");
      agent = await multica.request(`/api/agents/${encodeURIComponent(reference)}`);
      if (agent?.id !== reference) throw new Error("invalid agent response");
    } else {
      if (!reference) invalid("agent_missing", "尚未选择智能体，请在管理页完成绑定。");
      const catalog = await multica.listAgents({ includeArchived: true });
      if (!Array.isArray(catalog)) throw new Error("invalid agent catalog");
      // Case-only renames can migrate only when unique even across case variants.
      // The original account and workspace are gates, never search suggestions.
      const matches = catalog.filter(entry => entry?.workspace_id === workspaceId &&
        entry.owner_id === me.id && typeof entry.name === "string" &&
        entry.name.toLowerCase() === reference.toLowerCase());
      if (matches.length !== 1) invalid(matches.length ? "ambiguous" : "unresolved_legacy",
        matches.length ? "旧绑定名称对应多个智能体，请在原工作区手动重新选择。" :
          "无法在原账号、原工作区确认旧绑定，请重新选择智能体；不会自动匹配其他账号或工作区。");
      agent = matches[0];
    }
    if (!AGENT_UUID.test(agent?.id || "") || typeof agent.name !== "string" || !agent.name) throw new Error("invalid agent identity");
    if (agent.workspace_id !== workspaceId) invalid("workspace_changed", "智能体已不在原工作区，请检查权限并重新选择绑定。");
    if (agent.owner_id !== me.id) invalid("owner_changed", "智能体所有者已变化，原账号无法继续绑定，请由所有者重新绑定。");
    if (agent.archived_at) invalid("archived", "绑定的智能体已归档，请先恢复智能体，或选择其他智能体。");
    return { agent, ownerId: me.id, workspaceId };
  } catch (error) {
    if (error instanceof AgentBindingError) throw error;
    if (error.status === 401 || error.status === 403) invalid("permission_denied", "绑定账号登录或访问权限已失效，请恢复原账号登录及工作区权限后重试。");
    if (error.status === 404) invalid("missing_or_inaccessible", "无法访问绑定的智能体或工作区：可能已删除或权限失效，请检查后重新选择绑定。");
    throw new AgentBindingError("query_failed", "校验失败：暂时无法查询智能体，请稍后重试；现有绑定已保留。", "validation_failed");
  }
}

export class RobotAgentBinding {
  constructor(robot, multica, { onResolved = () => {}, ttlMs = 15000, now = Date.now } = {}) {
    this.robot = robot;
    this.initialName = robot.agent;
    this.multica = multica;
    this.onResolved = onResolved;
    this.ttlMs = ttlMs;
    this.now = now;
    this.health = { state: "checking", error: "正在校验智能体绑定" };
    this.checkedAt = -Infinity;
  }

  async resolve() {
    if (this.pending) return this.pending;
    if (this.now() - this.checkedAt < this.ttlMs) {
      if (this.error) throw this.error;
      return this.agent;
    }
    this.pending = this.#resolve();
    try { return await this.pending; }
    finally { this.pending = null; }
  }

  async #resolve() {
    try {
      const result = await resolveRobotAgent(this.robot, this.multica);
      await this.onResolved(result);
      this.agent = result.agent;
      this.error = null;
      this.health = { state: "healthy", agentId: result.agent.id, agentName: result.agent.name };
      return this.agent;
    } catch (error) {
      this.error = error instanceof AgentBindingError ? error :
        new AgentBindingError("query_failed", "校验失败：绑定暂时无法更新，请稍后重试；现有配置已保留。", "validation_failed");
      this.health = { state: this.error.state, code: this.error.code, error: this.error.message };
      throw this.error;
    } finally { this.checkedAt = this.now(); }
  }
}
