import { readFileSync, writeFileSync, renameSync } from "node:fs";

let lastClientMessageId = 0;
function clientMessageId() {
  // Date.now alone collides between robots/concurrent notices in one tick.
  lastClientMessageId = Math.max(Date.now() * 1000, lastClientMessageId + 1);
  return lastClientMessageId;
}

// The pinned SDK's convenience methods parseInt group IDs and default malformed
// member lists to []. Use its authenticated transport, but retain the original
// numeric JSON token and validate the official API envelope ourselves.
export function exactGroupId(value) {
  if (typeof value === "number" && !Number.isSafeInteger(value)) throw new Error("unsafe group identifier");
  const id = String(value ?? "");
  if (!/^[1-9][0-9]{0,18}$/.test(id) || BigInt(id) > 9223372036854775807n) throw new Error("invalid group identifier");
  return id;
}

function apiEnvelope(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("invalid group API response");
  if (value.code !== undefined && value.code !== "ok" && value.code !== 0) throw new Error("group API rejected request");
  const outerCode = value.errcode;
  if (outerCode !== undefined && ![0, 201].includes(outerCode)) throw new Error("group API rejected request");
  const nested = value.data;
  if (nested?.errcode !== undefined && ![0, 201].includes(nested.errcode)) throw new Error("group API rejected request");
  const code = outerCode === 201 || nested?.errcode === 201 ? 201 :
    nested?.errcode ?? outerCode ?? (value.code === "ok" || value.code === 0 ? 0 : undefined);
  if (code === undefined) throw new Error("invalid group API result code");
  return { code, data: nested?.data ?? nested };
}

export async function groupMembers(client, groupId) {
  const id = exactGroupId(groupId);
  const result = apiEnvelope(await client.httpClient.post("/robot/group/memberList", `{"groupId":${id},"recallType":0}`, { disableRetry: true }));
  if (result.code !== 0 || !Array.isArray(result.data?.agentInfoList) || !Array.isArray(result.data?.userInfoList)) {
    throw new Error("group membership could not be verified");
  }
  return result.data.agentInfoList.map(agent => ({
    // agentId is the InfoFlow application ID. imId is a DIFFERENT identifier.
    appId: exactGroupId(agent?.agentId),
    imId: agent?.imId == null ? null : exactGroupId(agent.imId),
  }));
}

export async function inviteGroupLeader(client, groupId, appId) {
  const id = exactGroupId(groupId), target = exactGroupId(appId);
  const result = apiEnvelope(await client.httpClient.post("/robot/group/addMembers",
    `{"groupId":${id},"agentIdList":${JSON.stringify(target)}}`, { disableRetry: true }));
  if (result.code === 201) return "approval_pending";
  if (!Array.isArray(result.data?.failAgentIds)) throw new Error("invalid group invitation result");
  return result.data.failAgentIds.some(value => exactGroupId(value) === target) ? "invitation_failed" : "membership_unconfirmed";
}

export async function sendGroupExact(client, conversation, content, options = {}) {
  const id = exactGroupId(conversation.id);
  const body = [];
  if (options.atUser) body.push({ type: "AT", atall: false, atuserids: [options.atUser] });
  body.push({ type: "MD", content });
  const header = `{"toid":${id},"totype":"GROUP","msgtype":"MD","clientmsgid":${clientMessageId()},"role":"robot"}`;
  const result = apiEnvelope(await client.httpClient.post("/robot/msg/groupmsgsend",
    `{"message":{"header":${header},"body":${JSON.stringify(body)}}}`, { disableRetry: true }));
  if (result.code !== 0) throw new Error("group message was not accepted");
  return result.data;
}

export function botOrigin(raw) {
  const header = raw?.message?.header ?? {};
  return [header.role, header.fromtype, raw?.role, raw?.FromUserType, raw?.senderType]
    .some(value => ["robot", "bot", "agent"].includes(String(value ?? "").toLowerCase())) ||
    [header.fromrobotid, header.fromagentid, raw?.fromrobotid, raw?.fromagentid].some(value => value != null && String(value) !== "0");
}

// A single process-wide coordinator is shared by every robot. Configuration
// mappings include offline robots: transport failure is NOT evidence of absence.
export class IssueGroups {
  constructor({ entries, statePath, now = Date.now, members = groupMembers, invite = inviteGroupLeader }) {
    this.entries = entries;
    this.now = now;
    this.members = members;
    this.invite = invite;
    this.path = statePath + ".issue-group-invites.json";
    this.deliveryPath = statePath + ".issue-group-deliveries.json";
    this.attempts = {};
    this.deliveries = {};
    this.locks = new Map();
    this.deliveryLocks = new Map();
    this.robotSenders = new Set();
    try { this.attempts = JSON.parse(readFileSync(this.path, "utf8")); }
    catch (error) { if (error.code !== "ENOENT") throw error; }
    try { this.deliveries = JSON.parse(readFileSync(this.deliveryPath, "utf8")); }
    catch (error) { if (error.code !== "ENOENT") throw error; }
  }

  isBot(raw, user) { return botOrigin(raw) || this.robotSenders.has(String(user)); }

  async owns(scope, binding) {
    const leaderId = binding?.group?.leader_agent_id;
    if (!leaderId || scope.agent_id !== leaderId) return false;
    const entries = await this.entries(scope.workspace_id, [leaderId], { agentNames: binding.group.agent_names });
    const leaders = entries.filter(entry => entry.agentId === leaderId);
    return leaders.length === 1 && leaders[0].robotId === scope.robot_id;
  }

  async inspect(scope, conversation, binding, { invite = false, bootstrap = false } = {}) {
    const key = JSON.stringify([scope.workspace_id, exactGroupId(conversation.id)]);
    const prior = this.locks.get(key) || Promise.resolve();
    const pending = prior.catch(() => {}).then(() => this.inspectSerial(scope, conversation, binding, invite, bootstrap));
    this.locks.set(key, pending);
    try { return await pending; }
    finally { if (this.locks.get(key) === pending) this.locks.delete(key); }
  }

  async inspectSerial(scope, conversation, binding, invite, bootstrap) {
    const group = binding?.group;
    if (!group?.leader_agent_id || !Array.isArray(group.agent_ids) || !group.agent_ids.includes(group.leader_agent_id)) {
      return { leader_present: false, reason: "leader_configuration_missing", leader_robot_id: "" };
    }
    const entries = await this.entries(scope.workspace_id, group.agent_ids, {
      agentNames: group.agent_names, includeRobotId: bootstrap ? scope.robot_id : null,
    });
    const candidates = entries.filter(entry => entry.agentId === group.leader_agent_id);
    if (candidates.length !== 1) return { leader_present: false, reason: "leader_robot_missing_or_ambiguous", leader_robot_id: "" };
    const leader = candidates[0];
    const result = { leader_present: false, leader_robot_id: leader.robotId, leader_agent_id: group.leader_agent_id };
    if (new Set(entries.map(entry => String(entry.appId))).size !== entries.length) return { ...result, reason: "leader_robot_missing_or_ambiguous" };
    if (!leader.client || !leader.send) return { ...result, reason: "leader_robot_unavailable" };
    // Background refresh always uses the leader, never the unrelated robot
    // whose workspace timer fired. Only an admitted bind may use its inviter.
    let caller = leader;
    try {
      let members;
      try { members = await this.members(leader.client, conversation.id); }
      catch {
        caller = bootstrap ? entries.find(entry => entry.robotId === scope.robot_id && entry.client) : null;
        if (!caller || caller === leader) throw new Error("leader member list unavailable");
        members = await this.members(caller.client, conversation.id);
      }
      if (bootstrap && !members.some(member => member.appId === String(leader.appId))) {
        caller = entries.find(entry => entry.robotId === scope.robot_id && entry.client) || leader;
      }
      this.rememberBots(members);
      if (!members.some(member => member.appId === String(leader.appId)) && invite) {
        const reason = await this.maybeInvite(caller, leader, conversation, binding);
        // An accepted invitation can mean admin approval, partial failure, or
        // delayed membership. Only a fresh member list may activate the binding.
        members = await this.members(caller.client, conversation.id);
        this.rememberBots(members);
        if (!members.some(member => member.appId === String(leader.appId))) return { ...result, reason };
      }
      if (!members.some(member => member.appId === String(leader.appId))) return { ...result, reason: "leader_not_in_group" };
      return { ...result, leader_present: true, leader, entries, members };
    } catch {
      // Do not expose SDK errors, credentials or request bodies in notices.
      return { ...result, reason: "membership_check_failed" };
    }
  }

  rememberBots(members) {
    for (const member of members) if (member.imId) this.robotSenders.add(member.imId);
  }

  async maybeInvite(caller, leader, conversation, binding) {
    const key = JSON.stringify([binding.binding_id, binding.generation, conversation.id, leader.robotId]);
    const prior = this.attempts[key];
    if (prior && (prior.count >= 3 || prior.next > this.now())) return prior.reason;
    const count = (prior?.count || 0) + 1;
    // Persist BEFORE the external mutation so crashes/timeouts never immediately
    // resend an invitation. Only a new binding generation resets the budget.
    this.attempts[key] = { count, next: this.now() + 600000 * 2 ** (count - 1), reason: "invitation_pending" };
    this.save();
    try { this.attempts[key].reason = await this.invite(caller.client, conversation.id, leader.appId); }
    catch { this.attempts[key].reason = "invitation_failed"; }
    this.save();
    return this.attempts[key].reason;
  }

  save() {
    writeFileSync(this.path + ".tmp", JSON.stringify(this.attempts), { mode: 0o600 });
    renameSync(this.path + ".tmp", this.path);
  }

  saveDeliveries() {
    writeFileSync(this.deliveryPath + ".tmp", JSON.stringify(this.deliveries), { mode: 0o600 });
    renameSync(this.deliveryPath + ".tmp", this.deliveryPath);
  }

  async deliveryLock(id, action) {
    const prior = this.deliveryLocks.get(id) || Promise.resolve();
    const pending = prior.catch(() => {}).then(action);
    this.deliveryLocks.set(id, pending);
    try { return await pending; }
    finally { if (this.deliveryLocks.get(id) === pending) this.deliveryLocks.delete(id); }
  }

  selectSender(observation, item, pinned = null) {
    if (!observation.leader_present) throw new Error("leader membership is not confirmed");
    const group = item.binding?.group;
    if (!group?.agent_ids?.includes(item.author_agent_id)) throw new Error("delivery author is outside the assigned team");
    let sender, delegated = false;
    if (pinned) {
      sender = observation.entries.find(entry => entry.robotId === pinned.robot_id && entry.agentId === pinned.agent_id);
      if (!sender || !sender.send || !sender.client || !observation.members.some(member => member.appId === String(sender.appId))) {
        throw new Error("pinned sender is unavailable; routing must not change during retry");
      }
      delegated = pinned.delegated;
      if (delegated && sender.robotId !== observation.leader.robotId) throw new Error("pinned leader changed");
    } else {
      const candidates = observation.entries.filter(entry => entry.agentId === item.author_agent_id);
      if (candidates.length > 1) throw new Error("delivery author robot mapping is ambiguous");
      const member = candidates[0];
      if (member && observation.members.some(entry => entry.appId === String(member.appId))) {
        if (!member.client || !member.send) throw new Error("member robot is in group but unavailable");
        sender = member;
      } else {
        sender = observation.leader;
        delegated = item.author_agent_id !== group.leader_agent_id;
      }
    }
    const author = Array.from(String(item.author_name || item.author_agent_id).replace(/[\r\n]/g, " ")).slice(0, 120).join("");
    const label = pinned?.label ?? (delegated ? `【${author} · 队长代发】\n` : "");
    return { send: sender.send, pin: { robot_id: sender.robotId, agent_id: sender.agentId, delegated, label } };
  }
}
