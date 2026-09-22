import { execFile } from "node:child_process";
import { readFile } from "node:fs/promises";
import { homedir } from "node:os";
import { join, basename, extname } from "node:path";

// Thin wrapper over the Multica CLI. The CLI carries the signed-in account, so
// the bridge never handles a Multica credential itself — it only picks which
// account to act as. An empty profile is the CLI default
// (~/.multica/config.json); a named one is ~/.multica/profiles/<name>/, which is
// what keeps one robot's account independent of whoever logged in last.
export class Multica {
  constructor({ cli, webUrl, serverUrl = "", workspaceSlug, workspaceId = "", profile = "" }, logger = console) {
    this.cli = cli;
    this.serverUrl = serverUrl;
    this.webUrl = webUrl;
    this.workspaceSlug = workspaceSlug;
    this.profile = String(profile ?? "").trim();
    // The workspace every call should act in. A CLI profile pins one workspace
    // (the one written at login), so a page that has to reach an account's other
    // workspaces passes the id explicitly — `--workspace-id` overrides the
    // profile for that one command, and the CLI supports it on every command.
    this.workspaceId = String(workspaceId ?? "").trim();
    this.logger = logger;
  }

  issueUrl(issue) {
    const ref = issue.identifier ?? issue.key ?? issue.id;
    return `${this.webUrl}/${this.workspaceSlug}/issues/${ref}`;
  }

  async listAgents() {
    return this.#json(["agent", "list", "--output", "json"]);
  }

  async request(path, { method = "GET", body } = {}) {
    if (this.profile && !/^[a-zA-Z0-9._-]+$/.test(this.profile)) throw new Error("Invalid profile");
    const configPath = this.profile
      ? join(homedir(), ".multica", "profiles", this.profile, "config.json")
      : join(homedir(), ".multica", "config.json");
    const config = JSON.parse(await readFile(configPath, "utf8"));
    if (!config.token) throw new Error("机器人绑定账号尚未登录 Multica");
    const headers = { Authorization: `Bearer ${config.token}`, "X-Workspace-ID": this.workspaceId || config.workspace_id };
    const multipart = body instanceof FormData;
    if (body !== undefined && !multipart) headers["Content-Type"] = "application/json";
    const response = await fetch(`${(this.serverUrl || config.server_url).replace(/\/+$/, "")}${path}`, {
      method, headers, body: body === undefined ? undefined : multipart ? body : JSON.stringify(body),
      redirect: "error", signal: AbortSignal.timeout(20000),
    });
    if (!response.ok) {
      // Do not echo remote bodies, which can contain credentials or private input.
      const error = new Error(`Multica ${method} ${path}: HTTP ${response.status}`);
      error.status = response.status;
      throw error;
    }
    return response.status === 204 ? null : response.json();
  }

  createChatSession(agentId, title) {
    return this.request("/api/chat/sessions", { method: "POST", body: { agent_id: agentId, title } });
  }

  chatSession(id) { return this.request(`/api/chat/sessions/${encodeURIComponent(id)}`); }
  chatMessages(id) { return this.request(`/api/chat/sessions/${encodeURIComponent(id)}/messages`); }
  chatPending(id) { return this.request(`/api/chat/sessions/${encodeURIComponent(id)}/pending-task`); }
  sendChatMessage(id, content, attachmentIds = []) {
    return this.request(`/api/chat/sessions/${encodeURIComponent(id)}/messages`, {
      method: "POST", body: { content, attachment_ids: attachmentIds },
    });
  }

  async uploadChatImage(sessionId, path) {
    const type = { ".png": "image/png", ".jpg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp" }[extname(path)] || "application/octet-stream";
    const form = new FormData();
    form.append("file", new Blob([await readFile(path)], { type }), basename(path));
    form.append("chat_session_id", sessionId);
    return this.request("/api/upload-file", { method: "POST", body: form });
  }

  getIssue(id) { return this.#json(["issue", "get", id, "--output", "json"]); }

  async createIssue({ title, description, assignee, attachments = [] }) {
    const attachmentArgs = attachments.flatMap((path) => ["--attachment", path]);
    return this.#json(
      // --allow-duplicate: Multica refuses a second active issue with the same
      // title, which is right for the UI but wrong for chat, where asking the
      // same thing twice is normal. Accidental repeats are caught by the
      // bridge's own short repeat window instead.
      [
        "issue",
        "create",
        "--title",
        title,
        "--description-stdin",
        "--assignee",
        assignee,
        "--allow-duplicate",
        ...attachmentArgs,
        "--output",
        "json",
      ],
      description,
    );
  }

  async runs(issueId) {
    return this.#json(["issue", "runs", issueId, "--output", "json"]);
  }

  async comments(issueId) {
    return this.#json(["issue", "comment", "list", issueId, "--output", "json"]);
  }

  async addComment(issueId, body) {
    return this.#json(["issue", "comment", "add", issueId, "--body", body, "--output", "json"]);
  }

  #json(args, stdin) {
    return new Promise((resolvePromise, reject) => {
      const scoped = [
        ...(this.serverUrl ? ["--server-url", this.serverUrl] : []),
        ...(this.profile === "" ? [] : ["--profile", this.profile]),
        ...(this.workspaceId === "" ? [] : ["--workspace-id", this.workspaceId]),
        ...args,
      ];
      const child = execFile(
        this.cli,
        scoped,
        { maxBuffer: 16 * 1024 * 1024, timeout: 120000 },
        (error, stdout, stderr) => {
          if (error) {
            reject(new Error(`multica ${args[0]} ${args[1] ?? ""} failed: ${(stderr || error.message).trim()}`));
            return;
          }
          const text = stdout.trim();
          if (text === "") {
            resolvePromise(null);
            return;
          }
          try {
            resolvePromise(JSON.parse(text));
          } catch {
            reject(new Error(`multica ${args[0]} ${args[1] ?? ""} returned non-JSON output: ${text.slice(0, 200)}`));
          }
        },
      );
      if (stdin !== undefined) {
        child.stdin.end(stdin);
      }
    });
  }
}

export const TERMINAL_RUN_STATUSES = new Set(["completed", "failed", "cancelled"]);

export function latestRun(runs) {
  const list = Array.isArray(runs) ? runs : (runs?.runs ?? []);
  return list[0] ?? null;
}
