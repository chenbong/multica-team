// Group awareness for the robot: what the agent is told about the group a message
// came from, and which people a reply may @. Kept free of the SDK so it can be
// tested without credentials.

const INFO_TTL_MS = 10 * 60_000;
// A failed lookup (permission not granted yet, robot removed) is retried after a
// short pause instead of on every message, so the 10 QPS limit is never at risk.
const FAILURE_TTL_MS = 60_000;
// Above this size the member list costs more context than it is worth; the agent
// still gets the count and can @ anyone it already knows the userid of.
const MAX_LISTED_MEMBERS = 100;
// groupmsgsend accepts at most 50 people in one AT block.
const MAX_AT_USERS = 50;

export function parseGroupBlocks(blocks) {
  const images = [];
  const parts = [];
  const mentions = [];

  for (const block of Array.isArray(blocks) ? blocks : []) {
    const type = String(block?.type ?? "").toUpperCase();

    if (type === "IMAGE") {
      images.push(block);
      continue;
    }

    if (type === "REPLYDATA" || type === "REPLY" || type === "QUOTE") {
      if (Array.isArray(block.replyImages)) images.push(...block.replyImages.filter(Boolean));
    }

    if (type === "AT") {
      // The robot's own AT block carries robotid; anything else names people.
      if (block.robotid === undefined) mentions.push(...atUserIds(block));
      continue;
    }

    if (type === "LINK") {
      parts.push(block.label ?? block.url ?? block.href ?? "");
      continue;
    }

    parts.push(block.content ?? block.text ?? "");
  }

  return {
    text: parts.filter(Boolean).join(" ").trim(),
    images,
    mentions: unique(mentions),
  };
}

// The documentation only shows the outbound shape (atuserids) and the robot's own
// inbound block, not how an inbound mention of a person is spelled, so the likely
// field names are all accepted until a real message confirms which one is used.
function atUserIds(block) {
  const ids = [];
  if (Array.isArray(block.atuserids)) ids.push(...block.atuserids);
  if (Array.isArray(block.userids)) ids.push(...block.userids);
  for (const field of ["userid", "userId", "atuserid"]) {
    if (block[field] !== undefined) ids.push(block[field]);
  }
  return ids.map((id) => String(id ?? "").trim()).filter(Boolean);
}

// Caches group info and member lists per group. `api` is the SDK's
// client.im.group (getGroupInfo / getMembers), or a stand-in with the same shape.
export class GroupDirectory {
  constructor(api, logger = console, now = () => Date.now()) {
    this.api = api;
    this.logger = logger;
    this.now = now;
    this.cache = new Map();
  }

  info(groupId) {
    return this.#cached("info", groupId, () => this.api.getGroupInfo(groupId));
  }

  // Human members' userids only; robots cannot be @-ed through atuserids.
  members(groupId) {
    return this.#cached("members", groupId, async () => {
      const list = await this.api.getMembers(groupId);
      return unique((Array.isArray(list) ? list : [])
        .filter((member) => member?.type !== "agent")
        .map((member) => String(member?.userId ?? "").trim())
        .filter(Boolean));
    });
  }

  async #cached(kind, groupId, load) {
    const key = kind + ":" + groupId;
    const hit = this.cache.get(key);
    if (hit && hit.expires > this.now()) return hit.value;
    let value = null;
    let ttl = INFO_TTL_MS;
    try {
      value = (await load()) ?? null;
    } catch (error) {
      ttl = FAILURE_TTL_MS;
      this.logger.warn(`group ${kind} lookup for ${groupId} failed: ${error.message}`);
    }
    this.cache.set(key, { value, expires: this.now() + ttl });
    return value;
  }
}

// What the agent sees ahead of a forwarded group message. `members` is null when
// it is unchanged since the last message in this chat and need not be repeated.
export function formatGroupContext({ groupId, info, sender, mentions = [], members = null, sendCommand = "" }) {
  const name = String(info?.name ?? "").trim();
  const lines = [
    `群聊：${name ? name + "（ID " + groupId + "）" : "ID " + groupId}`,
    `发送人：${sender}`,
  ];
  if (mentions.length > 0) lines.push(`消息中@了：${mentions.join("、")}`);
  if (info?.masterUserId) lines.push(`群主：${info.masterUserId}`);
  if (Array.isArray(members)) {
    const count = Number(info?.memberCount) || members.length;
    lines.push(
      members.length <= MAX_LISTED_MEMBERS
        ? `群成员（userid）：${members.join("、")}`
        : `群成员共 ${count} 人，名单较长未列出`,
    );
    lines.push("回复中写 @userid 即可@对应群成员。");
    // Sent with the member list, i.e. once per chat session and when it changes.
    if (sendCommand) {
      lines.push(`需要主动或定时往本群发消息时，执行：${sendCommand} --group ${groupId} --at <userid,...> --text "<内容>" [--when "HH:MM" 或 "YYYY-MM-DD HH:MM"]`);
    }
  }
  return "[如流群聊上下文]\n" + lines.join("\n") + "\n[/如流群聊上下文]";
}

// The userids a reply @-mentions, limited to people actually in the group so
// strings like "@types/node" or an e-mail address never turn into a mention.
export function replyMentions(content, members) {
  if (!Array.isArray(members) || members.length === 0) return [];
  const known = new Map(members.map((id) => [id.toLowerCase(), id]));
  const found = [];
  for (const match of String(content).matchAll(/(?<![\w.@])@([A-Za-z0-9_.-]+)/g)) {
    const id = known.get(match[1].replace(/[.-]+$/, "").toLowerCase());
    if (id) found.push(id);
  }
  return unique(found);
}

export function atUsers(atUser, content, members) {
  return unique([...(atUser ? [String(atUser)] : []), ...replyMentions(content, members)]).slice(0, MAX_AT_USERS);
}

function unique(values) {
  return [...new Set(values)];
}
