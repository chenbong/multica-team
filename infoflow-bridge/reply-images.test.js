import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, rmSync, readFileSync, statSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { ReplyImages, replyImageConfig, readReplyImage, MAX_REPLY_IMAGE_BYTES } from "./src/reply-images.js";
import { markdownImages, imageMarkdown, replaceMarkdownImages } from "./src/markdown-images.js";
import { formatReply, MAX_REPLY_LENGTH } from "./src/reply-format.js";
import { combinedMarkdown } from "./src/group-markdown.js";
import { privateSdkLogger } from "./src/robots.js";

const workspaceId = "11111111-1111-4111-8111-111111111111";
const png = Buffer.concat([Buffer.from("89504e470d0a1a0a", "hex"), Buffer.from("public test fixture")]);
const id = n => `22222222-2222-4222-8222-${String(n).padStart(12, "0")}`;
const attachment = (n = 1, extra = {}) => ({ id: id(n), filename: `image-${n}.png`, content_type: "image/png", uploader_type: "agent",
  workspace_id: workspaceId, size_bytes: png.length, url: `/uploads/${n}.png`, markdown_url: `https://app.example.test/api/attachments/${id(n)}/download`, ...extra });

function fixture(t, overrides = {}) {
  const directory = mkdtempSync(join(tmpdir(), "reply-images-test-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const config = { cli: "/fake/bcecmd", confPath: "/fake/credentials", bucket: "fixture-bucket", endpoint: "bj.bcebos.com",
    prefix: "reply-images/v1", statePath: join(directory, "cache.json"), since: "2026-01-01T00:00:00Z" };
  const calls = [], downloads = [];
  const run = async (_cli, args) => {
    calls.push(args);
    if (args[3] === "cp") { assert.deepEqual(readFileSync(args[4]), png); return "uploaded"; }
    const path = args[4].slice(`bos:/${config.bucket}/`.length);
    return `https://${config.bucket}.${config.endpoint}/${path}?authorization=${encodeURIComponent("bce-auth-v1/fixture-key/2026-01-01T00:00:00Z/-1//fixture-signature")}`;
  };
  const publisher = new ReplyImages(config, { run, credentialFingerprint: "fixture-key-version", ...overrides });
  const options = { workspaceId, createdAt: "2026-01-02T00:00:00Z", attachments: [attachment()],
    download: async image => { downloads.push(image.id); return png; } };
  return { config, publisher, calls, downloads, options, directory, run };
}

test("outbound hosting is opt-in and rejects incomplete or unsafe configuration", () => {
  assert.equal(replyImageConfig({}), null);
  assert.throws(() => replyImageConfig({ INFOFLOW_REPLY_IMAGES_ENABLED: "true" }), /configuration/);
  const env = { INFOFLOW_REPLY_IMAGES_ENABLED: "true", INFOFLOW_BOS_CLI: "/bin/fake", INFOFLOW_BOS_CONFIG_PATH: "/protected/bos",
    INFOFLOW_BOS_BUCKET: "fixture-bucket", INFOFLOW_BOS_ENDPOINT: "bj.bcebos.com", INFOFLOW_REPLY_IMAGES_STATE: "/state/images.json",
    INFOFLOW_REPLY_IMAGES_SINCE: "2026-01-01T00:00:00Z" };
  assert.equal(replyImageConfig(env).bucket, "fixture-bucket");
  assert.throws(() => replyImageConfig({ ...env, INFOFLOW_BOS_IMAGE_PREFIX: "../escape" }));
  assert.throws(() => replyImageConfig({ ...env, INFOFLOW_BOS_ENDPOINT: "evil.example.test" }));
});

test("upload once across parallel destinations and process restart; preserve canonical input", async t => {
  const f = fixture(t), input = `before\n\n![cat](${attachment().markdown_url})\n\nafter`;
  const [group, privateChat] = await Promise.all([f.publisher.prepare(input, f.options), f.publisher.prepare(input, f.options)]);
  assert.equal(group.content, privateChat.content);
  assert.equal(f.downloads.length, 1);
  assert.equal(f.calls.filter(args => args[3] === "cp").length, 1);
  assert.equal(f.calls.at(-1).at(-1), "-e-1");
  assert(group.content.startsWith("before\n\n![image-1.png](https://fixture-bucket.bj.bcebos.com/"));
  assert(group.content.endsWith("\n\nafter"));
  assert.equal(f.options.attachments[0].markdown_url, attachment().markdown_url);
  const restored = new ReplyImages(f.config, { run: async () => { throw Error("must not reupload"); }, credentialFingerprint: "fixture-key-version" });
  assert.equal((await restored.prepare(input, f.options)).content, group.content);
  assert.equal(statSync(f.config.statePath).mode & 0o777, 0o600);
});

test("retain inline image order, append unreferenced attachments and deduplicate", async t => {
  const f = fixture(t), a = attachment(1), b = attachment(2);
  const body = `one ![b](${b.markdown_url}) two ![b](${b.markdown_url}) three`;
  const out = await f.publisher.prepare(body, { ...f.options, attachments: [a, b, a] });
  const spans = markdownImages(out.content);
  assert.equal(spans.length, 2);
  assert(spans[0].text.includes("image-2.png"));
  assert(spans[1].text.includes("image-1.png"));
  assert.deepEqual(f.downloads, [b.id, a.id]);
  assert(out.content.indexOf("two") < out.content.indexOf("three"));
});

test("never upload user images or backfill old and unversioned replies", async t => {
  const f = fixture(t);
  for (const createdAt of [null, "not-a-date", "2025-12-31T23:59:59Z"]) assert.equal(await f.publisher.prepare("old", { ...f.options, createdAt }), null);
  assert.equal(await f.publisher.prepare("human", { ...f.options, attachments: [attachment(1, { uploader_type: "member" })] }), null);
  assert.equal(f.downloads.length, 0);
});

test("only five reply images; oversized and unsupported formats never download", async t => {
  const f = fixture(t);
  const images = Array.from({ length: 7 }, (_, i) => attachment(i + 1));
  images[0].size_bytes = MAX_REPLY_IMAGE_BYTES + 1;
  images[1].content_type = "image/svg+xml";
  const out = await f.publisher.prepare("text remains", { ...f.options, attachments: images });
  assert.equal(f.downloads.length, 3);
  assert.equal(markdownImages(out.content).length, 3);
  assert(out.content.includes("超过大小限制"));
  assert(out.content.includes("暂不支持该图片格式"));
  assert(out.content.includes("超过 5 张"));
});

test("bounded transfer failure becomes a safe notice; later sends may retry", async t => {
  let attempts = 0;
  const f = fixture(t, { run: async () => { attempts++; throw Error("secret signed URL must not escape"); } });
  const out = await f.publisher.prepare("reply", f.options);
  assert.equal(attempts, 2);
  assert(out.content.startsWith("reply"));
  assert(out.content.includes("图片暂未同步"));
  assert(!out.content.includes("secret"));
  await f.publisher.prepare("next", f.options);
  assert.equal(attempts, 4);
});

test("mismatched bytes and truncated downloads are never published", async t => {
  const f = fixture(t);
  for (const bytes of [Buffer.from("<svg>not a PNG</svg>"), png.subarray(0, png.length - 1)]) {
    const out = await f.publisher.prepare("body", { ...f.options, download: async () => bytes });
    assert(out.content.includes("暂未同步"));
  }
  assert.equal(f.calls.length, 0);
});

test("a paused binding blocks upload even when it changes during download", async t => {
  const f = fixture(t); let active = true;
  const out = await f.publisher.prepare("body", { ...f.options,
    beforeTransfer: async () => { if (!active) throw Error("paused"); },
    download: async () => { active = false; return png; } });
  assert.equal(f.calls.length, 0);
  assert(out.content.includes("暂未同步"));
});

test("BOS URLs must be HTTPS, scoped to the exact object, and non-expiring", t => {
  const f = fixture(t), key = "reply-images/v1/example.png";
  const valid = `https://${f.config.bucket}.${f.config.endpoint}/${key}?authorization=${encodeURIComponent("bce-auth-v1/key/date/-1//signature")}`;
  assert(f.publisher.validURL(valid, key));
  for (const value of [valid.replace("https:", "http:"), valid.replace("/-1/", "/1800/"), valid.replace("fixture-bucket", "another-bucket"), valid + "#fragment"]) {
    // The signature value in an URL is encoded, so test TTL separately below.
    if (value !== valid) assert.equal(f.publisher.validURL(value, key), false);
  }
  assert.equal(f.publisher.validURL(valid.replace("%2F-1%2F", "%2F1800%2F"), key), false);
});

test("stream readers reject oversized, unknown, and HTTP-error image bodies", async () => {
  assert.deepEqual(await readReplyImage(new Response(png)), png);
  await assert.rejects(readReplyImage(new Response("denied", { status: 403 })), /unavailable/);
  await assert.rejects(readReplyImage(new Response(png, { headers: { "content-length": String(MAX_REPLY_IMAGE_BYTES + 1) } })), /unavailable/);
  await assert.rejects(readReplyImage(new Response("<html>not an image</html>")), /unsupported/);
  const stream = new ReadableStream({ start(controller) { for (let i = 0; i < 321; i++) controller.enqueue(new Uint8Array(65536)); controller.close(); } });
  await assert.rejects(readReplyImage(new Response(stream)), /download failed/);
});

test("supported byte signatures preserve PNG, JPEG, WebP and GIF without conversion", async () => {
  for (const bytes of [png, Buffer.from([255, 216, 255, 224, 0, 0]), Buffer.from("GIF89a-public-fixture"), Buffer.from("RIFF0000WEBP-public-fixture")]) {
    assert.deepEqual(await readReplyImage(new Response(bytes)), bytes);
  }
});

test("SDK logging never emits request bodies or permanent image capabilities", () => {
  const lines = [], logger = privateSdkLogger({ warn: value => lines.push(value) });
  for (const method of ["info", "debug", "trace", "warn", "error"]) logger[method]("https://fixture.test/?authorization=private-capability", { secret: "fixture-secret" });
  assert.equal(lines.length, 2);
  assert(lines.every(line => !line.includes("private-capability") && !line.includes("fixture-secret")));
});

test("Markdown rewriting keeps code spans, fences, escapes and unrelated links opaque", () => {
  const body = "![cat](https://example.test/a_(1).png \"caption\")\n`![code](https://example.test/code)`\n\\![escaped](https://example.test/escaped)\n```md\n![fence](https://example.test/fence)\n```\n[normal](https://example.test/link)";
  const found = markdownImages(body);
  assert.equal(found.length, 1);
  assert.equal(found[0].url, "https://example.test/a_(1).png");
  const out = replaceMarkdownImages(body, () => "REPLACED");
  assert(out.includes("`![code]")); assert(out.includes("![fence]")); assert(out.includes("\\![escaped]"));
  assert(out.includes("[normal](https://example.test/link)"));
  assert.equal(markdownImages('![alt](<https://example.test/a.png> "title")')[0].url, "https://example.test/a.png");
});

test("8192-character summaries retain complete image URLs, native AT and footer", () => {
  const urls = Array.from({ length: 5 }, (_, i) => `https://fixture.bj.bcebos.com/image-${i}.png?authorization=${"x".repeat(300)}`);
  const body = urls.map((url, i) => `${"中文🙂".repeat(1700)}\n\n${imageMarkdown(`image-${i}`, url)}`).join("\n\n");
  const formatted = formatReply(body, { url: "https://app.example.test/ws/issues/TEST-1", mode: "issue", identifier: "TEST-1" });
  const delivered = combinedMarkdown(formatted, ["requester", "notified"]);
  assert(delivered.length <= MAX_REPLY_LENGTH);
  assert.deepEqual(markdownImages(delivered).map(i => i.url), urls);
  assert(delivered.includes("@requester @notified"));
  assert(delivered.endsWith("**[完整对话链接](https://app.example.test/ws/issues/TEST-1)**"));
});
