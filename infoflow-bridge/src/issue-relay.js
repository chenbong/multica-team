import { createHash } from "node:crypto";
import { readFileSync, writeFileSync, renameSync, mkdirSync, existsSync } from "node:fs";
import { readFile } from "node:fs/promises";
import { basename, extname } from "node:path";
import { issueRequest } from "./chat.js";
import { messageIdString } from "./conversation-context.js";
import { withReplyMode, noticeReplyMode } from "./reply-format.js";

// The service credential only authorizes the trusted inbound bridge. Never put
// it in agent prompts, CLI profiles, robot admin responses, or daemon requests.
export class IssueRelay {
  constructor(config, multica, send, logger = console, { fetchImpl = fetch, token = process.env.INFOFLOW_ISSUE_RELAY_TOKEN || "", stageImages = null, groups = null } = {}) {
    this.config = config;
    this.multica = multica;
    this.send = send;
    this.logger = logger;
    this.fetch = fetchImpl;
    this.token = token;
    this.stageImages = stageImages;
    this.groups = groups;
    this.enabled = config.issueRelay?.enabled === true;
    this.queues = new Map();
    this.stopped = false;
    const scope = JSON.stringify([config.robotId, config.multica.workspaceId, config.multica.defaultAgent]);
    this.path = config.statePath + ".issue-deliveries-" + createHash("sha256").update(scope).digest("hex").slice(0, 16) + ".json";
    this.inboxPath = this.path + ".inbox";
    this.quarantinePath = this.path + ".quarantine";
    this.sentParts = {};
    this.inbox = {};
    if (this.enabled) {
      try { this.sentParts = JSON.parse(readFileSync(this.path, "utf8")); }
      catch (error) { if (error.code !== "ENOENT") throw error; }
      try { this.inbox = JSON.parse(readFileSync(this.inboxPath, "utf8")); }
      catch (error) { if (error.code !== "ENOENT") throw error; }
      this.timer = setInterval(() => void this.poll(), config.pollIntervalMs || 5000);
      this.timer.unref?.();
    }
  }

  stop() { this.stopped = true; clearInterval(this.timer); }

  async scope() {
    const agentRef = String(this.config.multica.defaultAgent || "");
    // UUID references require no owner-account lookup. Name lookup is solely
    // configuration resolution; all incoming actions use server-resolved users.
    if (/^[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}$/i.test(agentRef)) {
      return { robot_id: this.config.robotId, agent_id: agentRef, workspace_id: this.config.multica.workspaceId };
    }
    const agents = await this.multica.listAgents();
    const matches = agents.filter((agent) => agent.id === agentRef || agent.name === agentRef);
    if (matches.length !== 1) throw new Error("issue relay agent configuration is missing or ambiguous");
    return { robot_id: this.config.robotId, agent_id: matches[0].id, workspace_id: this.config.multica.workspaceId };
  }

  async request(path, body) {
    if (this.token.length < 32) throw new Error("issue relay service credential is not configured");
    const base = String(this.config.multica.serverUrl || "").replace(/\/+$/, "");
    if (!/^https?:\/\//.test(base)) throw new Error("issue relay server URL is not configured");
    const response = await this.fetch(base + "/api/integrations/infoflow/" + path, {
      method: "POST", redirect: "error", signal: AbortSignal.timeout(30000),
      headers: { Authorization: `Bearer ${this.token}`, "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    if (!response.ok) {
      const error = new Error(`issue relay returned HTTP ${response.status}`);
      error.status = response.status;
      throw error;
    }
    const result = await response.json();
    if (!result || typeof result !== "object" || Array.isArray(result)) throw new Error("invalid issue relay response");
    return result;
  }

  async handle(message) {
    if (!this.enabled) return false;
    if (this.stopped || !message.messageId || (typeof message.messageId === "number" && !Number.isSafeInteger(message.messageId))) return true;
    const key = JSON.stringify([message.conversation.kind, String(message.conversation.id)]);
    const id = createHash("sha256").update(JSON.stringify([key, String(message.messageId)])).digest("hex");
    if (existsSync(joinPath(this.quarantinePath, id))) return true;
    if (!this.inbox[id]) {
      if (Object.keys(this.inbox).length >= 500) {
        this.logger.warn("issue relay inbox is full; input was not accepted");
        return true;
      }
      const input = { conversation: message.conversation, user: message.user, text: message.text, messageId: String(message.messageId),
        replyToMessageId: message.replyToMessageId || null, images: message.images || [], context: message.context || null };
      this.inbox[id] = { key, input, createdAt: Date.now() };
      try { this.saveInbox(); }
      catch (error) { delete this.inbox[id]; this.logger.warn("issue relay inbox persistence failed"); return true; }
    }
    return this.serial(key, () => this.drain(key, id));
  }

  serial(key, action) {
    const prior = this.queues.get(key) || Promise.resolve();
    const pending = prior.catch(() => {}).then(action);
    this.queues.set(key, pending);
    return pending.finally(() => { if (this.queues.get(key) === pending) this.queues.delete(key); });
  }

  async drain(key, currentId = null) {
    let handled = true;
    for (const [id, record] of Object.entries(this.inbox)) {
      if (record.key !== key || this.stopped) continue;
      try {
        if (record.quarantined) { this.archiveQuarantined(id, record); continue; }
        if (record.chatSubmitted) { delete this.inbox[id]; this.saveInbox(); continue; }
        if (record.chatSubmitting) {
          const error = new Error("Chat submission outcome uncertain; inspect retained input before retrying");
          error.status = 409;
          throw error;
        }
        const result = await this.handleSerial(record.input, record);
        if (id === currentId) handled = result;
        // A recovered unbound Chat input cannot be dropped silently. The main
        // Bridge installs a fallback that reuses its normal Chat deduplication.
        if (!result && this.fallback) {
          await this.fallback(record.input, {
            beforeSubmit: () => { record.chatSubmitting = true; this.saveInbox(); },
            afterSubmit: () => { record.chatSubmitted = true; this.saveInbox(); },
          });
          if (id === currentId) handled = true;
        } else if (!result && id !== currentId) {
          throw new Error("unbound message awaits Chat handoff");
        }
        delete this.inbox[id];
        this.saveInbox();
      } catch (error) {
        if (id === currentId) handled = true;
        if (record.chatSubmitted) {
          // Chat already persisted its task ID; a failed receipt is not a
          // reason to submit the same user request a second time.
          delete this.inbox[id]; this.saveInbox();
          if (id === currentId) handled = true;
          continue;
        }
        record.lastErrorAt = Date.now();
        record.lastStatus = error.status || null;
        // Invalid protocol input or a conflicting idempotency key cannot heal
        // through retries. Preserve it for operators without blocking new input.
        if ([400, 409, 413].includes(error.status) || record.chatSubmitting) record.quarantined = true;
        if (record.quarantined) {
          this.archiveQuarantined(id, record);
          this.logger.warn("issue relay input quarantined for operator review");
          continue;
        }
        this.saveInbox();
        this.logger.warn("issue relay input pending: " + error.message);
        // Preserve order within this conversation; other conversations may run.
        break;
      }
    }
    return handled;
  }

  archiveQuarantined(id, record) {
    mkdirSync(this.quarantinePath, { recursive: true, mode: 0o700 });
    const path = joinPath(this.quarantinePath, id);
    writeFileSync(path + ".tmp", JSON.stringify(record), { mode: 0o600 });
    renameSync(path + ".tmp", path);
    delete this.inbox[id];
    this.saveInbox();
  }

  saveInbox() {
    const data = JSON.stringify(this.inbox);
    if (Buffer.byteLength(data) > 64 * 1024 * 1024) throw new Error("issue relay inbox exceeds storage budget");
    writeFileSync(this.inboxPath + ".tmp", data, { mode: 0o600 });
    renameSync(this.inboxPath + ".tmp", this.inboxPath);
  }

  async handleSerial({ conversation, user, text, messageId, replyToMessageId, images = [] }, record = {}) {
      // Missing/rounded identifiers cannot provide safe retry deduplication.
      if (!messageId || (typeof messageId === "number" && !Number.isSafeInteger(messageId))) return true;
      const scope = await this.scope();
      const parsed = relayOperation(text);
      const body = record.preparedBody || {
        ...scope, conversation: { kind: conversation.kind, id: String(conversation.id) },
        sender: user, message_id: String(messageId), operation: parsed.operation,
        ...(parsed.issue_ref ? { issue_ref: parsed.issue_ref } : {}),
        content: parsed.content,
        ...(replyToMessageId ? { reply_to_message_id: String(replyToMessageId) } : {}),
      };
      let staged;
      let result;
      try {
        if (this.groups && conversation.kind === "group" && ["message", "binding"].includes(parsed.operation)) {
          const preflight = await this.request("issue-relay", { ...body, operation: "binding", content: "" });
          this.validateDecision(preflight);
          if (preflight.binding && !preflight.binding.group) throw new Error("group binding is missing its coordination plan");
          if (preflight.binding?.group) {
            const checked = await this.reconcileGroup(scope, conversation, preflight.binding, { invite: preflight.can_reconcile_group === true, bootstrap: true });
            if (checked.binding?.group?.state !== "active") {
              await this.sendNotice(conversation, !preflight.can_comment && preflight.notice ? preflight : checked);
              return true;
            }
          } else if (preflight.handled && !preflight.can_comment) {
            await this.sendNotice(conversation, preflight);
            return true;
          }
        }
        if (!record.preparedBody && images.length && ["message", "create"].includes(parsed.operation)) {
          const preflight = await this.request("issue-relay", { ...body, operation: "binding", content: "", ...(parsed.operation === "create" ? { command: "create" } : {}) });
          this.validateDecision(preflight);
          if (preflight.handled && !preflight.can_comment) {
            await this.sendNotice(conversation, preflight);
            return true;
          }
          if (!preflight.binding && parsed.operation === "message") return false;
          if (images.length > 5) {
            await this.sendNotice(conversation, { ...preflight, notice: "一次最多发送 5 张图片，请分开发送。" });
            return true;
          }
          if (!this.stageImages) throw new Error("issue image staging is not configured");
          staged = await this.stageImages(images);
          body.attachments = [];
          let totalBytes = 0;
          for (const path of staged.paths) {
            const bytes = await readFile(path);
            totalBytes += bytes.length;
            if (totalBytes > 20 * 1024 * 1024) {
              await this.sendNotice(conversation, { ...preflight, notice: "图片合计超过 20MB，请分开发送。" });
              return true;
            }
            const content_type = ({ ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp" })[extname(path).toLowerCase()] || "application/octet-stream";
            body.attachments.push({ filename: basename(path), content_type, data_base64: bytes.toString("base64") });
          }
          if (staged.failed.length) body.content += "\n\n图片附件处理失败：\n" + staged.failed.join("\n");
        }
        record.preparedBody = body;
        this.saveInbox();
        result = await this.request("issue-relay", body);
      } finally { await staged?.cleanup(); }
      this.validateDecision(result);
      if (this.groups && conversation.kind === "group" && result.binding?.group && ["bind", "create"].includes(parsed.operation) && result.can_reconcile_group === true) {
        // Pending bind/create notices must not claim success until the leader
        // (or the single assigned agent) is observed in the real group.
        const checked = await this.reconcileGroup(scope, conversation, result.binding, { invite: true, bootstrap: true });
        result = checked;
      }
      if (!result.handled) {
        if (parsed.explicitBinding) {
          await this.sendNotice(conversation, { notice: "当前会话未绑定任务。使用 /bind <任务编号> 绑定，或 /issue <内容> 新建并绑定。" });
          return true;
        }
        if (parsed.operation !== "message" && parsed.operation !== "binding") return true;
        return false;
      }
      if (parsed.operation === "binding" && result.binding && result.can_comment && !result.notice) {
        result.notice = `当前绑定任务：${result.binding.identifier || result.binding.issue_id}${result.binding.title ? " " + result.binding.title : ""}\n后续消息会发布到任务评论；使用 /unbind 解除绑定后恢复普通聊天。`;
      }
      await this.sendNotice(conversation, result);
      return true;
  }

  validateDecision(result) {
    if (typeof result?.handled !== "boolean" || typeof result?.silent !== "boolean") throw new Error("invalid issue relay decision");
  }

  async reconcileGroup(scope, conversation, binding, { invite = false, bootstrap = false } = {}) {
    if (!binding?.group || !this.groups) throw new Error("group binding coordination is unavailable");
    if (!binding.group.leader_agent_id) return { handled: true, silent: false, binding,
      notice: binding.group.reason || "请先为任务配置负责人或小队队长", observation: { leader_present: false } };
    let observation;
    try { observation = await this.groups.inspect(scope, conversation, binding, { invite, bootstrap }); }
    catch { observation = { leader_present: false, leader_robot_id: binding.group.leader_robot_id || "", reason: "membership_check_failed" }; }
    const response = await this.request("issue-group-status", {
      ...scope,
      ...(observation.leader_robot_id ? { robot_id: observation.leader_robot_id, agent_id: binding.group.leader_agent_id } : {}),
      conversation: { kind: "group", id: String(conversation.id) }, generation: binding.generation,
      leader_agent_id: binding.group.leader_agent_id || "", leader_robot_id: observation.leader_robot_id || "",
      leader_present: observation.leader_present,
    });
    this.validateDecision(response);
    if (!response.binding?.group || response.binding.generation !== binding.generation ||
        response.binding.group.leader_agent_id !== binding.group.leader_agent_id) {
      throw new Error("group binding changed during membership verification");
    }
    if (!observation.leader_present && response.binding.group.state === "active") throw new Error("group activated without confirmed leader");
    if (!observation.leader_present) {
      const reasons = {
        leader_robot_missing_or_ambiguous: "负责人机器人未配置或有多个匹配，请先确认唯一机器人。",
        leader_robot_unavailable: "负责人机器人不可用，请先启用或恢复连接。",
        membership_check_failed: "无法确认群成员，请检查获取群成员列表的 API 权限或网络。",
        approval_pending: "入群申请等待群管理员审批。",
        invitation_failed: "邀请负责人机器人失败，请检查添加群成员的 API 权限，或手动邀请入群。",
        invitation_pending: "入群请求结果待确认，请检查群成员或管理员审批。",
        membership_unconfirmed: "已提交入群请求，尚未确认负责人机器人已入群。",
        leader_not_in_group: "负责人机器人不在群中，请先邀请入群。",
      };
      if (reasons[observation.reason]) response.notice = `${response.notice || "群聊任务绑定尚未启用"}\n${reasons[observation.reason]}`;
    }
    return { ...response, observation };
  }

  async reconcileGroups(scope) {
    const result = await this.request("issue-groups", scope);
    if (!Array.isArray(result.groups)) throw new Error("invalid issue group list");
    const checked = new Map();
    for (const group of result.groups) {
      if (group.conversation?.kind !== "group" || !group.conversation.id || !group.binding?.group) throw new Error("invalid issue group plan");
      if (!(await this.groups.owns(scope, group.binding))) continue;
      const state = await this.reconcileGroup(scope, group.conversation, group.binding, { invite: true });
      checked.set(String(group.conversation.id), state);
    }
    return checked;
  }

  async sendNotice(conversation, result) {
    if (result.silent || typeof result.notice !== "string" || !result.notice) return;
    for (const part of this.replyParts(result.notice, result.binding)) {
      if (this.stopped) throw new Error("issue relay stopped before notice delivery");
      await this.send(conversation, withReplyMode(part, noticeReplyMode(result)), {});
    }
  }

  replyParts(content, binding, maxBytes = 1800) {
    if (!binding) return splitMessage(content);
    const ref = binding.identifier || binding.issue_id;
    const slug = binding.workspace_slug || this.config.multica.workspaceSlug;
    if (!ref || !slug) throw new Error("issue link metadata is missing");
    const base = new URL(this.config.multica.webUrl);
    if (!["http:", "https:"].includes(base.protocol)) throw new Error("invalid issue link URL");
    base.pathname = base.pathname.replace(/\/$/, "") + `/${encodeURIComponent(slug)}/issues/${encodeURIComponent(ref)}`;
    base.search = ""; base.hash = "";
    const footer = "\n\n完整任务对话：" + base.href;
    const parts = splitMessage(content, Math.max(200, maxBytes - Buffer.byteLength(footer)));
    return parts.length ? parts.map(part => part + footer) : [footer.trimStart()];
  }

  save() {
    writeFileSync(this.path + ".tmp", JSON.stringify(this.sentParts), { mode: 0o600 });
    renameSync(this.path + ".tmp", this.path);
  }

  async poll() {
    if (!this.enabled || this.stopped || this.polling) return;
    this.polling = true;
    try {
      const scope = await this.scope();
      let groupPlans = null;
      if (this.groups) {
        try { groupPlans = await this.reconcileGroups(scope); }
        catch { this.logger.warn("issue group reconciliation pending; group output paused"); }
      }
      for (const key of new Set(Object.values(this.inbox).map(record => record.key))) {
        if (this.stopped) return;
        await this.serial(key, () => this.drain(key));
      }
      const result = await this.request("issue-deliveries", { ...scope, limit: 20 });
      if (!Array.isArray(result.deliveries)) throw new Error("invalid issue delivery list");
      for (const item of result.deliveries) {
        if (this.stopped) break;
        try {
        const deliver = async () => {
        if (typeof item.id !== "string" || typeof item.content !== "string" ||
            !["private", "group"].includes(item.conversation?.kind) || !item.conversation?.id) {
          throw new Error("invalid issue delivery");
        }
        // New group checkpoints reserve attribution bytes (including 120
        // Unicode author characters); private/legacy digests remain unchanged.
        const parts = this.replyParts(item.content, item.binding || item,
          this.groups && item.conversation.kind === "group" ? 1200 : 1800);
        const digest = createHash("sha256").update(parts.join("")).digest("hex");
        const shared = this.groups && item.conversation.kind === "group";
        const checkpoints = shared ? this.groups.deliveries : this.sentParts;
        const save = () => shared ? this.groups.saveDeliveries() : this.save();
        const prior = checkpoints[item.id];
        if (prior?.acked) return;
        if (shared && !prior && this.sentParts[item.id]) throw new Error("legacy per-robot group checkpoint requires operator review");
        if (prior && prior.digest !== digest) throw new Error("issue delivery content changed during retry");
        let route = null;
        if (shared) {
          const plan = groupPlans?.get(String(item.conversation.id));
          if (!plan || plan.binding.group.state !== "active" ||
              plan.binding.generation !== item.binding?.generation || plan.binding.issue_id !== item.binding?.issue_id ||
              plan.binding.group.leader_robot_id !== scope.robot_id) throw new Error("group delivery is not owned by this active leader");
          if (prior && !prior.route) throw new Error("legacy group checkpoint requires operator review");
          const checked = await this.reconcileGroup(scope, item.conversation, plan.binding);
          if (checked.binding.group.state !== "active") throw new Error("group delivery paused until leader rejoins");
          // A poll snapshot can outlive squad membership. Author admission must
          // use the freshly checked topology, not the original delivery view.
          if (!prior || prior.next < parts.length) route = this.groups.selectSender(checked.observation, { ...item, binding: checked.binding }, prior?.route);
          if (!prior) {
            checkpoints[item.id] = { next: 0, digest, messageIds: [], route: route.pin };
            // Pin before the first external send; a retry must never switch bots.
            save();
          }
        }
        for (let index = prior?.next || 0; index < parts.length; index += 1) {
          if (this.stopped) return;
          // Decorate only at send time: existing checkpoints/digests remain
          // based on the unmodified parts, so adding a label cannot replay them.
          if (route) {
            const checked = await this.reconcileGroup(scope, item.conversation, item.binding);
            if (checked.binding.group.state !== "active") throw new Error("group delivery paused");
            route = this.groups.selectSender(checked.observation, { ...item, binding: checked.binding }, route.pin);
          }
          const receipt = await (route?.send || this.send)(item.conversation,
            withReplyMode((route?.pin.label || "") + parts[index], "issue"), {});
          const messageId = messageIdString(receipt?.messageid ?? receipt?.messageId);
          const messageIds = [...(checkpoints[item.id]?.messageIds || [])];
          if (messageId) messageIds.push(messageId);
          checkpoints[item.id] = { next: index + 1, digest, messageIds, ...(route ? { route: route.pin } : {}) };
          save();
        }
        if (this.stopped) return;
        const ids = checkpoints[item.id]?.messageIds || [];
        await this.request(`issue-deliveries/${encodeURIComponent(item.id)}/ack`, {
          ...scope, ...(ids.length ? { message_id: ids.at(-1), message_ids: ids } : {}),
        });
        // Group receipts are process-wide and survive coordinator reassignment.
        // Keep a tombstone so a second timer with an older delivery snapshot
        // cannot send it again after the first coordinator already acknowledged.
        if (shared) checkpoints[item.id].acked = true;
        else delete checkpoints[item.id];
        save();
        };
        if (this.groups && item.conversation?.kind === "group") await this.groups.deliveryLock(item.id, deliver);
        else await deliver();
        } catch (error) { this.logger.warn("issue relay delivery pending: " + error.message); }
      }
    } catch (error) { this.logger.warn("issue relay delivery pending: " + error.message); }
    finally { this.polling = false; }
  }
}

function joinPath(directory, id) {
  // The id is a locally generated SHA-256, never an external path component.
  if (!/^[a-f0-9]{64}$/.test(id)) throw new Error("invalid inbox key");
  return directory + "/" + id + ".json";
}

export function relayOperation(text) {
  const match = String(text).trim().match(/^\/(bind|binding|unbind)(?:\s+([\s\S]*))?$/i);
  if (match) return { operation: match[1].toLowerCase(), issue_ref: (match[2] || "").trim(), content: "", explicitBinding: true };
  const create = issueRequest(String(text).trim());
  if (create !== null) return { operation: "create", content: create };
  // Bound conversations must not escape to legacy owner-authority status or
  // agent-switch commands. A binding lookup handles commands before fallback.
  if (String(text).trim().startsWith("/")) return { operation: "binding", content: "" };
  return { operation: "message", content: String(text) };
}

// Conservative byte chunks preserve all characters, including surrogate pairs,
// and stay below the ordinary Markdown and whole-request size limits.
export function splitMessage(content, maxBytes = 1800) {
  const parts = [];
  let part = "", bytes = 0;
  for (const char of String(content)) {
    const size = Buffer.byteLength(char);
    if (bytes + size > maxBytes && part) { parts.push(part); part = ""; bytes = 0; }
    part += char; bytes += size;
  }
  if (part) parts.push(part);
  return parts;
}
