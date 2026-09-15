import { readFileSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

export const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const CONFIG_PATH = resolve(ROOT, "config.local.json");
// Every deployment target is a configuration value, never a literal: this
// repository is published publicly, so hostnames, registries and the e-mail
// domains allowed to log in live in config.local.json / the environment
// instead. INFOFLOW_BASE_URL is the HTTP API of the IM open platform.
export const DEFAULT_BASE_URL = (process.env.INFOFLOW_BASE_URL ?? "").trim().replace(/\/+$/, "");
// The IM SDK picks its own WebSocket gateway by default. A deployment whose
// network can only reach an internal gateway sets these two environment
// variables; empty means "leave the SDK's default in place".
export const DEFAULT_WS_GATEWAY = (process.env.INFOFLOW_WS_GATEWAY ?? "").trim();
export const DEFAULT_WS_CONNECT_DOMAIN = (process.env.INFOFLOW_WS_CONNECT_DOMAIN ?? "").trim();

// Everything the admin page can edit lives in config.local.json (mode 0600,
// git-ignored). One entry binds one InfoFlow robot to one Multica agent.
export function loadConfig() {
  const file = readFile();
  const multica = {
    cli: "../multica-local/bin/multica",
    webUrl: "http://localhost:3000",
    workspaceSlug: "teamharness",
    ...file.multica,
  };
  multica.cli = resolve(ROOT, multica.cli);
  // Which Multica API this bridge belongs to. When a machine hosts more than one
  // instance, this is what stops the page from offering a CLI profile that is
  // signed into the other one. List every address the same instance answers on:
  // a profile written by `multica setup` records whichever one the operator
  // typed, and the loopback and public forms are the same server.
  const trimUrl = (value) => String(value ?? "").trim().replace(/\/+$/, "");
  multica.serverUrl = trimUrl(multica.serverUrl);
  multica.serverUrls = [...new Set([multica.serverUrl, ...(file.multica?.serverUrls ?? []).map(trimUrl)])].filter(Boolean);
  // The backend prints login codes here when no email backend is configured;
  // that is what the verification robot relays into InfoFlow.
  multica.apiLogPath = resolve(ROOT, multica.apiLogPath ?? "../multica/.multica/local/logs/api.log");

  return {
    admin: {
      port: Number(file.admin?.port ?? 4180),
      // Loopback by default. A deployment that has to be reachable from other
      // machines sets host plus the hostnames it will be reached by; the Host
      // header check stays on either way, so a page on another origin cannot
      // resolve a name to this address and drive the API through the browser.
      host: String(file.admin?.host ?? "127.0.0.1").trim() || "127.0.0.1",
      publicHosts: (file.admin?.publicHosts ?? []).map((entry) => String(entry).trim().toLowerCase()).filter(Boolean),
      // Who may log into the admin page. The domain list is the default gate; a
      // non-empty allowEmails narrows it to named addresses.
      allowedDomains: (file.admin?.allowedDomains ?? []).map((domain) => String(domain).trim()).filter(Boolean),
      allowEmails: (file.admin?.allowEmails ?? []).map((email) => String(email).trim()).filter(Boolean),
      // Where an operator creates a robot in the IM open platform. Empty hides
      // the link on the page instead of pointing anywhere.
      infoflowConsoleUrl: String(file.admin?.infoflowConsoleUrl ?? "").trim().replace(/\/+$/, ""),
    },
    multica,
    robots: migrateRobots(file).map(normalizeRobot),
    // Group ids that receive a copy of every verification message sent by the
    // shared verification robot. The robot must already be a member of each
    // group; a group that rejects the message never blocks the direct send.
    verificationMirrorGroupIds: (file.verificationMirrorGroupIds ?? [])
      .map((entry) => String(entry).trim())
      .filter(Boolean),
    // True when the robots array was derived from the pre-admin-page layout, so the
    // caller can persist the new shape once instead of migrating on every boot.
    migratedFromLegacy: !Array.isArray(file.robots) && Boolean(file.infoflow?.appKey),
    pollIntervalMs: file.pollIntervalMs ?? 5000,
    pollTimeoutMs: file.pollTimeoutMs ?? 30 * 60 * 1000,
    statePath: resolve(ROOT, "state.json"),
  };
}

// Writes back only the persisted shape, so runtime-only fields (resolved CLI
// path, state path) never leak into the file.
export function saveRobots(robots, extra = {}) {
  const file = readFile();
  const next = {
    ...file,
    ...extra,
    robots: robots.map(normalizeRobot),
  };
  delete next.infoflow;
  delete next.allowUsers;
  delete next.allowAllInGroups;
  delete next.userMap;
  if (next.multica) delete next.multica.defaultAgent;
  writeFileSync(CONFIG_PATH, JSON.stringify(next, null, 2) + "\n", { mode: 0o600 });
}

// The admin page never receives a secret back; it only learns whether one is set.
export function redactRobot(robot) {
  const { appSecret, ...rest } = robot;
  return { ...rest, hasSecret: Boolean(appSecret) };
}

export function robotIsComplete(robot) {
  const required = robot.purpose === "verification"
    ? ["name", "appId", "appKey", "appSecret"]
    : ["name", "appId", "appKey", "appSecret", "agent"];
  return required.every((key) => String(robot[key] ?? "").trim() !== "");
}

function readFile() {
  try {
    return JSON.parse(readFileSync(CONFIG_PATH, "utf8"));
  } catch (error) {
    if (error.code === "ENOENT") return {};
    throw error;
  }
}

// The first version of this bridge held a single robot under `infoflow`, with the
// agent and allow list at the top level. Fold that into the robots array so an
// existing install keeps working after the upgrade.
function migrateRobots(file) {
  if (Array.isArray(file.robots) && file.robots.length > 0) return file.robots;
  const legacy = file.infoflow;
  if (!legacy?.appKey) return [];
  return [
    {
      id: "default",
      name: "默认机器人",
      appId: legacy.agentId,
      appKey: legacy.appKey,
      appSecret: legacy.appSecret,
      baseUrl: legacy.baseUrl,
      agent: file.multica?.defaultAgent ?? "",
      allowUsers: file.allowUsers ?? [],
      allowAllInGroups: file.allowAllInGroups === true,
      enabled: true,
    },
  ];
}

function normalizeRobot(robot) {
  return {
    id: String(robot.id ?? "").trim() || randomId(),
    name: String(robot.name ?? "").trim(),
    // "task" robots dispatch work to an agent; "verification" robots only relay
    // login codes and deliberately receive nothing.
    purpose: robot.purpose === "verification" ? "verification" : "task",
    // Which Multica account (CLI profile) this robot acts as. Empty is the CLI
    // default profile. Pinning it here is what stops a robot from following
    // whoever last ran `multica login` on this machine.
    profile: String(robot.profile ?? "").trim(),
    // Recorded when the robot is saved so issue links point at the workspace
    // that account actually creates issues in.
    workspaceSlug: String(robot.workspaceSlug ?? "").trim(),
    // Optional deep link that opens this robot's chat in the IM client. The
    // admin page's login hint uses the verification robot's link.
    chatUrl: String(robot.chatUrl ?? "").trim(),
    appId: String(robot.appId ?? "").trim(),
    appKey: String(robot.appKey ?? "").trim(),
    appSecret: String(robot.appSecret ?? "").trim(),
    baseUrl: String(robot.baseUrl ?? "").trim() || DEFAULT_BASE_URL,
    agent: String(robot.agent ?? "").trim(),
    allowUsers: (robot.allowUsers ?? []).map((name) => String(name).trim()).filter(Boolean),
    allowAllInGroups: robot.allowAllInGroups === true,
    enabled: robot.enabled !== false,
  };
}

function randomId() {
  return Math.random().toString(36).slice(2, 10);
}
