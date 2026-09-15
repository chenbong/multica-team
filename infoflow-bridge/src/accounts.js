import { readFile, readdir } from "node:fs/promises";
import { homedir } from "node:os";
import { join } from "node:path";

// The Multica CLI keeps one signed-in account per profile: the default one in
// ~/.multica/config.json and every named one under ~/.multica/profiles/<name>/.
// This module reads those files to find out which Multica accounts this machine
// can act as, and asks each one's server who it is. Tokens never leave here —
// the admin API only ever sees a profile name, a person, and a workspace.
const MULTICA_HOME = join(homedir(), ".multica");
const CACHE_TTL_MS = 30_000;
const API_TIMEOUT_MS = 8000;

export class Accounts {
  constructor(logger = console) {
    this.logger = logger;
    this.cache = new Map();
  }

  // Profiles that actually hold a token. A profile file without one is a
  // half-finished login and cannot answer for anybody.
  async list() {
    const files = [{ profile: "", path: join(MULTICA_HOME, "config.json") }];
    for (const name of await this.#profileNames()) {
      files.push({ profile: name, path: join(MULTICA_HOME, "profiles", name, "config.json") });
    }

    const accounts = [];
    for (const file of files) {
      const config = await readJSON(file.path);
      if (!config?.token) continue;
      accounts.push({
        profile: file.profile,
        serverUrl: String(config.server_url ?? "").replace(/\/+$/, ""),
        workspaceId: String(config.workspace_id ?? ""),
        token: config.token,
      });
    }
    return accounts;
  }

  async describeAll() {
    const accounts = await this.list();
    return Promise.all(accounts.map((account) => this.#describe(account)));
  }

  async describe(profile) {
    const account = (await this.list()).find((entry) => entry.profile === profile);
    return account ? this.#describe(account) : null;
  }

  // user id -> person, for the workspace this profile points at, so the agent
  // list can name an owner instead of showing a raw uuid.
  async members(profile) {
    const account = (await this.list()).find((entry) => entry.profile === profile);
    if (!account?.workspaceId) return {};
    try {
      const rows = await this.#api(account, `/api/workspaces/${account.workspaceId}/members`);
      return Object.fromEntries(rows.map((row) => [row.user_id, { name: row.name, email: row.email, role: row.role }]));
    } catch (error) {
      this.logger.warn(`could not list members for profile ${profile || "(default)"}: ${error.message}`);
      return {};
    }
  }

  async #profileNames() {
    try {
      return (await readdir(join(MULTICA_HOME, "profiles"))).sort();
    } catch (error) {
      if (error.code === "ENOENT") return [];
      throw error;
    }
  }

  // A profile whose server is down or whose token expired is reported with its
  // error rather than dropped, otherwise it silently disappears from the page
  // and the robot bound to it looks unconfigured.
  async #describe(account) {
    const cached = this.cache.get(account.profile);
    if (cached && Date.now() - cached.at < CACHE_TTL_MS) return cached.value;

    const value = {
      profile: account.profile,
      label: account.profile === "" ? "默认 profile" : account.profile,
      serverUrl: account.serverUrl,
      workspaceId: account.workspaceId,
      userId: null,
      name: null,
      email: null,
      workspaceName: null,
      workspaceSlug: null,
      warning: null,
      error: null,
    };

    try {
      const me = await this.#api(account, "/api/me");
      value.userId = me.id;
      value.name = me.name;
      value.email = me.email;

      const workspaces = await this.#api(account, "/api/workspaces");
      const workspace = workspaces.find((entry) => entry.id === account.workspaceId);
      if (workspace) {
        value.workspaceName = workspace.name;
        value.workspaceSlug = workspace.slug;
      } else if (account.workspaceId === "") {
        // Token login leaves workspace_id empty, and every CLI call that needs a
        // workspace then fails. Say so here instead of at dispatch time.
        value.warning = "这个 profile 还没有选工作区，运行 multica --profile <名字> config set workspace_id <id>";
      } else {
        value.warning = "配置里的工作区不在这个账号的可见范围内";
      }
    } catch (error) {
      value.error = error.message;
    }

    this.cache.set(account.profile, { at: Date.now(), value });
    return value;
  }

  async #api(account, path) {
    if (!account.serverUrl) throw new Error("这个 profile 没有配置 server_url");
    const response = await fetch(account.serverUrl + path, {
      headers: { authorization: `Bearer ${account.token}` },
      signal: AbortSignal.timeout(API_TIMEOUT_MS),
    });
    if (!response.ok) throw new Error(`${path} 返回 ${response.status}`);
    return response.json();
  }
}

async function readJSON(path) {
  try {
    return JSON.parse(await readFile(path, "utf8"));
  } catch {
    return null;
  }
}
