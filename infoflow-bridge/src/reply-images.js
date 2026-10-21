import { createHash } from "node:crypto";
import { execFile } from "node:child_process";
import { readFileSync, writeFileSync, renameSync, mkdirSync } from "node:fs";
import { mkdtemp, writeFile, rm } from "node:fs/promises";
import { dirname, join } from "node:path";
import { imageMarkdown, markdownImages, replaceMarkdownImages } from "./markdown-images.js";

export const MAX_REPLY_IMAGES = 5;
export const MAX_REPLY_IMAGE_BYTES = 20 * 1024 * 1024;
const UUID = /^[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}$/i;
const TYPES = new Map([["image/png", "png"], ["image/jpeg", "jpg"], ["image/webp", "webp"], ["image/gif", "gif"]]);
const hash = value => createHash("sha256").update(value).digest("hex");
const missing = "[图片暂未同步，请打开完整对话查看]";

export function replyImageConfig(env = process.env) {
  // Keep operator-only settings out of the admin-editable robot configuration.
  const path = env.INFOFLOW_REPLY_IMAGES_CONFIG_FILE || (env.INFOFLOW_CONFIG_PATH && join(dirname(env.INFOFLOW_CONFIG_PATH), "reply-images.json"));
  let file = {};
  if (path) {
    try { file = JSON.parse(readFileSync(path, "utf8")); }
    catch (error) { if (error.code !== "ENOENT") throw new Error("outbound image configuration unreadable"); }
  }
  if ((env.INFOFLOW_REPLY_IMAGES_ENABLED ?? String(file.enabled === true)) !== "true") return null;
  const config = { cli: env.INFOFLOW_BOS_CLI ?? file.cli, confPath: env.INFOFLOW_BOS_CONFIG_PATH ?? file.confPath,
    bucket: env.INFOFLOW_BOS_BUCKET ?? file.bucket, endpoint: env.INFOFLOW_BOS_ENDPOINT ?? file.endpoint,
    prefix: env.INFOFLOW_BOS_IMAGE_PREFIX ?? file.prefix ?? "infoflow-images/v1", statePath: env.INFOFLOW_REPLY_IMAGES_STATE ?? file.statePath,
    since: env.INFOFLOW_REPLY_IMAGES_SINCE ?? file.since, caFile: env.INFOFLOW_BOS_CA_FILE ?? file.caFile };
  if (!config.cli?.startsWith("/") || !config.confPath?.startsWith("/") || !config.statePath?.startsWith("/") ||
      !/^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$/.test(config.bucket || "") ||
      !/^[a-z0-9-]+\.bcebos\.com$/.test(config.endpoint || "") ||
      !/^[a-zA-Z0-9_-]+(?:\/[a-zA-Z0-9_-]+)*$/.test(config.prefix) || !Number.isFinite(Date.parse(config.since))) {
    throw new Error("invalid outbound image hosting configuration");
  }
  if (config.caFile != null && (typeof config.caFile !== "string" || !config.caFile.startsWith("/"))) throw new Error("invalid outbound image CA path");
  return config;
}

export function imageType(bytes) {
  if (bytes.subarray(0, 8).equals(Buffer.from("89504e470d0a1a0a", "hex"))) return "image/png";
  if (bytes[0] === 255 && bytes[1] === 216 && bytes[2] === 255) return "image/jpeg";
  if (["GIF87a", "GIF89a"].includes(bytes.subarray(0, 6).toString())) return "image/gif";
  if (bytes.subarray(0, 4).toString() === "RIFF" && bytes.subarray(8, 12).toString() === "WEBP") return "image/webp";
  return null;
}

export async function readReplyImage(response) {
  if (!response.ok || Number(response.headers.get("content-length")) > MAX_REPLY_IMAGE_BYTES) {
    await response.body?.cancel(); throw new Error("image unavailable");
  }
  const chunks = []; let size = 0;
  try {
    for await (const chunk of response.body) {
      size += chunk.length;
      if (size > MAX_REPLY_IMAGE_BYTES) throw new Error("image too large");
      chunks.push(Buffer.from(chunk));
    }
  } catch { throw new Error("image download failed"); }
  const bytes = Buffer.concat(chunks);
  if (!bytes.length || !imageType(bytes)) throw new Error("unsupported image bytes");
  return bytes;
}

// The only CLI inputs are trusted configuration, generated paths and UUIDs.
// Never expose subprocess output: it can contain credentials and signed URLs.
export function runBos(cli, args, timeout, caFile = null) {
  return new Promise((resolve, reject) => execFile(cli, args, { timeout, maxBuffer: 128 * 1024,
    env: { ...process.env, ...(caFile ? { SSL_CERT_FILE: caFile } : {}) } },
    (error, stdout) => error ? reject(new Error("BOS operation failed")) : resolve(stdout)));
}

export class ReplyImages {
  constructor(config, { run = runBos, now = Date.now, credentialFingerprint = null } = {}) {
    this.config = config; this.run = run; this.now = now;
    this.since = Date.parse(config.since);
    this.fingerprint = credentialFingerprint || hash(readFileSync(join(config.confPath, "credentials")));
    this.inflight = new Map(); this.queue = Promise.resolve();
    try { this.cache = JSON.parse(readFileSync(config.statePath, "utf8")); }
    catch (error) { if (error.code !== "ENOENT") throw new Error("outbound image cache unreadable"); this.cache = {}; }
    if (!this.cache || typeof this.cache !== "object" || Array.isArray(this.cache)) throw new Error("invalid outbound image cache");
  }

  eligible(createdAt) { const created = Date.parse(createdAt); return Number.isFinite(created) && created >= this.since; }
  save() {
    mkdirSync(dirname(this.config.statePath), { recursive: true, mode: 0o700 });
    writeFileSync(this.config.statePath + ".tmp", JSON.stringify(this.cache), { mode: 0o600 });
    renameSync(this.config.statePath + ".tmp", this.config.statePath);
  }

  validURL(raw, objectKey) {
    try {
      const u = new URL(raw);
      const host = this.config.bucket + "." + this.config.endpoint;
      const expectedPath = "/" + objectKey.split("/").map(encodeURIComponent).join("/");
      const pathStyle = "/" + this.config.bucket + expectedPath;
      return u.protocol === "https:" && !u.username && !u.password && !u.port && !u.hash &&
        ((u.hostname === host && u.pathname === expectedPath) || (u.hostname === this.config.endpoint && u.pathname === pathStyle)) &&
        /^bce-auth-v1\/[^/]+\/[^/]+\/-1\//.test(u.searchParams.get("authorization") || "");
    } catch { return false; }
  }

  async publish(image, workspaceId, download, beforeTransfer, deadline) {
    const key = hash(JSON.stringify([this.config.bucket, this.config.endpoint, this.config.prefix, this.fingerprint, workspaceId, image.id]));
    const cached = this.cache[key];
    if (cached && this.validURL(cached.url, cached.objectKey)) return cached.url;
    if (this.inflight.has(key)) return this.inflight.get(key);
    const work = this.queue.catch(() => {}).then(async () => {
      if (this.now() >= deadline) throw new Error("image transfer deadline exceeded");
      let directory;
      try {
        for (let attempt = 0; attempt < 2; attempt++) {
          try {
            await beforeTransfer?.();
            const remaining = () => { const ms = deadline - this.now(); if (ms <= 0) throw new Error("image transfer deadline exceeded"); return ms; };
            const bytes = await download(image, Math.min(10000, remaining()));
            if (!Buffer.isBuffer(bytes) || bytes.length !== image.size_bytes || !bytes.length || bytes.length > MAX_REPLY_IMAGE_BYTES || imageType(bytes) !== image.content_type) throw new Error("invalid image content");
            const objectKey = `${this.config.prefix}/${workspaceId}/${image.id}/${hash(bytes)}.${TYPES.get(image.content_type)}`;
            directory ||= await mkdtemp(join(dirname(this.config.statePath), ".reply-image-"));
            const local = join(directory, "image." + TYPES.get(image.content_type));
            await writeFile(local, bytes, { mode: 0o600 });
            const common = ["--conf-path", this.config.confPath, "bos"];
            await beforeTransfer?.();
            await this.run(this.config.cli, [...common, "cp", local, `bos:/${this.config.bucket}/${objectKey}`, "--content-type", image.content_type,
              "--content-disposition", "inline", "--disable-bar", "--quiet", "--yes"], Math.min(20000, remaining()), this.config.caFile);
            const output = await this.run(this.config.cli, [...common, "gen_signed_url", `bos:/${this.config.bucket}/${objectKey}`, "-e-1"], Math.min(5000, remaining()), this.config.caFile);
            const raw = output.match(/https?:\/\/[^\s]+/)?.[0];
            const url = raw?.replace(/^http:/, "https:");
            if (!this.validURL(url, objectKey)) throw new Error("invalid BOS image URL");
            this.cache[key] = { objectKey, url };
            this.save(); return url;
          } catch { if (attempt === 1 || this.now() >= deadline) throw new Error("image transfer failed"); }
        }
      } finally { if (directory) await rm(directory, { recursive: true, force: true }); }
    });
    this.queue = work.catch(() => {}); this.inflight.set(key, work);
    try { return await work; } finally { this.inflight.delete(key); }
  }

  async prepare(content, { attachments = [], total = null, workspaceId, createdAt, download, beforeTransfer } = {}) {
    if (!this.eligible(createdAt)) return null;
    const unique = [...new Map(attachments.filter(a => a?.uploader_type === "agent" && /^image\//i.test(a.content_type || ""))
      .map(a => [a.id, a])).values()];
    if (!unique.length && !total) return null;
    if (!UUID.test(workspaceId || "")) return { content: String(content) + "\n\n" + missing };
    mkdirSync(dirname(this.config.statePath), { recursive: true, mode: 0o700 });
    const images = markdownImages(String(content)), positions = new Map();
    const aliases = image => new Set([image.url, image.markdown_url, `/api/attachments/${image.id}/download`, `/api/attachments/${image.id}/content`].filter(Boolean));
    for (const a of unique) {
      const known = aliases(a);
      positions.set(a.id, images.findIndex(i => known.has(i.url) || (() => { try { const u = new URL(i.url); return known.has(u.pathname) && /^\/api\/attachments\//.test(u.pathname); } catch { return false; } })()));
    }
    unique.sort((a, b) => (positions.get(a.id) < 0 ? Infinity : positions.get(a.id)) - (positions.get(b.id) < 0 ? Infinity : positions.get(b.id)));
    const rendered = new Map(), deadline = this.now() + 45000;
    for (let i = 0; i < unique.length; i++) {
      const a = unique[i]; let output = missing;
      if (i >= MAX_REPLY_IMAGES) output = "[图片超过 5 张，请打开完整对话查看]";
      else if (!TYPES.has(a.content_type)) output = "[暂不支持该图片格式，请打开完整对话查看]";
      else if (!Number.isSafeInteger(a.size_bytes) || a.size_bytes <= 0 || a.size_bytes > MAX_REPLY_IMAGE_BYTES) output = "[图片超过大小限制或不可读取，请打开完整对话查看]";
      else if (UUID.test(a.id || "")) {
        try { output = imageMarkdown(a.filename, await this.publish(a, workspaceId, download, beforeTransfer, deadline)); }
        catch { output = missing; }
      }
      rendered.set(a.id, output);
    }
    const seen = new Set();
    const matched = url => unique.find(a => aliases(a).has(url) || (() => { try { const u = new URL(url); return /^\/api\/attachments\//.test(u.pathname) && aliases(a).has(u.pathname); } catch { return false; } })());
    let result = replaceMarkdownImages(String(content), span => {
      const a = matched(span.url); if (!a) return span.text;
      if (seen.has(a.id)) return "";
      seen.add(a.id); return rendered.get(a.id);
    });
    for (const a of unique) if (!seen.has(a.id)) { result += "\n\n" + rendered.get(a.id); seen.add(a.id); }
    if ((total ?? unique.length) > unique.length) result += "\n\n[其余图片请打开完整对话查看]";
    return { content: result };
  }
}
