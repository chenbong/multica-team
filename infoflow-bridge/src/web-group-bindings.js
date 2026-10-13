import { createHash } from "node:crypto";
import { exactGroupId, groupUsername, validatedRoster } from "./issue-groups.js";

const UUID = /^[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}$/i;
const REASONS = new Set(["approval_pending", "invitation_failed", "invitation_pending", "membership_unconfirmed"]);
export const MAX_WEB_GROUP_CANDIDATES = 100;
const MAX_CONFIGURED_CANDIDATES = 1000;

function uuid(value) {
  if (typeof value !== "string" || !UUID.test(value)) throw new Error("invalid web binding identifier");
  return value;
}

export function validateWebGroupJob(job) {
  const group = job?.binding?.group;
  uuid(job?.request_id); uuid(job?.generation); uuid(job?.binding?.issue_id); uuid(job?.lease_token);
  if (typeof job.group_id !== "string") throw new Error("web group identifier must be an exact string");
  exactGroupId(job.group_id);
  if (job.binding.binding_id !== job.request_id || job.binding.generation !== job.generation ||
      typeof job.sender !== "string" || groupUsername(job.sender) !== job.sender ||
      group?.state !== "pending" || !Array.isArray(group.agent_ids) || !group.agent_ids.length ||
      group.agent_ids.length > 100 || new Set(group.agent_ids).size !== group.agent_ids.length ||
      !group.agent_ids.includes(group.leader_agent_id) || !Array.isArray(job.inviter_robot_ids) ||
      job.inviter_robot_ids.length > 100 ||
      new Set(job.inviter_robot_ids).size !== job.inviter_robot_ids.length) {
    throw new Error("invalid web binding job");
  }
  for (const id of group.agent_ids) {
    uuid(id);
    if (typeof group.agent_names?.[id] !== "string" || !group.agent_names[id] || group.agent_names[id].length > 255) {
      throw new Error("web binding roster is incomplete");
    }
  }
  if (!job.inviter_robot_ids.every(id => typeof id === "string" && /^[!-~]{1,256}$/.test(id))) {
    throw new Error("invalid web binding inviter");
  }
  return job;
}

// Web requests contain only trusted server-resolved actors and topology. The
// group itself is proven through the configured caller's fresh official roster;
// neither discovery observations nor a user's pasted link authorize invitations.
export async function inspectWebGroupJob(groups, scope, input, stopped = () => false) {
  const job = validateWebGroupJob(input), group = job.binding.group;
  // The server leases for 90 seconds. Bound work to 60 seconds and check again
  // after every asynchronous roster lookup before any external mutation.
  const startedAt = Date.now();
  const cancelled = () => stopped() || Date.now() - startedAt >= 60000;
  const result = { actor_present: false, leader_present: false, leader_agent_id: group.leader_agent_id, leader_robot_id: "" };
  const failed = reason => ({ ...result, reason });
  const lock = JSON.stringify([scope.workspace_id, job.request_id, job.group_id]);
  const prior = groups.locks.get(lock) || Promise.resolve();
  const pending = prior.catch(() => {}).then(async () => {
    if (cancelled()) throw new Error("web binding worker stopped");
    // Persist the generation's immutable target before ANY external operation.
    // A recycled generation cannot reset budgets or redirect the same request.
    const key = JSON.stringify(["web-binding", job.request_id, job.generation]);
    const fingerprint = createHash("sha256").update(JSON.stringify([
      scope.workspace_id, job.group_id, job.sender, job.binding.issue_id,
      group.leader_agent_id, group.agent_ids,
    ])).digest("hex");
    if (groups.attempts[key]?.fingerprint && groups.attempts[key].fingerprint !== fingerprint) {
      throw new Error("web binding generation target changed");
    }
    groups.attempts[key] = { ...groups.attempts[key], fingerprint };
    groups.save();

    let configured;
    try {
      configured = await groups.candidateRobotIds(scope.workspace_id);
      if (!Array.isArray(configured) || configured.length > MAX_CONFIGURED_CANDIDATES) throw new Error("invalid robot candidates");
    } catch { return failed("membership_check_failed"); }
    // Priority is the lease worker, observation hints, then configured robots.
    // Larger sets are scanned in durable bounded batches; repeatedly claimed
    // jobs must not forever miss an in-group robot beyond the first batch.
    const candidates = [...new Set([...job.inviter_robot_ids, ...configured])]
      .filter(id => id !== scope.robot_id && typeof id === "string" && /^[!-~]{1,256}$/.test(id));
    const batchSize = MAX_WEB_GROUP_CANDIDATES - 1;
    const rawCursor = groups.attempts[key].candidateCursor;
    const cursor = Number.isSafeInteger(rawCursor) && rawCursor >= 0 && candidates.length ? rawCursor % candidates.length : 0;
    const batch = candidates.length <= batchSize ? candidates :
      Array.from({ length: batchSize }, (_, index) => candidates[(cursor + index) % candidates.length]);
    groups.attempts[key].candidateCursor = candidates.length > batchSize ? (cursor + batchSize) % candidates.length : 0;
    groups.save();
    let caller, roster, unavailable = false;
    // Observed IDs aid candidate selection only. A manually entered group may
    // have no observations yet; probe this configured robot without inviting it.
    // Other timers may claim the lease, but the actual SDK inviter must freshly
    // prove its own membership. The lease/result scope always remains unchanged.
    for (const robotId of [scope.robot_id, ...batch]) {
      try {
        if (cancelled()) throw new Error("web binding worker stopped");
        const entries = await groups.entries(scope.workspace_id, [], { agentNames: {}, includeRobotId: robotId });
        const candidates = entries.filter(entry => entry.robotId === robotId &&
          (robotId !== scope.robot_id || entry.agentId === scope.agent_id));
        if (candidates.length !== 1 || !candidates[0].client) continue;
        uuid(candidates[0].agentId);
        const candidate = { ...candidates[0], appId: exactGroupId(candidates[0].appId) };
        const current = validatedRoster(await groups.roster(candidate.client, job.group_id));
        if (cancelled()) throw new Error("web binding worker stopped");
        if (!current.bots.some(bot => bot.appId === candidate.appId)) continue;
        caller = candidate; roster = current;
        groups.rememberBots(roster.bots);
        result.actor_present = roster.members.includes(job.sender);
        if (!result.actor_present) return failed("actor_not_in_group");
        break;
      } catch (error) { if (error.groupReason !== "inviter_not_in_group") unavailable = true; }
    }
    if (!caller) return failed(unavailable || candidates.length > batchSize ? "membership_check_failed" : "inviter_not_in_group");

    const refresh = async () => {
      if (cancelled()) throw new Error("web binding worker stopped");
      roster = validatedRoster(await groups.roster(caller.client, job.group_id));
      if (cancelled()) throw new Error("web binding worker stopped");
      groups.rememberBots(roster.bots);
      if (!roster.bots.some(bot => bot.appId === caller.appId)) return "inviter_not_in_group";
      result.actor_present = roster.members.includes(job.sender);
      return result.actor_present ? "" : "actor_not_in_group";
    };
    let leader;
    try {
      const entries = await groups.entries(scope.workspace_id, [group.leader_agent_id], { agentNames: group.agent_names });
      const leaders = entries.filter(entry => entry.agentId === group.leader_agent_id);
      if (leaders.length !== 1) return failed("leader_robot_missing_or_ambiguous");
      leader = { ...leaders[0], appId: exactGroupId(leaders[0].appId) };
      if (leader.appId === caller.appId && leader.robotId !== caller.robotId) return failed("leader_robot_missing_or_ambiguous");
      result.leader_robot_id = leader.robotId;
      if (!leader.client || !leader.send) return failed("leader_robot_unavailable");
    } catch { return failed("leader_robot_missing_or_ambiguous"); }

    const targets = [leader];
    const seenApps = new Set([leader.appId, caller.appId]);
    // A missing/ambiguous optional member does not invalidate a unique leader.
    // It remains uninvited and existing outgoing routing still fails closed on
    // ambiguity rather than inventing an identity or delegating a known member.
    for (const id of group.agent_ids.filter(id => id !== group.leader_agent_id)) {
      try {
        const entries = await groups.entries(scope.workspace_id, [id], { agentNames: group.agent_names });
        const matches = entries.filter(entry => entry.agentId === id);
        if (matches.length !== 1) continue;
        const target = { ...matches[0], appId: exactGroupId(matches[0].appId) };
        if (seenApps.has(target.appId)) continue;
        seenApps.add(target.appId); targets.push(target);
      } catch { /* Optional robots need repaired configuration, not guessed authorization. */ }
    }
    let leaderReason = "leader_not_in_group";
    try {
      for (const target of targets) {
        // Reverify BOTH identities immediately before each invitation, including
        // optional members. Leaving between reads cannot authorize later effects.
        const reason = await refresh();
        if (reason) return failed(reason);
        if (roster.bots.some(bot => bot.appId === target.appId)) continue;
        if (cancelled()) throw new Error("web binding worker stopped");
        const invitation = await groups.maybeInvite(caller, target, { kind: "group", id: job.group_id }, job.binding);
        if (target === leader) leaderReason = REASONS.has(invitation) ? invitation : "invitation_failed";
      }
      // An accepted/approval response is NEVER membership proof. This final
      // read also invalidates success if the applicant or inviter has departed.
      const reason = await refresh();
      if (reason) return failed(reason);
      result.leader_present = roster.bots.some(bot => bot.appId === leader.appId);
      return { ...result, reason: result.leader_present ? "" : leaderReason };
    } catch { return failed("membership_check_failed"); }
  });
  groups.locks.set(lock, pending);
  try { return await pending; }
  finally { if (groups.locks.get(lock) === pending) groups.locks.delete(lock); }
}

export async function pollWebGroupJobs(relay, scope) {
  return relay.groups.webJobLock(scope.workspace_id, async () => {
    if (relay.stopped) return;
    const response = await relay.request("web-group-jobs", scope);
    if (!Array.isArray(response.jobs) || response.jobs.length > 1) throw new Error("invalid web binding job list");
    for (const input of response.jobs) {
      if (relay.stopped) return;
      const job = validateWebGroupJob(input);
      const checked = await inspectWebGroupJob(relay.groups, scope, job, () => relay.stopped);
      if (relay.stopped) return;
      const completed = await relay.request("web-group-result", {
        ...scope, request_id: job.request_id, generation: job.generation,
        lease_token: job.lease_token, group_id: job.group_id, ...checked,
      });
      if (completed.ok !== true || !["pending", "failed", "active"].includes(completed.state) ||
          (completed.state === "active" && (!checked.actor_present || !checked.leader_present || checked.reason))) {
        throw new Error("invalid web binding result");
      }
    }
  });
}
