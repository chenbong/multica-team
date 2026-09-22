import { readFileSync, writeFileSync } from "node:fs";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { Multica, TERMINAL_RUN_STATUSES, latestRun } from "./multica.js";
import { ChatRelay, issueRequest } from "./chat.js";

const HELP = [
  "**Multica 机器人**",
  "",
  "普通消息是持续聊天；群里需要 @ 我。只有明确创建任务时才新建 issue。",
  "",
  "- `/agents` 列出可用 agent",
  "- `/agent <名字>` 切换你自己的默认 agent（私聊和群里共用）",
  "- `/issue <内容>` 创建任务，也可说「帮我创建一个任务：内容」",
  "- `/new` 开启新的聊天上下文",
  "- `/status` 查询当前聊天状态（没有聊天时查询最近任务）",
  "- `/status <任务编号>` 查询指定 issue；也可问「CBH-15 进展怎么样？」",
  "- `/help` 这份说明",
].join("\n");

const MAX_INBOUND_IMAGE_BYTES = 20 * 1024 * 1024;
const INBOUND_IMAGE_TIMEOUT_MS = 30_000;

export class Bridge {
  // A double tap in the chat client is filtered by content; InfoFlow redelivering
  // the same message is filtered by id (see #alreadyHandled).
  static REPEAT_WINDOW_MS = 60_000;

  // Enough id history to cover a redelivery burst without growing the state file.
  static SEEN_MESSAGE_LIMIT = 200;

  constructor(config, sender, logger = console, { getImageAccessToken = null } = {}) {
    this.config = config;
    this.sender = sender;
    this.logger = logger;
    this.multica = new Multica(config.multica, logger);
    this.getImageAccessToken = getImageAccessToken;
    this.state = this.#loadState();
    this.chat = new ChatRelay(config, this.multica,
      async (conversation, user, content) => {
        for (let i = 0; i < content.length; i += 3000) {
          await this.sender(conversation, content.slice(i, i + 3000), { atUser: user });
        }
      },
      (images) => stageInboundImages(images, this.logger, this.getImageAccessToken), logger);
  }

  stop() { this.chat.stop(); }

  // Inbound events normally carry the username already; a payload that only has
  // the numeric uid is mapped here so state, allow list, and replies agree.
  resolveUser(rawUser) {
    const key = String(rawUser);
    return this.config.userMap?.[key] ?? key;
  }

  // conversation: { kind: "private" | "group", id }. For a private chat the id is
  // the username to reply to; for a group it is the numeric group id.
  async handleMessage({ conversation, user: rawUser, text, messageId, images = [], acknowledge = null }) {
    const inboundImages = Array.isArray(images) ? images : [];
    const body = String(text ?? "").trim() || (inboundImages.length > 0 ? "（图片消息，详见附件）" : "");
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
      const issueBody = issueRequest(body);
      if (issueBody !== null) {
        if (!issueBody) {
          await this.reply(conversation, user, "请提供任务内容，例如 /issue 优化推理性能。");
          return;
        }
        await this.#dispatch(conversation, user, issueBody, inboundImages);
        return;
      }
      if (body.startsWith("/")) {
        await this.#handleCommand(conversation, user, body);
        return;
      }
      const progress = body.match(/^([A-Za-z][A-Za-z0-9]*-\d+)\s*(?:的)?(?:进度|进展|状态|完成了吗|怎么样了)(?:怎么样|怎么样了|如何|呢)?[？?\s]*$/);
      if (progress) {
        await this.#issueStatus(conversation, user, progress[1]);
        return;
      }
      if (/^(?:刚才|之前|上一个|最近)(?:的)?(?:那个)?任务(?:的)?(?:进度|进展|状态|完成了吗|怎么样了)[？?\s]*$/.test(body)) {
        await this.#issueStatus(conversation, user, "");
        return;
      }
      await this.chat.send(conversation, user, body, inboundImages, this.agentFor(user), acknowledge);
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

    if (command === "/new") {
      await this.chat.reset(conversation, user);
      return;
    }

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
      if (!argument && await this.chat.status(conversation, user)) return;
      await this.#issueStatus(conversation, user, argument);
      return;
    }
    await this.reply(conversation, user, `不认识的命令：${command}。发 \`/help\` 看用法。`);
  }

  async #issueStatus(conversation, user, reference) {
    this.state = this.#loadState();
    const last = this.#thread(conversation, user).lastIssue;
    if (!reference && !last) {
      await this.reply(conversation, user, "你在这个会话里还没有建过任务，可以用 /status <任务编号> 查询。");
      return;
    }
    if (reference && !/^[A-Za-z][A-Za-z0-9]*-\d+$/.test(reference)) {
      await this.reply(conversation, user, "请输入任务编号，例如 /status CBH-15。");
      return;
    }
    // Group status commands expose only the requester's last task in this thread.
    if (conversation.kind === "group" && reference && reference.toUpperCase() !== last?.identifier?.toUpperCase()) {
      await this.reply(conversation, user, "群内仅能查询你在当前会话创建的最近任务，其他任务请在有权限的网页版查看。");
      return;
    }
    const issue = await this.multica.getIssue(reference || last.id);
    const run = latestRun(await this.multica.runs(issue.id));
    await this.reply(conversation, user, `${issue.identifier} ${issue.title}\n执行状态：${run?.status || "尚无执行记录"}\n${this.multica.issueUrl(issue)}`);
  }

  async #dispatch(conversation, user, body, images = []) {
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
    const staged = await stageInboundImages(images, this.logger, this.getImageAccessToken);
    const failureNote = staged.failed.map((entry) => "- " + entry).join("\n");
    const description = staged.failed.length === 0 ? body : body + "\n\n图片附件处理失败：\n" + failureNote;
    let issue;
    try {
      issue = await this.multica.createIssue({
        title: titleFrom(body),
        description,
        assignee,
        attachments: staged.paths,
      });
    } finally {
      await staged.cleanup();
    }
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
    // The review watcher sends the binder a single cross-entry notification.
    // Retain replies to groups/other users and runs that do not enter review.
    if (this.config.reviewNotifications && conversation.kind === "private") {
      try {
        const [current, me] = await Promise.all([this.multica.getIssue(issue.id), this.multica.request("/api/me")]);
        if ((current.status_category || current.status) === "in_review" && me.email?.split("@")[0] === user) return;
      } catch (error) { this.logger.warn("review handoff check failed: " + error.message); }
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
    this.state = this.#loadState();
    const key = this.#conversationKey(conversation);
    const record = this.state.users[user] ?? {};
    const conversations = { ...record.conversations, [key]: { ...record.conversations?.[key], ...patch } };
    this.state.users[user] = { ...record, conversations };
    this.#persist();
  }

  #rememberUser(user, patch) {
    this.state = this.#loadState();
    this.state.users[user] = { ...this.state.users[user], ...patch };
    this.#persist();
  }

  // Message-id de-duplication. InfoFlow redelivers a message a few seconds later
  // often enough that an in-memory set is not enough: a restart in between would
  // let the copy through, so the ids are persisted with the rest of the state.
  #alreadyHandled(messageId) {
    this.state = this.#loadState();
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
export async function stageInboundImages(images, logger = console, getImageAccessToken = null) {
  const list = Array.isArray(images) ? images.filter(Boolean) : [];
  if (list.length === 0) return { paths: [], failed: [], cleanup: async () => {} };

  const directory = await mkdtemp(join(process.cwd(), ".infoflow-images-"));
  const paths = [];
  const failed = [];
  for (let index = 0; index < list.length; index += 1) {
    try {
      paths.push(await downloadInboundImage(list[index], directory, index + 1, getImageAccessToken));
    } catch (error) {
      const detail = error instanceof Error ? error.message : String(error);
      failed.push("第" + (index + 1) + "张图片：" + detail);
      logger.warn("inbound image " + (index + 1) + " download failed: " + detail);
    }
  }

  let cleaned = false;
  return {
    paths,
    failed,
    cleanup: async () => {
      if (cleaned) return;
      cleaned = true;
      await rm(directory, { recursive: true, force: true });
    },
  };
}

async function downloadInboundImage(image, directory, index, getImageAccessToken) {
  const descriptor = image && typeof image === "object" ? image : { content: String(image ?? "") };
  const inlineContent = descriptor.inline && typeof descriptor.content === "string" ? descriptor.content.trim() : "";

  if (inlineContent !== "" && !/^https?:\/\//i.test(inlineContent)) {
    const encoded = inlineContent.replace(/^data:image\/[^;]+;base64,/i, "");
    return writeInboundImage(Buffer.from(encoded, "base64"), directory, index, "");
  }

  const candidates = imageUrlCandidates(descriptor);
  if (candidates.length === 0) throw new Error("消息中没有可下载的图片地址");

  let lastError = null;
  for (const candidate of candidates) {
    try {
      return await fetchInboundImage(candidate, directory, index, getImageAccessToken);
    } catch (error) {
      lastError = error;
    }
  }
  throw lastError ?? new Error("图片下载失败");
}

function imageUrlCandidates(image) {
  const fields = [
    ["downloadurl", false],
    ["downloadUrl", false],
    ["imgDownloadurl", true],
    ["imgDownloadUrl", true],
    ["url", false],
    ["href", false],
  ];
  const candidates = [];
  const seen = new Set();
  for (const [field, needsAuth] of fields) {
    const value = image?.[field];
    if (typeof value !== "string" || !/^https?:\/\//i.test(value) || seen.has(value)) continue;
    seen.add(value);
    candidates.push({ url: value, needsAuth });
  }
  if (typeof image?.content === "string" && /^https?:\/\//i.test(image.content) && !seen.has(image.content)) {
    candidates.push({ url: image.content, needsAuth: false });
  }
  return candidates;
}

async function fetchInboundImage(candidate, directory, index, getImageAccessToken) {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), INBOUND_IMAGE_TIMEOUT_MS);
  try {
    const headers = {};
    if (candidate.needsAuth && getImageAccessToken) {
      const token = await getImageAccessToken();
      if (token) headers.Authorization = "Bearer-" + token;
    }
    const response = await fetch(candidate.url, { headers, redirect: "follow", signal: controller.signal });
    if (!response.ok) throw new Error("HTTP " + response.status);

    const advertisedSize = Number(response.headers.get("content-length"));
    if (Number.isFinite(advertisedSize) && advertisedSize > MAX_INBOUND_IMAGE_BYTES) {
      throw new Error("图片超过 20MB 限制");
    }

    const bytes = Buffer.from(await response.arrayBuffer());
    if (bytes.length === 0) throw new Error("返回空内容");
    const contentType = response.headers.get("content-type") || "";
    const prefix = bytes.subarray(0, 128).toString("utf8").trimStart().toLowerCase();
    if (
      contentType.toLowerCase().includes("json") ||
      contentType.toLowerCase().includes("text/html") ||
      prefix.startsWith("{") ||
      prefix.startsWith("[") ||
      prefix.startsWith("<!doctype") ||
      prefix.startsWith("<html")
    ) {
      throw new Error("下载接口返回的不是图片数据");
    }
    return writeInboundImage(bytes, directory, index, contentType);
  } catch (error) {
    if (error?.name === "AbortError") throw new Error("下载超时");
    throw error instanceof Error ? error : new Error(String(error));
  } finally {
    clearTimeout(timeout);
  }
}

async function writeInboundImage(bytes, directory, index, contentType) {
  if (!Buffer.isBuffer(bytes) || bytes.length === 0) throw new Error("图片内容为空");
  if (bytes.length > MAX_INBOUND_IMAGE_BYTES) throw new Error("图片超过 20MB 限制");
  const extension = imageExtension(bytes, contentType);
  const filePath = join(directory, "infoflow-image-" + String(index).padStart(2, "0") + extension);
  await writeFile(filePath, bytes, { mode: 0o600 });
  return filePath;
}

function imageExtension(bytes, contentType) {
  if (bytes.length >= 8 && bytes.subarray(0, 8).equals(Buffer.from("89504e470d0a1a0a", "hex"))) return ".png";
  if (bytes.length >= 3 && bytes.subarray(0, 3).equals(Buffer.from("ffd8ff", "hex"))) return ".jpg";
  if (bytes.subarray(0, 4).toString("ascii") === "GIF8") return ".gif";
  if (bytes.length >= 12 && bytes.subarray(0, 4).toString("ascii") === "RIFF" && bytes.subarray(8, 12).toString("ascii") === "WEBP") return ".webp";
  if (bytes.subarray(0, 2).toString("ascii") === "BM") return ".bmp";
  const mime = String(contentType).split(";", 1)[0].trim().toLowerCase();
  return {
    "image/png": ".png",
    "image/jpeg": ".jpg",
    "image/gif": ".gif",
    "image/webp": ".webp",
    "image/bmp": ".bmp",
    "image/svg+xml": ".svg",
  }[mime] ?? ".bin";
}
