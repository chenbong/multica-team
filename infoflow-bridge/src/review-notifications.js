import { readFileSync, writeFileSync, renameSync } from "node:fs";

export class ReviewNotifications {
  constructor(runtime, { intervalMs = 15000, now = () => Date.now() } = {}) {
    this.runtime = runtime;
    this.now = now;
    this.path = runtime.config.statePath + ".review-notifications.json";
    try { this.state = JSON.parse(readFileSync(this.path, "utf8")); }
    catch (error) { if (error.code !== "ENOENT") throw error; this.state = { bindings: {} }; }
    this.timer = setInterval(() => void this.poll(), intervalMs);
    this.timer.unref?.();
  }
  stop() { this.stopped = true; clearInterval(this.timer); }
  save() {
    writeFileSync(this.path + ".tmp", JSON.stringify(this.state), { mode: 0o600 });
    renameSync(this.path + ".tmp", this.path);
  }
  async poll() {
    if (this.busy || this.stopped) return;
    this.busy = true;
    try {
      const visited = new Set();
      for (const instance of this.runtime.instances.values()) {
        const { robot, bridge } = instance;
        if (robot.purpose !== "task" || !robot.enabled || this.stopped) continue;
        const key = JSON.stringify([robot.appId, robot.profile, robot.workspaceId, robot.agent]);
        if (visited.has(key)) continue;
        visited.add(key);
        const state = this.state.bindings[key] ||= { since: this.now(), versions: {}, sent: {}, pending: {} };
        this.save();
        try { await this.scan(instance, state); }
        catch (error) { this.runtime.logger.warn(`[${robot.name}] review notification scan failed: ${error.message}`); }
      }
    } finally { this.busy = false; }
  }
  async scan({ robot, bridge }, state) {
    const m = bridge.multica;
    const me = await m.request("/api/me");
    const owner = String(me.email || "").split("@")[0];
    if (!owner || !String(me.email).includes("@")) throw new Error("无法确认机器人绑定账号的邮箱");
    // New bindings record the page's authenticated user. Legacy bindings use
    // the profile's verified identity, never the arbitrary private-chat allowlist.
    if (robot.boundByEmail && robot.boundByEmail.toLowerCase() !== me.email.toLowerCase()) throw new Error("绑定者与当前 profile 身份不一致，暂停通知");
    const agents = await m.listAgents();
    const agent = agents.find(a => a.id === robot.agent || a.name === robot.agent);
    if (!agent) return;
    const notificationCategories = new Set(["in_review", "blocked"]);
    const statusCategories = new Map([["in_review", "in_review"], ["blocked", "blocked"]]);
    const catalog = await m.request("/api/issue-statuses?include_archived=true");
    for (const entry of (Array.isArray(catalog) ? catalog : catalog.statuses || [])) {
      if (notificationCategories.has(entry.category)) statusCategories.set(entry.key, entry.category);
    }
    // Scan all assigned issues, including those already moved past review, so a
    // short review transition between polls can still be found in the timeline.
    for (let offset = 0; !this.stopped;) {
      const page = await m.request(`/api/issues?assignee_id=${encodeURIComponent(agent.id)}&limit=100&offset=${offset}`);
      const issues = Array.isArray(page) ? page : page.issues;
      if (!Array.isArray(issues)) throw new Error("Unexpected issue list response");
      for (const issue of issues) {
        if (issue.assignee_type !== "agent" || issue.assignee_id !== agent.id) continue;
        const version = String(issue.revision ?? issue.updated_at);
        if (state.versions[issue.id] === version) continue;
        const timeline = await m.request(`/api/issues/${issue.id}/timeline`);
        if (!Array.isArray(timeline)) throw new Error("Unexpected timeline response");
        for (const event of timeline) {
          const toCategory = statusCategories.get(event.details?.to);
          const fromCategory = statusCategories.get(event.details?.from);
          if (event.action !== "status_changed" || !toCategory || fromCategory === toCategory) continue;
          if (Date.parse(event.created_at) < state.since || state.sent[event.id] || state.pending[event.id]) continue;
          const comment = timeline.filter(e => e.type === "comment" && e.actor_type === "agent" && e.actor_id === agent.id && e.content).at(-1);
          const stateLabel = toCategory === "blocked" ? "已阻塞" : "已进入审核中";
          state.pending[event.id] = {
            issueId: issue.id, owner, at: event.created_at,
            content: `**任务${stateLabel}：${issue.identifier}**\n\n${issue.title}\n智能体：${agent.name}\n` +
              (comment ? `\n完成摘要：\n${comment.content.slice(0, 1600)}\n` : "\n请打开任务查看执行结果并审核。\n") +
              `\n${m.issueUrl(issue)}`,
          };
        }
        state.versions[issue.id] = version;
        this.save();
      }
      offset += issues.length;
      if (!issues.length || Array.isArray(page) || offset >= page.total) break;
    }
    for (const [eventId, notification] of Object.entries(state.pending)) {
      if (this.stopped) break;
      if (notification.owner !== owner) continue;
      try {
        await this.runtime.sendDirect(robot.id, owner, notification.content);
        state.sent[eventId] = { at: this.now(), issueId: notification.issueId };
        delete state.pending[eventId];
        this.save();
        this.runtime.logger.log(`[${robot.name}] review notification delivered: ${notification.issueId}`);
      } catch (error) {
        this.runtime.logger.warn(`[${robot.name}] review notification will retry: ${error.message}`);
      }
    }
  }
}
