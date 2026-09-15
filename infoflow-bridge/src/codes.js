import { open, stat } from "node:fs/promises";

// Multica prints "[DEV] Verification code for <email>: <code>" whenever it has no
// email backend configured. This watcher turns that line into an InfoFlow direct
// message, so a login code reaches the one person it belongs to without any mail
// server in between.
const CODE_LINE = /\[DEV\] Verification code for (\S+@[A-Za-z0-9.-]+): (\d{6})/g;
const POLL_INTERVAL_MS = 1000;
const DEDUPE_LIMIT = 50;

export class VerificationCodeRelay {
  constructor({ logPath, send, allowedDomains = [], logger = console }) {
    this.logPath = logPath;
    this.send = send;
    this.allowedDomains = allowedDomains;
    this.logger = logger;
    this.offset = null;
    this.recent = [];
    this.timer = null;
  }

  async start() {
    if (this.timer) return;
    // Start at the current end of file: codes printed before the relay came up are
    // already stale, and replaying them would message people out of the blue.
    this.offset = await this.#size();
    this.timer = setInterval(() => {
      this.#tick().catch((error) => this.logger.warn(`code relay tick failed: ${error.message}`));
    }, POLL_INTERVAL_MS);
    this.logger.log(`verification code relay watching ${this.logPath}`);
  }

  stop() {
    if (!this.timer) return;
    clearInterval(this.timer);
    this.timer = null;
    this.logger.log("verification code relay stopped");
  }

  async #tick() {
    const size = await this.#size();
    if (size === null) return;
    // A rotated or truncated log restarts from the beginning.
    if (this.offset === null || size < this.offset) this.offset = 0;
    if (size === this.offset) return;

    // Read only what was appended. The API log grows continuously (pool stats every
    // 15s), so re-reading the whole file every second would scale with its size.
    const length = size - this.offset;
    const buffer = Buffer.allocUnsafe(length);
    const handle = await open(this.logPath, "r");
    try {
      await handle.read(buffer, 0, length, this.offset);
    } finally {
      await handle.close();
    }
    this.offset = size;
    const chunk = buffer.toString("utf8");

    for (const match of chunk.matchAll(CODE_LINE)) {
      await this.#deliver(match[1].toLowerCase(), match[2]);
    }
  }

  async #deliver(email, code) {
    const key = `${email}:${code}`;
    if (this.recent.includes(key)) return;
    this.recent.push(key);
    if (this.recent.length > DEDUPE_LIMIT) this.recent = this.recent.slice(-DEDUPE_LIMIT);

    const [user, domain] = email.split("@");
    if (!this.allowedDomains.includes(domain)) {
      this.logger.warn(`not relaying a code for ${email}: domain is not in the allow list`);
      return;
    }

    try {
      await this.send(user, [
        `**Multica 登录验证码：${code}**`,
        "",
        "10 分钟内有效。如果不是你本人在登录，请忽略这条消息。",
      ].join("\n"));
      this.logger.log(`relayed a login code to ${user}`);
    } catch (error) {
      // The usual cause is the robot not being visible to that person yet.
      this.logger.error(`could not relay the code to ${user}: ${error.message}`);
    }
  }

  async #size() {
    try {
      return (await stat(this.logPath)).size;
    } catch (error) {
      if (error.code !== "ENOENT") throw error;
      return null;
    }
  }
}
