import { createHash, randomBytes, randomInt, timingSafeEqual } from "node:crypto";
import {
  chmodSync,
  mkdirSync,
  readFileSync,
  renameSync,
  unlinkSync,
  writeFileSync,
} from "node:fs";
import { dirname } from "node:path";

// Login for the admin page. The page has no password store and no mail server:
// it proves who you are by sending a one-time code to your 如流 account through
// the verification robot, which is the same path Multica's own login codes take.
// The code remains one-time; only the resulting admin session is persistent.
const CODE_TTL_MS = 10 * 60 * 1000;
const RESEND_INTERVAL_MS = 60 * 1000;
const MAX_ATTEMPTS = 5;
const SESSION_TTL_MS = 30 * 24 * 60 * 60 * 1000;
const SESSION_STORE_VERSION = 1;

export class AdminAuth {
  constructor({ allowedDomains = [], allowEmails = [], send, sessionPath, logger = console }) {
    this.allowedDomains = allowedDomains.map((domain) => domain.toLowerCase());
    // Empty means "anybody with an address in an allowed domain who can receive
    // the robot's message". A non-empty list narrows it to those addresses.
    this.allowEmails = allowEmails.map((email) => email.toLowerCase());
    this.send = send;
    this.logger = logger;
    this.sessionPath = String(sessionPath ?? "").trim();
    this.pending = new Map();
    this.sessions = new Map();
    this.#loadSessions();
  }

  async requestCode(rawEmail) {
    const email = normalizeEmail(rawEmail);
    if (!this.#allowed(email)) {
      throw new Error("只允许 " + this.allowedDomains.join(" / ") + " 的邮箱登录");
    }

    const existing = this.pending.get(email);
    if (existing && Date.now() - existing.at < RESEND_INTERVAL_MS) {
      const wait = Math.ceil((RESEND_INTERVAL_MS - (Date.now() - existing.at)) / 1000);
      throw new Error(String(wait) + " 秒后才能再要一次验证码");
    }

    const code = String(randomInt(0, 1_000_000)).padStart(6, "0");
    const user = email.split("@")[0];
    try {
      await this.send(user, [
        "**机器人绑定页面登录验证码：" + code + "**",
        "",
        "10 分钟内有效。如果不是你本人在登录，请忽略这条消息。",
      ].join("\n"));
    } catch (error) {
      // Losing the only delivery path would lock everyone out of the page, so the
      // code goes to the local log as a fallback. Reading it already requires an
      // account on this machine, which is also all it takes to edit the config.
      this.logger.error("could not send a login code to " + user + ": " + error.message);
      this.logger.warn("fallback login code for " + email + ": " + code);
    }

    this.pending.set(email, { code, at: Date.now(), attempts: 0 });
    return { email, user };
  }

  verify(rawEmail, rawCode) {
    const email = normalizeEmail(rawEmail);
    const record = this.pending.get(email);
    if (!record) throw new Error("先获取验证码");
    if (Date.now() - record.at > CODE_TTL_MS) {
      this.pending.delete(email);
      throw new Error("验证码已过期，重新获取验证码");
    }
    record.attempts += 1;
    if (record.attempts > MAX_ATTEMPTS) {
      this.pending.delete(email);
      throw new Error("试的次数太多了，重新获取验证码");
    }
    if (!sameCode(record.code, String(rawCode ?? ""))) throw new Error("验证码不对");

    const now = Date.now();
    const token = randomBytes(32).toString("hex");
    const tokenHash = hashSessionToken(token);
    this.sessions.set(tokenHash, {
      email,
      at: now,
      expiresAt: now + SESSION_TTL_MS,
    });
    try {
      this.#persistSessions();
    } catch (error) {
      this.sessions.delete(tokenHash);
      this.logger.error("could not persist admin session: " + error.message);
      throw new Error("无法保存登录会话");
    }

    // The verification code is consumed only after the durable session is saved.
    this.pending.delete(email);
    this.logger.log("admin page login: " + email);
    return {
      token,
      email,
      maxAgeSeconds: Math.floor(SESSION_TTL_MS / 1000),
    };
  }

  session(token) {
    const rawToken = String(token ?? "").trim();
    if (!rawToken) return null;
    const tokenHash = hashSessionToken(rawToken);
    const record = this.sessions.get(tokenHash);
    if (!record) return null;
    if (!Number.isFinite(record.expiresAt) || Date.now() >= record.expiresAt) {
      this.sessions.delete(tokenHash);
      try {
        this.#persistSessions();
      } catch (error) {
        this.logger.warn("could not prune expired admin session: " + error.message);
      }
      return null;
    }
    return { email: record.email, at: record.at };
  }

  logout(token) {
    const rawToken = String(token ?? "").trim();
    if (!rawToken) return;
    const tokenHash = hashSessionToken(rawToken);
    const record = this.sessions.get(tokenHash);
    if (!record) return;
    this.sessions.delete(tokenHash);
    try {
      this.#persistSessions();
    } catch (error) {
      this.sessions.set(tokenHash, record);
      throw error;
    }
  }

  #loadSessions() {
    if (!this.sessionPath) {
      this.logger.warn("admin session path is not configured; sessions will not survive a restart");
      return;
    }

    let parsed;
    try {
      parsed = JSON.parse(readFileSync(this.sessionPath, "utf8"));
    } catch (error) {
      if (error.code === "ENOENT") return;
      this.logger.error("could not load persisted admin sessions: " + error.message);
      return;
    }

    const entries = Array.isArray(parsed?.sessions) ? parsed.sessions : [];
    const now = Date.now();
    let pruned = false;
    for (const entry of entries) {
      const tokenHash = String(entry?.tokenHash ?? "");
      const email = normalizeEmail(entry?.email);
      const createdAt = Number(entry?.createdAt);
      const expiresAt = Number(entry?.expiresAt);
      if (
        !/^[0-9a-f]{64}$/.test(tokenHash) ||
        !/^[^@\s]+@[^@\s]+$/.test(email) ||
        !Number.isFinite(createdAt) ||
        !Number.isFinite(expiresAt) ||
        expiresAt <= now
      ) {
        pruned = true;
        continue;
      }
      this.sessions.set(tokenHash, { email, at: createdAt, expiresAt });
    }

    if (pruned) {
      try {
        this.#persistSessions();
      } catch (error) {
        this.logger.warn("could not prune persisted admin sessions: " + error.message);
      }
    }
  }

  #persistSessions() {
    if (!this.sessionPath) {
      throw new Error("admin session path is not configured");
    }

    const directory = dirname(this.sessionPath);
    mkdirSync(directory, { recursive: true, mode: 0o700 });
    const payload = JSON.stringify({
      version: SESSION_STORE_VERSION,
      sessions: Array.from(this.sessions.entries()).map(([tokenHash, record]) => ({
        tokenHash,
        email: record.email,
        createdAt: record.at,
        expiresAt: record.expiresAt,
      })),
    }, null, 2) + "\n";
    const temporaryPath = this.sessionPath + ".tmp";

    try {
      writeFileSync(temporaryPath, payload, { encoding: "utf8", mode: 0o600 });
      chmodSync(temporaryPath, 0o600);
      renameSync(temporaryPath, this.sessionPath);
    } catch (error) {
      try {
        unlinkSync(temporaryPath);
      } catch {
        // Ignore cleanup errors and report the original persistence failure.
      }
      throw error;
    }
  }

  #allowed(email) {
    if (!/^[^@\s]+@[^@\s]+$/.test(email)) return false;
    if (this.allowEmails.length > 0) return this.allowEmails.includes(email);
    return this.allowedDomains.includes(email.split("@")[1]);
  }
}

function normalizeEmail(value) {
  return String(value ?? "").trim().toLowerCase();
}

function hashSessionToken(token) {
  return createHash("sha256").update(token).digest("hex");
}

// Constant-time compare so a wrong code leaks nothing through timing; the length
// check first is safe because the length is fixed and public.
function sameCode(expected, given) {
  const a = Buffer.from(expected, "utf8");
  const b = Buffer.from(given.trim(), "utf8");
  return a.length === b.length && timingSafeEqual(a, b);
}
