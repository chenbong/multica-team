import { createHash } from "node:crypto";
import { readFileSync, writeFileSync, renameSync, mkdirSync, existsSync } from "node:fs";
import { readFile } from "node:fs/promises";
import { basename, extname } from "node:path";
import { issueRequest } from "./chat.js";
import { messageIdString } from "./conversation-context.js";
import { withReplyMode, noticeReplyMode } from "./reply-format.js";
import { pollWebGroupJobs } from "./web-group-bindings.js";
import { pollWebPrivateJobs, privateRobotMetadata } from "./web-private-bindings.js";

const DEFERRED_BINDING = Symbol("deferred-binding");

// The service credential only authorizes the trusted inbound bridge. Never put
// it in agent prompts, CLI profiles, robot admin responses, or daemon requests.
export class IssueRelay {
  constructor(config, multica, send, logger = console, { fetchImpl = fetch, token = process.env.INFOFLOW_ISSUE_RELAY_TOKEN || "", stageImages = null, groups = null, getPrivateRobotInfo = null, privateRobotInfoTimeoutMs = 10000 } = {}) {
    this.config = config;
    this.multica = multica;
    this.send = send;
    this.logger = logger;
    this.fetch = fetchImpl;
    this.token = token;
    this.stageImages = stageImages;
    this.groups = groups;
    this.getPrivateRobotInfo = getPrivateRobotInfo;
    this.privateRobotInfoTimeoutMs = privateRobotInfoTimeoutMs;
    this.enabled = config.issueRelay?.enabled === true;
    this.queues = new Map();
    this.stopped = false;
    const scope = JSON.stringify([config.robotId, config.multica.workspaceId, config.multica.defaultAgent]);
    this.path = config.statePath + ".issue-deliveries-" + createHash("sha256").update(scope).digest("hex").slice(0, 16) + ".json";
    this.inboxPath = this.path + ".inbox";
    this.quarantinePath = this.path + ".quarantine";
    this.decisionsPath = this.path + ".decisions";
    this.sentParts = {};
    this.inbox = {};
    if (this.enabled) {
      try { this.sentParts = JSON.parse(readFileSync(this.path, "utf8")); }
      catch (error) { if (error.code !== "ENOENT") throw error; }
      try { this.inbox = JSON.parse(readFileSync(this.inboxPath, "utf8")); }
      catch (error) { if (error.code !== "ENOENT") throw error; }
      this.timer = setInterval(() => void this.poll(), config.pollIntervalMs || 5000);
      this.timer.unref?.();
      if (this.groups) {
        // Web admission has its own timer: slow membership/invitation calls
        // must not delay established group leases, input retries or deliveries.
        this.webJobTimer = setInterval(() => void this.pollWebBindings(), config.pollIntervalMs || 5000);
        this.webJobTimer.unref?.();
        this.webPrivateJobTimer = setInterval(() => void this.pollWebPrivateBindings(), config.pollIntervalMs || 5000);
        this.webPrivateJobTimer.unref?.();
      }
    }
  }

  stop() {
    this.stopped = true;
    clearInterval(this.timer); clearInterval(this.webJobTimer); clearInterval(this.webPrivateJobTimer);
    this.timer = null; this.webJobTimer = null; this.webPrivateJobTimer = null;
  }

  async pollWebBindings() {
    if (!this.enabled || this.stopped || !this.groups || this.webPolling) return;
    this.webPolling = true;
    try {
      const scope = await this.scope();
      if (!this.stopped) await pollWebGroupJobs(this, scope);
    } catch { if (!this.stopped) this.logger.warn("web group binding pending"); }
    finally { this.webPolling = false; }
  }

  async pollWebPrivateBindings() {
    if (!this.enabled || this.stopped || !this.groups || this.webPrivatePolling) return;
    this.webPrivatePolling = true;
    try {
      const scope = await this.scope();
      if (!this.stopped) await pollWebPrivateJobs(this, scope);
    } catch { if (!this.stopped) this.logger.warn("web private binding pending"); }
    finally { this.webPrivatePolling = false; }
  }

  async observeGroupEvent({ groupId, appId, client }) {
    if (!this.enabled || this.stopped || !this.groups) return;
    try {
      const scope = await this.scope();
      if (this.stopped) return;
      await this.groups.observeGroup(scope, { robotId: this.config.robotId, appId, client }, groupId,
        async body => {
          if (this.stopped) return;
          const response = await this.request("infoflow-group-observation", body);
          if (response.ok !== true) throw new Error("invalid group observation result");
        });
    } catch { this.logger.warn("group discovery pending; existing messages are unaffected"); }
  }

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

  async privateBindingMetadata() {
    if (typeof this.getPrivateRobotInfo !== "function") return {};
    let timer;
    try {
      const info = await Promise.race([
        Promise.resolve().then(() => this.getPrivateRobotInfo()),
        new Promise((_, reject) => { timer = setTimeout(() => reject(new Error("metadata timeout")), this.privateRobotInfoTimeoutMs); }),
      ]);
      const metadata = privateRobotMetadata(info);
      if (metadata.reason) return {};
      return { robot_chat_id: metadata.robot_chat_id, robot_name: metadata.robot_name };
    } catch {
      // Public display metadata is optional; a failed read must not lose an IM
      // bind or expose SDK errors/credentials. A later explicit bind can retry.
      this.logger.warn("private robot display metadata unavailable");
      return {};
    } finally { clearTimeout(timer); }
  }

  async handle(message) {
    if (!this.enabled) return false;
    if (this.stopped || !message.messageId || (typeof message.messageId === "number" && !Number.isSafeInteger(message.messageId))) return true;
    const key = JSON.stringify([message.conversation.kind, String(message.conversation.id)]);
    const id = createHash("sha256").update(JSON.stringify([key, String(message.messageId)])).digest("hex");
    if (existsSync(joinPath(this.quarantinePath, id)) || existsSync(joinPath(this.decisionsPath, id))) return true;
    if (!this.inbox[id]) {
      if (Object.keys(this.inbox).length >= 500) {
        this.logger.warn("issue relay inbox is full; input was not accepted");
        return true;
      }
      const input = { conversation: message.conversation, user: message.user, text: message.text, messageId: String(message.messageId),
        replyToMessageId: message.replyToMessageId || null, images: message.images || [], context: message.context || null,
        ...(message.receipt ? { receipt: message.receipt } : {}) };
      this.inbox[id] = { key, input, createdAt: Date.now() };
      try { this.saveInbox(); }
      catch (error) { delete this.inbox[id]; this.logger.warn("issue relay inbox persistence failed"); return true; }
    }
    // Functions stay in memory; JSON persistence retains only the receipt IDs.
    if (typeof message.acknowledge === "function") this.inbox[id].acknowledge = message.acknowledge;
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
    const blockedBindings = new Set();
    for (const [id, record] of Object.entries(this.inbox)) {
      if (record.key !== key || this.stopped) continue;
      try {
        // A crash may leave an inbox entry after its terminal no-route marker
        // was committed. Do not resend its notice or reinterpret its target.
        if (existsSync(joinPath(this.decisionsPath, id))) { delete this.inbox[id]; this.saveInbox(); continue; }
        if (record.quarantined) { this.archiveQuarantined(id, record); continue; }
        if (record.chatSubmitted) { delete this.inbox[id]; this.saveInbox(); continue; }
        if (record.chatSubmitting) {
          const error = new Error("Chat submission outcome uncertain; inspect retained input before retrying");
          error.status = 409;
          throw error;
        }
        const result = await this.handleSerial(record.input, record, blockedBindings);
        if (result === DEFERRED_BINDING) continue;
        if (id === currentId) handled = result;
        // A recovered unbound Chat input cannot be dropped silently. The main
        // Bridge installs a fallback that reuses its normal Chat deduplication.
        if (!result && this.fallback) {
          await this.fallback({ ...record.input, ...(record.acknowledge ? { acknowledge: record.acknowledge } : {}) }, {
            beforeSubmit: () => { record.chatSubmitting = true; this.saveInbox(); },
            afterSubmit: () => { record.chatSubmitted = true; this.saveInbox(); },
          });
          if (id === currentId) handled = true;
        } else if (!result && id !== currentId) {
          throw new Error("unbound message awaits Chat handoff");
        }
        // Only terminal, non-selected routing decisions are completed here.
        // Accepted mutations still rely on server receipts/permission checks;
        // transport failures and failed notice sends remain in the inbox.
        if (record.routingNotice && result === true) this.completeRoutingDecision(id);
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
        // Once the authoritative target is pinned, preserve order per task,
        // not per physical conversation. Another task can keep working while this
        // task's leader/status endpoint is unavailable. Unknown targets still
        // conservatively block this conversation until routing can be proven.
        if (record.preparedBody?.expected_binding_id) {
          blockedBindings.add(record.preparedBody.expected_binding_id);
          continue;
        }
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

  completeRoutingDecision(id) {
    // Keep small per-input tombstones rather than an expiring/in-memory seen
    // set: delayed SDK replays must not become executable after a topology
    // change. No raw text, identity, task list or credentials are retained.
    mkdirSync(this.decisionsPath, { recursive: true, mode: 0o700 });
    const path = joinPath(this.decisionsPath, id);
    writeFileSync(path + ".tmp", JSON.stringify({ disposition: "no-route", completedAt: Date.now() }), { mode: 0o600 });
    renameSync(path + ".tmp", path);
  }

  saveInbox() {
    const data = JSON.stringify(this.inbox);
    if (Buffer.byteLength(data) > 64 * 1024 * 1024) throw new Error("issue relay inbox exceeds storage budget");
    writeFileSync(this.inboxPath + ".tmp", data, { mode: 0o600 });
    renameSync(this.inboxPath + ".tmp", this.inboxPath);
  }

  async handleSerial({ conversation, user, text, messageId, replyToMessageId, images = [] }, record = {}, blockedBindings = new Set()) {
      // Missing/rounded identifiers cannot provide safe retry deduplication.
      if (!messageId || (typeof messageId === "number" && !Number.isSafeInteger(messageId))) return true;
      // Once preflight hands this input to Chat, later binds must never retarget
      // it. Unknown Chat POST outcomes are quarantined by drain before this path.
      if (record.chatHandoff) return false;
      if (record.routingNotice) {
        // A failed clarification send is not permission to reinterpret the
        // same message as executable when the group topology later changes.
        await this.sendNotice(conversation, record.routingNotice);
        return true;
      }
      const scope = await this.scope();
      const parsed = relayOperation(text);
      const unbind = parsed.operation === "unbind";
      const versionedOperation = parsed.operation === "message" || unbind;
      const wasPrepared = Boolean(record.preparedBody);
      const body = record.preparedBody ? structuredClone(record.preparedBody) : {
        ...scope, conversation: { kind: conversation.kind, id: String(conversation.id) },
        sender: user, message_id: String(messageId), operation: parsed.operation,
        ...(parsed.explicitBinding && parsed.operation === "binding" ? { command: "binding" } : {}),
        ...(parsed.issue_ref ? { issue_ref: parsed.issue_ref } : {}),
        content: parsed.content,
        ...(replyToMessageId ? { reply_to_message_id: String(replyToMessageId) } : {}),
      };
      let staged;
      let result;
      let preflight;
      try {
        if (body.expected_binding_id && blockedBindings.has(body.expected_binding_id)) return DEFERRED_BINDING;
        if (wasPrepared && versionedOperation &&
            (!body.expected_binding_id || !body.expected_generation)) {
          // A legacy queued request cannot prove its original destination. Do
          // not retrofit today's binding onto it, even after a restart.
          throw Object.assign(new Error("unversioned prepared input requires operator review"), { status: 409 });
        }
        if (record.preflightPending || (!wasPrepared && (versionedOperation ||
            (this.groups && conversation.kind === "group" && parsed.operation === "binding")))) {
          // Routing is server-authoritative: preserve the text/task numbers and
          // exact quoted message ID, including when checking a pinned retry.
          preflight = await this.request("issue-relay", { ...body, operation: "binding", ...(unbind ? { command: "unbind" } : {}) });
          this.validateDecision(preflight);
          if (body.expected_binding_id && (preflight.binding?.binding_id !== body.expected_binding_id ||
              preflight.binding?.generation !== body.expected_generation)) {
            throw Object.assign(new Error("binding changed during input preparation"), { status: 409 });
          }
          if (!preflight.binding && !preflight.handled && parsed.operation === "message") {
            record.chatHandoff = true;
            this.saveInbox();
            return false;
          }
          if (!preflight.binding && (preflight.handled || unbind)) {
            // Ambiguity/unknown references are terminal notices, not a guess,
            // a Chat fallback, or a mutation with an unpinned destination.
            record.routingNotice = { handled: true, silent: preflight.silent,
              notice: preflight.notice || (unbind ? "当前对话未绑定所指定的任务。" : undefined),
              conversation_mode: preflight.conversation_mode };
            this.saveInbox();
            await this.sendNotice(conversation, record.routingNotice);
            return true;
          }
          if (versionedOperation && preflight.binding) {
            if (!preflight.binding.binding_id || !preflight.binding.generation) throw new Error("binding version is missing from preflight");
            body.expected_binding_id = preflight.binding.binding_id;
            body.expected_generation = preflight.binding.generation;
            // Freeze the first observed destination before reconciliation,
            // notices or media work can fail and leave this input pending.
            record.preparedBody = structuredClone(body);
            record.preflightPending = true;
            record.mediaPrepared = images.length === 0;
            this.saveInbox();
            if (blockedBindings.has(body.expected_binding_id)) return DEFERRED_BINDING;
          }
          if (!unbind && conversation.kind === "private" && preflight.binding?.private?.state === "needs_rebind") {
            await this.sendNotice(conversation, { ...preflight, silent: false,
              notice: preflight.notice || "任务负责人已变更，请使用 /bind <任务编号> 重新绑定。" });
            return true;
          }
          if (!unbind && this.groups && conversation.kind === "group" && preflight.binding && !preflight.binding.group) throw new Error("group binding is missing its coordination plan");
          if (!unbind && this.groups && conversation.kind === "group" && preflight.binding?.group) {
            const checked = await this.reconcileGroup(scope, conversation, preflight.binding, { invite: preflight.can_reconcile_group === true, bootstrap: true });
            if (versionedOperation && checked.binding?.generation !== body.expected_generation) {
              // Fresh leader proof may legitimately rotate the background plan,
              // but this already prepared input still belongs to its old target.
              throw Object.assign(new Error("binding revalidated after input preparation"), { status: 409 });
            }
            if (checked.binding?.group?.state !== "active") {
              await this.sendNotice(conversation, !preflight.can_comment && preflight.notice ? preflight : checked);
              return true;
            }
          }
          if (!unbind && preflight.handled && !preflight.can_comment && (images.length || !(preflight.binding?.group && this.groups && conversation.kind === "group"))) {
            await this.sendNotice(conversation, preflight);
            return true;
          }
          if (versionedOperation) {
            if (!body.expected_binding_id) throw new Error("binding version is missing from preflight");
            record.preflightPending = false;
            this.saveInbox();
          }
        }
        if (images.length && ((!wasPrepared && parsed.operation === "create") ||
            (parsed.operation === "message" && record.mediaPrepared === false))) {
          if (!preflight && parsed.operation === "create") {
            preflight = await this.request("issue-relay", { ...body, operation: "binding", content: "", command: "create" });
            this.validateDecision(preflight);
          }
          if (preflight?.handled && !preflight.can_comment) {
            await this.sendNotice(conversation, preflight);
            return true;
          }
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
          record.mediaPrepared = true;
        }
        if (!wasPrepared && conversation.kind === "private" && ["bind", "create"].includes(parsed.operation)) {
          Object.assign(body, await this.privateBindingMetadata());
          if (this.stopped) throw new Error("relay stopped before private binding");
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
        if (body.operation === "message" && body.expected_binding_id) {
          throw Object.assign(new Error("bound input cannot fall back to Chat after binding change"), { status: 409 });
        }
        if (parsed.explicitBinding) {
          await this.sendNotice(conversation, { notice: "当前会话未绑定任务。使用 /bind <任务编号> 绑定，或 /issue <内容> 新建并绑定。" });
          return true;
        }
        if (parsed.operation !== "message" && parsed.operation !== "binding") return true;
        record.chatHandoff = true;
        this.saveInbox();
        return false;
      }
      if (parsed.operation === "binding" && result.binding && result.can_comment && !result.notice) {
        result.notice = `当前绑定任务：${result.binding.identifier || result.binding.issue_id}${result.binding.title ? " " + result.binding.title : ""}\n回复该任务消息或注明任务编号可发布评论；使用 /unbind ${result.binding.identifier || result.binding.issue_id} 解除此任务绑定。`;
      }
      await this.sendNotice(conversation, result);
      return true;
  }

  validateDecision(result) {
    if (typeof result?.handled !== "boolean" || typeof result?.silent !== "boolean") throw new Error("invalid issue relay decision");
  }

  async reconcileGroup(scope, conversation, binding, { invite = false, bootstrap = false } = {}) {
    if (!binding?.binding_id || !binding?.group || !this.groups) throw new Error("group binding coordination is unavailable");
    if (!binding.group.leader_agent_id) return { handled: true, silent: false, binding,
      notice: binding.group.reason || "请先为任务配置负责人或小队队长", observation: { leader_present: false } };
    let observation;
    try { observation = await this.groups.inspect(scope, conversation, binding, { invite, bootstrap }); }
    catch { observation = { leader_present: false, leader_robot_id: binding.group.leader_robot_id || "", reason: "membership_check_failed" }; }
    const response = await this.request("issue-group-status", {
      ...scope,
      ...(observation.leader_robot_id ? { robot_id: observation.leader_robot_id, agent_id: binding.group.leader_agent_id } : {}),
      conversation: { kind: "group", id: String(conversation.id) }, binding_id: binding.binding_id, generation: binding.generation,
      leader_agent_id: binding.group.leader_agent_id || "", leader_robot_id: observation.leader_robot_id || "",
      ...(binding.group.assignment_version !== undefined ? { assignment_version: binding.group.assignment_version } : {}),
      leader_present: observation.leader_present,
    });
    this.validateDecision(response);
    const rotated = response.binding?.generation !== binding.generation;
    if (!response.binding?.group || response.binding.binding_id !== binding.binding_id ||
        response.binding.issue_id !== binding.issue_id || !response.binding.generation ||
        (rotated && (response.binding_revalidated !== true || !observation.leader_present || response.binding.group.state !== "active")) ||
        response.binding.group.assignment_version !== binding.group.assignment_version ||
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
        invitation_failed: "邀请负责人机器人失败，请检查目标机器人的群可见范围与群聊开关，以及邀请机器人的添加群成员 API 权限，或手动邀请入群。",
        invitation_pending: "入群请求结果待确认，请检查群成员或管理员审批。",
        membership_unconfirmed: "已提交入群请求，尚未确认负责人机器人已入群。",
        leader_not_in_group: "负责人机器人不在群中，请先邀请入群。",
        inviter_not_in_group: "当前群内没有可用的平台机器人，请先手动邀请机器人入群。",
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
      try {
        if (group.conversation?.kind !== "group" || !group.conversation.id || !group.binding?.binding_id || !group.binding.group) throw new Error("invalid issue group plan");
        if (!(await this.groups.owns(scope, group.binding))) continue;
        const state = await this.reconcileGroup(scope, group.conversation, group.binding, { invite: true });
        // Multiple task bindings can share one physical group/leader. A failed
        // task's topology or membership must not pause unrelated deliveries.
        checked.set(group.binding.binding_id, { ...state, conversation: group.conversation });
      } catch { this.logger.warn("issue group binding reconciliation pending"); }
    }
    return checked;
  }

  async sendNotice(conversation, result) {
    if (result.silent || typeof result.notice !== "string" || !result.notice) return;
    for (const part of this.replyParts(result.notice, result.binding)) {
      if (this.stopped) throw new Error("issue relay stopped before notice delivery");
      await this.send(conversation, withReplyMode(this.presentReplyPart(part, result.binding), noticeReplyMode(result), result.binding?.identifier), {});
    }
  }

  replyParts(content, binding, maxBytes = 1800) {
    if (!binding) return splitMessage(content);
    const footer = "\n\n完整任务对话：" + this.issueUrl(binding, this.config.multica.webUrl);
    const parts = splitMessage(content, Math.max(200, maxBytes - Buffer.byteLength(footer)));
    return parts.length ? parts.map(part => part + footer) : [footer.trimStart()];
  }

  issueUrl(binding, webUrl) {
    const ref = binding.identifier || binding.issue_id;
    const slug = binding.workspace_slug || this.config.multica.workspaceSlug;
    if (!ref || !slug) throw new Error("issue link metadata is missing");
    const base = new URL(webUrl);
    if (!["http:", "https:"].includes(base.protocol) || base.username || base.password) throw new Error("invalid issue link URL");
    base.pathname = base.pathname.replace(/\/$/, "") + `/${encodeURIComponent(slug)}/issues/${encodeURIComponent(ref)}`;
    base.search = ""; base.hash = "";
    return base.href;
  }

  presentReplyPart(part, binding) {
    if (!binding) return part;
    // Keep the canonical parts/digest for in-flight checkpoints. Presentation is
    // replaced only at send time, so a rollout cannot replay or block old parts.
    const legacy = "完整任务对话：" + this.issueUrl(binding, this.config.multica.webUrl);
    if (!part.endsWith(legacy)) throw new Error("issue reply footer is missing");
    const target = this.issueUrl(binding, this.config.multica.issueWebUrl || this.config.multica.webUrl)
      .replace(/[()]/g, c => c === "(" ? "%28" : "%29");
    return part.slice(0, -legacy.length) + `**[完整任务对话](${target})**`;
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
          const plan = groupPlans?.get(item.binding?.binding_id);
          if (!plan || plan.binding.group.state !== "active" ||
              String(plan.conversation.id) !== String(item.conversation.id) ||
              plan.binding.generation !== item.binding?.generation || plan.binding.issue_id !== item.binding?.issue_id ||
              plan.binding.group.leader_robot_id !== scope.robot_id) throw new Error("group delivery is not owned by this active leader");
          if (prior && !prior.route) throw new Error("legacy group checkpoint requires operator review");
          const checked = await this.reconcileGroup(scope, item.conversation, plan.binding);
          if (checked.binding.group.state !== "active" || checked.binding.generation !== item.binding?.generation) throw new Error("group delivery paused until current assignment is confirmed");
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
            if (checked.binding.group.state !== "active" || checked.binding.generation !== item.binding?.generation) throw new Error("group delivery paused after assignment change");
            route = this.groups.selectSender(checked.observation, { ...item, binding: checked.binding }, route.pin);
          }
          const receipt = await (route?.send || this.send)(item.conversation,
            withReplyMode((route?.pin.label || "") + this.presentReplyPart(parts[index], item.binding || item), "issue", (item.binding || item).identifier), {});
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
