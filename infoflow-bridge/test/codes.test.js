import assert from 'node:assert/strict';
import test from 'node:test';
import { mkdtemp, writeFile, appendFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { VerificationCodeRelay } from '../src/codes.js';

test('login relay uses Team Mesh branding without changing code or expiry copy', { timeout: 5000 }, async (t) => {
  const directory = await mkdtemp(join(tmpdir(), 'bridge-code-copy-'));
  const logPath = join(directory, 'fixture.log');
  await writeFile(logPath, '[DEV] Verification code for stale@example.test: 111111\n');
  const messages = [];
  let delivered;
  const sent = new Promise(resolve => { delivered = resolve; });
  const relay = new VerificationCodeRelay({
    logPath, allowedDomains: ['example.test'], logger: {log() {}, warn() {}, error() {}},
    send: async (user, content) => { messages.push({user, content}); delivered(); },
  });
  t.after(async () => { relay.stop(); await rm(directory, {recursive: true, force: true}); });
  await relay.start();
  await appendFile(logPath, '[DEV] Verification code for alice@example.test: 000123\n');
  await sent;
  assert.deepEqual(messages, [{
    user: 'alice',
    content: '**Team Mesh 登录验证码：000123**\n\n10 分钟内有效。如果不是你本人在登录，请忽略这条消息。',
  }]);
});
