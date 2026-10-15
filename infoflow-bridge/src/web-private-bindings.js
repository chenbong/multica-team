import { exactGroupId, groupUsername } from "./issue-groups.js";

const UUID = /^[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}$/i;
const ROBOT_ID = /^[!-~]{1,256}$/;

export function validateWebPrivateJob(job) {
  if (!job || typeof job !== "object" || Array.isArray(job) ||
      ![job.request_id, job.generation, job.lease_token, job.agent_id, job.issue_id]
        .every(value => typeof value === "string" && UUID.test(value)) ||
      typeof job.sender !== "string" || groupUsername(job.sender) !== job.sender ||
      typeof job.agent_name !== "string" || !job.agent_name || job.agent_name.length > 255 ||
      typeof job.assignment_version !== "string" || !/^(?:0|[1-9][0-9]{0,18})$/.test(job.assignment_version) ||
      BigInt(job.assignment_version) > 9223372036854775807n) {
    throw new Error("invalid web private binding job");
  }
  return job;
}

// SDK metadata is intentionally reduced to public fields before leaving this
// worker. Never forward rawResponse, credentials, visibility lists or avatars.
export function privateRobotMetadata(info, { includeDisabled = false } = {}) {
  if (!info || typeof info !== "object" || Array.isArray(info)) throw new Error("invalid private robot metadata");
  const enabled = info.openChatDouble === 1 || info.openChatDouble === true;
  if (!enabled && !includeDisabled) return { reason: "private_chat_disabled" };
  if (includeDisabled && ![0, 1, false, true].includes(info.openChatDouble)) throw new Error("invalid private chat metadata");
  if (typeof info.name !== "string" || !info.name.trim() || info.name.length > 255 || /[\u0000-\u001f\u007f]/.test(info.name)) {
    throw new Error("invalid private robot name");
  }
  return { robot_chat_id: exactGroupId(info.puid), robot_name: info.name.trim(), reason: enabled ? "" : "private_chat_disabled" };
}

export async function inspectWebPrivateJob(groups, scope, input, stopped = () => false,
  { now = Date.now, timeoutMs = 60000 } = {}) {
  const job = validateWebPrivateJob(input);
  const result = { target_agent_id: job.agent_id, target_robot_id: "", robot_chat_id: "", robot_name: "", reason: "" };
  const deadline = now() + timeoutMs;
  const check = () => {
    if (stopped() || now() >= deadline) throw new Error("private binding worker stopped or expired");
  };
  // Bound each read as well as the overall job. Neither a hung account lookup
  // nor SDK metadata transport can monopolize a workspace's future leases.
  const bounded = async action => {
    check();
    let timer;
    try {
      const value = await Promise.race([
        Promise.resolve().then(() => { check(); return action(); }),
        new Promise((_, reject) => { timer = setTimeout(() => reject(new Error("private binding worker expired")), deadline - now()); }),
      ]);
      check();
      return value;
    } finally { clearTimeout(timer); }
  };
  let entries;
  try {
    entries = await bounded(() => groups.entries(scope.workspace_id, [job.agent_id], {
      agentNames: { [job.agent_id]: job.agent_name },
    }));
    if (!Array.isArray(entries) || entries.length > 1000) throw new Error("invalid private robot mapping");
  } catch { return { ...result, reason: "private_robot_missing_or_ambiguous" }; }
  // Include disabled/offline matches in uniqueness. Selecting the sole ONLINE
  // robot would conceal another configured robot for the same assigned agent.
  const targets = entries.filter(entry => entry?.agentId === job.agent_id);
  if (targets.length !== 1 || typeof targets[0].robotId !== "string" || !ROBOT_ID.test(targets[0].robotId)) {
    return { ...result, reason: "private_robot_missing_or_ambiguous" };
  }
  const target = targets[0];
  result.target_robot_id = target.robotId;
  if (typeof target.client?.robot?.getInfo !== "function") return { ...result, reason: "private_robot_unavailable" };
  try {
    // No sender/recipient lookup, outbound message, model call or group API.
    const metadata = privateRobotMetadata(await bounded(() => target.client.robot.getInfo()));
    return { ...result, ...metadata };
  } catch { return { ...result, reason: "private_metadata_failed" }; }
}

export async function pollWebPrivateJobs(relay, scope) {
  return relay.groups.webJobLock(scope.workspace_id, async () => {
    if (relay.stopped) return;
    const response = await relay.request("web-private-jobs", scope);
    if (!Array.isArray(response?.jobs) || response.jobs.length > 1) throw new Error("invalid web private binding job list");
    for (const input of response.jobs) {
      if (relay.stopped) return;
      const job = validateWebPrivateJob(input);
      const checked = await inspectWebPrivateJob(relay.groups, scope, job, () => relay.stopped);
      if (relay.stopped) return;
      const completed = await relay.request("web-private-result", {
        ...scope, request_id: job.request_id, generation: job.generation, lease_token: job.lease_token,
        ...checked,
      });
      if (completed?.ok !== true || !["pending", "failed", "active"].includes(completed.state) ||
          (completed.state === "active" && (checked.reason || !checked.target_robot_id || !checked.robot_chat_id))) {
        throw new Error("invalid web private binding result");
      }
    }
  }, "private");
}
