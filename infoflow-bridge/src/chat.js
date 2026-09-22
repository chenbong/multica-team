import { readFileSync, writeFileSync, renameSync } from "node:fs";
import { createHash } from "node:crypto";

// Only a leading, explicit creation request is routed to the issue workflow.
// Mentions, quoted instructions, progress questions and negations remain chat.
export function issueRequest(text) {
  const command = text.match(/^\/issue(?:\s+([\s\S]*))?$/i);
  if (command) return (command[1] || "").trim();
  const natural = text.match(/^(?:(?:请|麻烦)?(?:帮我)?\s*)(?:创建|新建|建立)(?:一个|一条|个)?\s*(?:issue|任务|工单)(?=\s|[：:，,]|$)[\s：:，,]*([\s\S]*)$/i);
  return natural ? natural[1].trim() : null;
}

export class ChatRelay {
  constructor(config, multica, reply, stageImages, logger = console) {
    this.config = config;
    this.multica = multica;
    this.reply = reply;
    this.stageImages = stageImages;
    this.logger = logger;
    const scope = JSON.stringify([config.robotId, config.multica.profile, config.multica.workspaceId]);
    this.path = config.statePath + ".chat-" + createHash("sha256").update(scope).digest("hex").slice(0, 16) + ".json";
    try { this.state = JSON.parse(readFileSync(this.path, "utf8")); }
    catch (error) { if (error.code !== "ENOENT") throw error; this.state = { threads: {}, pending: {} }; }
    this.queues = new Map();
    this.stopped = false;
    this.timer = setInterval(() => void this.poll(), config.pollIntervalMs || 5000);
    this.timer.unref?.();
  }

  stop() { this.stopped = true; clearInterval(this.timer); }
  key(conversation, user) { return JSON.stringify([conversation.kind, String(conversation.id), String(user)]); }
  save() {
    writeFileSync(this.path + ".tmp", JSON.stringify(this.state), { mode: 0o600 });
    renameSync(this.path + ".tmp", this.path);
  }
  serial(key, action) {
    const previous = this.queues.get(key) || Promise.resolve();
    const next = previous.catch(() => {}).then(action);
    this.queues.set(key, next);
    return next.finally(() => { if (this.queues.get(key) === next) this.queues.delete(key); });
  }
  reset(conversation, user) {
    const key = this.key(conversation, user);
    return this.serial(key, async () => {
      delete this.state.threads[key];
      this.save();
      await this.reply(conversation, user, "已开启新对话。之后的消息使用新的聊天上下文；之前正在执行的对话会继续完成。\n使用 /issue <内容> 创建任务。");
    });
  }

  async send(conversation, user, text, images, agentName, acknowledge = null) {
    const key = this.key(conversation, user);
    return this.serial(key, async () => {
      const agents = await this.multica.listAgents();
      const agent = agents.find((entry) => entry.name === agentName || entry.id === agentName);
      if (!agent) throw new Error(`找不到绑定的智能体「${agentName}」`);
      let thread = this.state.threads[key];
      if (thread && thread.agentId === agent.id) {
        try {
          const existing = await this.multica.chatSession(thread.id);
          if (existing.status !== "active") thread = null;
        } catch (error) {
          if (error.status === 404) thread = null;
          else throw error;
        }
      } else thread = null;
      if (!thread) {
        const session = await this.multica.createChatSession(agent.id, `如流 · ${user} · ${text.slice(0, 60)}`);
        thread = { id: session.id, agentId: agent.id };
        this.state.threads[key] = thread;
        this.save();
      }
      const staged = await this.stageImages(images);
      const attachmentIds = [];
      let content = text;
      try {
        for (const path of staged.paths) {
          const attachment = await this.multica.uploadChatImage(thread.id, path);
          if (!attachment.id) throw new Error("图片上传未返回附件 ID");
          attachmentIds.push(attachment.id);
        }
        if (staged.failed.length) {
          content += "\n\n图片附件处理失败：\n" + staged.failed.join("\n");
          await this.reply(conversation, user, "部分图片未能读取：\n" + staged.failed.join("\n"));
        }
        const sent = await this.multica.sendChatMessage(thread.id, content, attachmentIds);
        if (!sent.task_id) throw new Error("聊天已提交但缺少执行 ID，请到网页版确认，不要重复发送");
        this.state.pending[sent.task_id] = { sessionId: thread.id, conversation, user, at: Date.now() };
        this.save();
        if (attachmentIds.some((id) => !(sent.attachment_ids || []).includes(id))) {
          await this.reply(conversation, user, "消息已发送，但部分图片未绑定成功，请到网页版检查附件。");
        }
        let reacted = false;
        try { reacted = acknowledge ? await acknowledge() : false; }
        catch (error) { this.logger.warn("receipt failed: " + error.message); }
        if (!reacted) await this.reply(conversation, user, sent.queued ? "消息已加入当前聊天队列。" : "收到，正在回复…");
      } finally { await staged.cleanup(); }
    });
  }

  async status(conversation, user) {
    const thread = this.state.threads[this.key(conversation, user)];
    if (!thread) return false;
    const pending = await this.multica.chatPending(thread.id);
    const messages = await this.multica.chatMessages(thread.id);
    const last = messages.filter((message) => message.role === "assistant").at(-1);
    const text = pending.task_id
      ? `当前聊天：${pending.status}${pending.wait_reason ? "\n" + pending.wait_reason : ""}`
      : "当前聊天没有正在执行的请求。";
    await this.reply(conversation, user, text + (last ? "\n最近回复：\n" + (last.content || last.failure_reason || "无文字回复").slice(0, 2000) : ""));
    return true;
  }

  async poll() {
    if (this.polling || this.stopped) return;
    this.polling = true;
    try {
      for (const [taskId, pending] of Object.entries(this.state.pending)) {
        if (this.stopped) break;
        try {
          const messages = await this.multica.chatMessages(pending.sessionId);
          const answer = messages.find((message) => message.role === "assistant" && message.task_id === taskId);
          if (answer) {
            let content = answer.failure_reason ? "回复失败：" + answer.failure_reason : answer.content || "本轮已完成，没有文字回复。";
            if (answer.attachments?.length) content += "\n\n本轮生成了附件，请在网页版聊天中查看。";
            // Retain pending delivery on a send failure; restart resumes polling.
            await this.reply(pending.conversation, pending.user, content);
            delete this.state.pending[taskId];
            this.save();
          } else if (Date.now() - pending.at > (this.config.pollTimeoutMs || 1800000)) {
            await this.reply(pending.conversation, pending.user, "聊天请求仍未返回，使用 /status 查看状态；请求不会转为新 issue。");
            delete this.state.pending[taskId];
            this.save();
          }
        } catch (error) {
          this.logger.warn("chat reply poll failed: " + error.message);
        }
      }
    } finally { this.polling = false; }
  }
}
