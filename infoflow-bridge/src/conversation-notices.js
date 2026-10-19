import { createHash } from "node:crypto";
import { exactGroupId, groupUsername, validatedRoster } from "./issue-groups.js";
import { messageIdString } from "./conversation-context.js";
import { replyUrl, withReplyMode, taskReplyReference } from "./reply-format.js";

function noticeContent(notice, scope, config, includeTitle = true) {
  if (!notice || typeof notice !== "object" ||
      typeof notice.id !== "string" || !/^[A-Za-z0-9_-]{1,128}$/.test(notice.id) ||
      !["bound", "unbound"].includes(notice.kind) ||
      notice.robot_id !== scope.robot_id || notice.agent_id !== scope.agent_id ||
      typeof notice.issue_id !== "string" || !notice.issue_id ||
      typeof notice.workspace_slug !== "string" || !notice.workspace_slug ||
      !["private", "group"].includes(notice.conversation?.kind) ||
      typeof notice.conversation.id !== "string") throw new Error("invalid conversation notice");
  const { kind, id } = notice.conversation;
  if (kind === "group") {
    exactGroupId(id);
    if (notice.group_id !== undefined && notice.group_id !== id) throw new Error("notice group mismatch");
  } else if (groupUsername(id) !== id) throw new Error("invalid notice recipient");
  // Validate the identifier using the same rule as normal task reply headers.
  if (!notice.identifier) throw new Error("notice task identifier is missing");
  withReplyMode("", "issue", notice.identifier);
  const url = replyUrl(config, { binding: notice }).replace(/[()]/g, char => char === "(" ? "%28" : "%29");
  const bare = `**[任务模式] ${notice.identifier} 已${notice.kind === "bound" ? "绑定" : "解绑"}本对话**\n\n[任务链接](${url})`;
  const reference = taskReplyReference(notice.identifier, includeTitle ? notice.title : null,
    { maxTitleBytes: 1800 - Buffer.byteLength(bare) - 1 });
  const text = `**[任务模式] ${reference} 已${notice.kind === "bound" ? "绑定" : "解绑"}本对话**\n\n[任务链接](${url})`;
  if (Buffer.byteLength(text) > 1800) throw new Error("conversation notice exceeds byte budget");
  return text;
}

export async function pollConversationNotices(relay, scope) {
  if (relay.stopped) return;
  const result = await relay.request("conversation-notices", { ...scope, limit: 20 });
  if (!Array.isArray(result.notices) || result.notices.length > 20) throw new Error("invalid conversation notice list");
  for (const notice of result.notices) {
    if (relay.stopped) return;
    try {
      // Preserve the old checkpoint digest. Adding/renaming a title must not
      // replay an accepted notice or prevent its pending ACK after deployment.
      const canonical = noticeContent(notice, scope, relay.config.multica, false);
      const digest = createHash("sha256").update(JSON.stringify([notice.kind, notice.issue_id, notice.conversation, canonical])).digest("hex");
      const prior = relay.sentNotices[notice.id];
      if (prior && (prior.digest !== digest || ![0, 1].includes(prior.next))) throw new Error("conversation notice checkpoint mismatch");
      if (prior?.acked) continue;
      const content = prior?.content || noticeContent(notice, scope, relay.config.multica);
      if (!prior || prior.next === 0) {
        if (notice.conversation.kind === "group") {
          if (!relay.groups) throw new Error("notice group membership unavailable");
          const entries = await relay.groups.entries(scope.workspace_id, [scope.agent_id], { includeRobotId: scope.robot_id });
          const own = entries.filter(entry => entry.robotId === scope.robot_id && entry.agentId === scope.agent_id);
          if (own.length !== 1 || !own[0].client) throw new Error("notice caller unavailable");
          if (relay.stopped) return;
          const roster = validatedRoster(await relay.groups.roster(own[0].client, notice.conversation.id));
          if (!roster.bots.some(bot => bot.appId === exactGroupId(own[0].appId))) throw new Error("notice caller is not in group");
        }
        if (relay.stopped) return;
        // The poll snapshot may become revoked while SDK membership is read.
        // Recheck API authority only for unsent notices, never for ACK retries.
        const checked = await relay.request(`conversation-notices/${encodeURIComponent(notice.id)}/check`, scope);
        if (typeof checked.allowed !== "boolean") throw new Error("invalid conversation notice authority");
        if (relay.stopped) return;
        if (!checked.allowed) continue;
        relay.sentNotices[notice.id] = { digest, next: 0, messageIds: [], content };
        relay.saveNotices();
        const receipt = await relay.send(notice.conversation, content, {});
        const messageId = messageIdString(receipt?.messageid ?? receipt?.messageId ??
          (notice.conversation.kind === "private" ? receipt?.msgkey : null));
        relay.sentNotices[notice.id] = { digest, next: 1, messageIds: messageId ? [messageId] : [], content };
      }
      // Re-save even an in-memory success before ACK: a failed disk write must
      // not complete the server outbox without a durable send checkpoint.
      relay.saveNotices();
      if (relay.stopped) return;
      const ids = relay.sentNotices[notice.id].messageIds;
      const ack = await relay.request(`conversation-notices/${encodeURIComponent(notice.id)}/ack`, {
        ...scope, ...(ids.length ? { message_id: ids.at(-1), message_ids: ids } : {}),
      });
      if (ack.handled !== true || ack.silent !== true) throw new Error("invalid conversation notice acknowledgement");
      relay.sentNotices[notice.id].acked = true;
      relay.saveNotices();
    } catch { if (!relay.stopped) relay.logger.warn("conversation notice pending"); }
  }
}
