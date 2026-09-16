import { execFile } from "node:child_process";

// Thin wrapper over the Multica CLI. The CLI carries the signed-in account, so
// the bridge never handles a Multica credential itself — it only picks which
// account to act as. An empty profile is the CLI default
// (~/.multica/config.json); a named one is ~/.multica/profiles/<name>/, which is
// what keeps one robot's account independent of whoever logged in last.
export class Multica {
  constructor({ cli, webUrl, workspaceSlug, workspaceId = "", profile = "" }, logger = console) {
    this.cli = cli;
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
