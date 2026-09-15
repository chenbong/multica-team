import { randomBytes, randomInt, timingSafeEqual } from "node:crypto";

// Login for the admin page. The page has no password store and no mail server:
// it proves who you are by sending a one-time code to your 如流 account through
// the verification robot, which is the same path Multica's own login codes take.
// Sessions live in memory, so restarting the bridge signs everybody out.
const CODE_TTL_MS = 10 * 60 * 1000;
const RESEND_INTERVAL_MS = 60 * 1000;
const MAX_ATTEMPTS = 5;
const SESSION_TTL_MS = 12 * 60 * 60 * 1000;

export class AdminAuth {
  constructor({ allowedDomains = [], allowEmails = [], send, logger = console }) {
    this.allowedDomains = allowedDomains.map((domain) => domain.toLowerCase());
    // Empty means "anybody with an address in an allowed domain who can receive
    // the robot's message". A non-empty list narrows it to those addresses.
    this.allowEmails = allowEmails.map((email) => email.toLowerCase());
    this.send = send;
    this.logger = logger;
    this.pending = new Map();
    this.sessions = new Map();
  }

  async requestCode(rawEmail) {
    const email = normalizeEmail(rawEmail);
    if (!this.#allowed(email)) {
      throw new Error(`只允许 ${this.allowedDomains.join(" / ")} 的邮箱登录`);
    }

    const existing = this.pending.get(email);
    if (existing && Date.now() - existing.at < RESEND_INTERVAL_MS) {
      const wait = Math.ceil((RESEND_INTERVAL_MS - (Date.now() - existing.at)) / 1000);
      throw new Error(`${wait} 秒后才能再要一次验证码`);
    }

    const code = String(randomInt(0, 1_000_000)).padStart(6, "0");
    const user = email.split("@")[0];
    try {
      await this.send(user, [
        `**机器人绑定页面登录验证码：${code}**`,
        "",
        "10 分钟内有效。如果不是你本人在登录，请忽略这条消息。",
      ].join("\n"));
    } catch (error) {
      // Losing the only delivery path would lock everyone out of the page, so the
      // code goes to the local log as a fallback. Reading it already requires an
      // account on this machine, which is also all it takes to edit the config.
      this.logger.error(`could not send a login code to ${user}: ${error.message}`);
      this.logger.warn(`fallback login code for ${email}: ${code}`);
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
      throw new Error("验证码已过期，重新获取一个");
    }
    record.attempts += 1;
    if (record.attempts > MAX_ATTEMPTS) {
      this.pending.delete(email);
      throw new Error("试的次数太多了，重新获取验证码");
    }
    if (!sameCode(record.code, String(rawCode ?? ""))) throw new Error("验证码不对");

    this.pending.delete(email);
    const token = randomBytes(32).toString("hex");
    this.sessions.set(token, { email, at: Date.now() });
    this.logger.log(`admin page login: ${email}`);
    return { token, email, maxAgeSeconds: Math.floor(SESSION_TTL_MS / 1000) };
  }

  session(token) {
    if (!token) return null;
    const record = this.sessions.get(token);
    if (!record) return null;
    if (Date.now() - record.at > SESSION_TTL_MS) {
      this.sessions.delete(token);
      return null;
    }
    return record;
  }

  logout(token) {
    if (token) this.sessions.delete(token);
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

// Constant-time compare so a wrong code leaks nothing through timing; the length
// check first is safe because the length is fixed and public.
function sameCode(expected, given) {
  const a = Buffer.from(expected, "utf8");
  const b = Buffer.from(given.trim(), "utf8");
  return a.length === b.length && timingSafeEqual(a, b);
}
