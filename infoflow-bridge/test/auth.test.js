import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { AdminAuth } from "../src/auth.js";

const silentLogger = {
  error() {},
  log() {},
  warn() {},
};

test("admin sessions survive a bridge restart while codes stay one-time", async () => {
  const directory = mkdtempSync(join(tmpdir(), "infoflow-bridge-auth-"));
  const sessionPath = join(directory, "admin-sessions.json");
  const sent = [];

  const makeAuth = () => new AdminAuth({
    allowedDomains: ["baidu.com"],
    send: async (_user, content) => sent.push(content),
    sessionPath,
    logger: silentLogger,
  });

  try {
    const auth = makeAuth();
    await auth.requestCode("chenbohong@baidu.com");
    const code = sent[0].match(/[0-9]{6}/)?.[0];
    assert.ok(code);

    const login = auth.verify("chenbohong@baidu.com", code);
    assert.equal(login.maxAgeSeconds, 30 * 24 * 60 * 60);
    assert.equal(auth.session(login.token)?.email, "chenbohong@baidu.com");
    assert.throws(
      () => auth.verify("chenbohong@baidu.com", code),
      /先获取验证码/,
    );

    const persisted = JSON.parse(readFileSync(sessionPath, "utf8"));
    assert.equal(persisted.version, 1);
    assert.equal(persisted.sessions.length, 1);
    assert.equal(persisted.sessions[0].tokenHash.includes(login.token), false);

    const restarted = makeAuth();
    assert.equal(restarted.session(login.token)?.email, "chenbohong@baidu.com");

    restarted.logout(login.token);
    const afterLogout = makeAuth();
    assert.equal(afterLogout.session(login.token), null);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});
