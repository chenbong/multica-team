import { readFileSync, writeFileSync } from "node:fs";
import { Multica, TERMINAL_RUN_STATUSES, latestRun } from "./multica.js";

const HELP = [
  "**Multica 机器人**",
  "",
  "发一句话 = 新建任务并派给 agent，跑完把结果发回这里；群里需要 @ 我。",
  "",
  "- `/agents` 列出可用 agent",
  "- `/agent <名字>` 切换你自己的默认 agent（私聊和群里共用）",
  "- `/status` 看本会话里你最近一个任务的状态",
  "- `/help` 这份说明",
].join("\n");

export class Bridge {
  // A double tap in the chat client is filtered by content; InfoFlow redelivering
  // the same message is filtered by id (see #alreadyHandled).
  static REPEAT_WINDOW_MS = 60_000;

  // Enough id history to cover a redelivery burst without growing the state file.
  static SEEN_MESSAGE_LIMIT = 200;

  constructor(config, sender, logger = console) {
    this.config = config;
    this.sender = sender;
    this.logger = logger;
    this.multica = new Multica(config.multica, logger);
    this.state = this.#loadState();
  }

  // Inbound events normally carry the username already; a payload that only has
  // the numeric uid is mapped here so state, allow list, and replies agree.
  resolveUser(rawUser) {
    const key = String(rawUser);
    return this.config.userMap?.[key] ?? key;
  }

  // conversation: { kind: "private" | "group", id }. For a private chat the id is
  // the username to reply to; for a group it is the numeric group id.
  async handleMessage({ conversation, user: rawUser, text, messageId }) {
    const body = String(text ?? "").trim();
    if (!conversation?.id || !rawUser || body === "") return;
    if (messageId && this.#alreadyHandled(messageId)) {
      this.logger.warn(`ignoring redelivered InfoFlow message ${messageId} from ${rawUser}`);
      return;
    }

    const user = this.resolveUser(rawUser);
    if (!this.#allowed(user, conversation)) {
      this.logger.warn(`rejected ${conversation.kind} message from ${user}: not in allowUsers`);
      if (conversation.kind === "private") {
        await this.reply(conversation, user, "你还没有被授权使用这个机器人，请联系它的部署者把你加入允许名单。");
      }
      return;
    }

    try {
      if (body.startsWith("/")) {
        await this.#handleCommand(conversation, user, body);
        return;
      }
      await this.#dispatch(conversation, user, body);
    } catch (error) {
      this.logger.error(`handling message from ${user} failed: ${error.message}`);
      await this.reply(conversation, user, `出错了：${error.message}`);
    }
  }

  async reply(conversation, user, content) {
    try {
      await this.sender(conversation, content, { atUser: user });
    } catch (error) {
      this.logger.error(`sending to ${conversation.kind}:${conversation.id} failed: ${error.message}`);
    }
  }

  agentFor(user) {
    return this.state.users[user]?.agent ?? this.config.multica.defaultAgent;
  }

  // Groups are opened up through one explicit setting: with allowAllInGroups every
  // member of a group the robot is in can dispatch work, which also means they can
  // start local agent runs on this machine.
  #allowed(user, conversation) {
    if (conversation.kind === "group" && this.config.allowAllInGroups) return true;
    return this.config.allowUsers.some((name) => name.toLowerCase() === String(user).toLowerCase());
  }

  async #handleCommand(conversation, user, body) {
    const [command, ...rest] = body.split(/\s+/);
    const argument = rest.join(" ").trim();

    if (command === "/help") {
      await this.reply(conversation, user, HELP);
      return;
    }
    if (command === "/agents") {
      const agents = await this.multica.listAgents();
      const current = this.agentFor(user);
      const lines = agents.map(
        (agent) => `- ${agent.name === current ? "**" + agent.name + "**（当前）" : agent.name}　${agent.model ?? ""}`,
      );
      await this.reply(conversation, user, lines.length > 0 ? lines.join("\n") : "这个工作区还没有 agent。");
      return;
    }
    if (command === "/agent") {
      if (argument === "") {
        await this.reply(conversation, user, `你当前的 agent：${this.agentFor(user)}。用 \`/agent <名字>\` 切换。`);
        return;
      }
      const agents = await this.multica.listAgents();
      const match =
        agents.find((agent) => agent.name === argument) ??
        agents.find((agent) => agent.name.toLowerCase() === argument.toLowerCase());
      if (!match) {
        await this.reply(conversation, user, `没找到叫「${argument}」的 agent。用 \`/agents\` 看看有哪些。`);
        return;
      }
      this.#rememberUser(user, { agent: match.name });
      await this.reply(conversation, user, `好，之后你的任务派给「${match.name}」。`);
      return;
    }
    if (command === "/status") {
      const last = this.#thread(conversation, user).lastIssue;
      if (!last) {
        await this.reply(conversation, user, "你在这个会话里还没有建过任务。");
        return;
      }
      const run = latestRun(await this.multica.runs(last.id));
      await this.reply(conversation, user, `${last.identifier}　${run ? run.status : "还没有执行记录"}\n${last.url}`);
      return;
    }
    await this.reply(conversation, user, `不认识的命令：${command}。发 \`/help\` 看用法。`);
  }

  async #dispatch(conversation, user, body) {
    const repeat = this.#thread(conversation, user).lastRequest;
    if (repeat && repeat.body === body && Date.now() - repeat.at < Bridge.REPEAT_WINDOW_MS) {
      await this.reply(
        conversation,
        user,
        `同样的内容一分钟内已经派过任务 **${repeat.identifier}**，这次没有重复派单。确实想再跑一遍就稍等一会儿再发，或者换个说法。\n${repeat.url}`,
      );
      return;
    }

    const assignee = this.agentFor(user);
    const issue = await this.multica.createIssue({ title: titleFrom(body), description: body, assignee });
    const url = this.multica.issueUrl(issue);
    this.#remember(conversation, user, {
      lastIssue: { id: issue.id, identifier: issue.identifier, url },
      lastRequest: { body, identifier: issue.identifier, url, at: Date.now() },
    });
    await this.reply(conversation, user, `已建任务 **${issue.identifier}**，派给「${assignee}」，跑完发回这里。\n${url}`);

    const outcome = await this.#waitForRun(issue.id);
    if (outcome.kind === "timeout") {
      await this.reply(
        conversation,
        user,
        `${issue.identifier} 还在跑（已经 ${Math.round(this.config.pollTimeoutMs / 60000)} 分钟），先不等了，用 \`/status\` 查进度。\n${url}`,
      );
      return;
    }
    if (outcome.kind === "failed") {
      await this.reply(conversation, user, `${issue.identifier} 执行失败：${outcome.detail}\n${url}`);
      return;
    }
    await this.reply(conversation, user, `**${issue.identifier}** 完成\n\n${outcome.detail}\n\n${url}`);
  }

  // Polls until the newest run reaches a terminal status. The daemon reports the
  // answer twice — as run.result.output and as an issue comment — and the comment
  // is preferred because it keeps the formatting the agent chose.
  async #waitForRun(issueId) {
    const deadline = Date.now() + this.config.pollTimeoutMs;
    while (Date.now() < deadline) {
      await sleep(this.config.pollIntervalMs);
      const run = latestRun(await this.multica.runs(issueId));
      if (!run || !TERMINAL_RUN_STATUSES.has(run.status)) continue;

      if (run.status === "completed") {
        const comments = await this.multica.comments(issueId);
        const fromRun = comments
          .filter((comment) => comment.author_type === "agent" && comment.source_task_id === run.id)
          .map((comment) => comment.content)
          .filter(Boolean);
        const detail = fromRun.length > 0 ? fromRun.join("\n\n") : (run.result?.output ?? "（agent 没有留下文字结果）");
        return { kind: "completed", detail: truncate(detail, 3500) };
      }
      return { kind: "failed", detail: truncate(run.error ?? run.result?.output ?? run.status, 1500) };
    }
    return { kind: "timeout" };
  }

  // Per-conversation state so a group thread and a direct chat do not overwrite
  // each other last issue, while the agent preference stays per person.
  #conversationKey(conversation) {
    // The robot id is part of the key so two robots bound to different agents keep
    // separate threads even inside the same group.
    return `${this.config.robotId ?? "default"}:${conversation.kind}:${conversation.id}`;
  }

  #thread(conversation, user) {
    return this.state.users[user]?.conversations?.[this.#conversationKey(conversation)] ?? {};
  }

  #remember(conversation, user, patch) {
    const key = this.#conversationKey(conversation);
    const record = this.state.users[user] ?? {};
    const conversations = { ...record.conversations, [key]: { ...record.conversations?.[key], ...patch } };
    this.state.users[user] = { ...record, conversations };
    this.#persist();
  }

  #rememberUser(user, patch) {
    this.state.users[user] = { ...this.state.users[user], ...patch };
    this.#persist();
  }

  // Message-id de-duplication. InfoFlow redelivers a message a few seconds later
  // often enough that an in-memory set is not enough: a restart in between would
  // let the copy through, so the ids are persisted with the rest of the state.
  #alreadyHandled(messageId) {
    const id = String(messageId);
    if (this.state.seenMessageIds.includes(id)) return true;
    this.state.seenMessageIds.push(id);
    if (this.state.seenMessageIds.length > Bridge.SEEN_MESSAGE_LIMIT) {
      this.state.seenMessageIds = this.state.seenMessageIds.slice(-Bridge.SEEN_MESSAGE_LIMIT);
    }
    this.#persist();
    return false;
  }

  #persist() {
    try {
      writeFileSync(this.config.statePath, JSON.stringify(this.state, null, 2) + "\n", { mode: 0o600 });
    } catch (error) {
      this.logger.warn(`could not persist state: ${error.message}`);
    }
  }

  #loadState() {
    try {
      const parsed = JSON.parse(readFileSync(this.config.statePath, "utf8"));
      return { users: parsed.users ?? {}, seenMessageIds: parsed.seenMessageIds ?? [] };
    } catch {
      return { users: {}, seenMessageIds: [] };
    }
  }
}

function titleFrom(body) {
  const firstLine = body.split(/\r?\n/, 1)[0].trim();
  const title = firstLine === "" ? body.trim() : firstLine;
  return truncate(title.replace(/\s+/g, " "), 80);
}

function truncate(text, limit) {
  const value = String(text);
  return value.length <= limit ? value : `${value.slice(0, limit)}…（内容已截断）`;
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
