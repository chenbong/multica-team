#!/usr/bin/env node
// Exercise a local staged production build. All API requests use fixtures.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { createRequire } = require("node:module");

const root = fs.realpathSync(process.argv[2]);
const origin = new URL(process.argv[3] || "http://127.0.0.1:3038");
assert(["127.0.0.1", "localhost"].includes(origin.hostname), "Local staging only");
assert(!fs.existsSync(path.join(root, ".git")), "Use a separate staging copy");
const { chromium } = createRequire(path.join(root, "package.json"))("@playwright/test");

(async () => {
  const executablePath = [
    process.env.MULTICA_SMOKE_CHROME,
    chromium.executablePath(),
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
  ].find(candidate => candidate && fs.existsSync(candidate));
  assert(executablePath, "Set MULTICA_SMOKE_CHROME to a local Chrome/Chromium executable");
  const browser = await chromium.launch({ headless: true, executablePath });
  try {
    for (const legacyBuiltins of [false, true]) {
      const context = await browser.newContext({ viewport: { width: legacyBuiltins ? 375 : 1280, height: 812 } });
      if (legacyBuiltins) {
        await context.addInitScript(() => {
          delete globalThis.structuredClone;
          for (const method of ["toSorted", "findLast", "findLastIndex"]) delete Array.prototype[method];
        });
      }
      const page = await context.newPage();
      const errors = [];
      const writes = [];
      page.on("pageerror", error => errors.push(error.message));
      await page.route("**/*", route => {
        const request = route.request();
        const url = new URL(request.url());
        if (url.origin !== origin.origin) return route.abort();
        if (url.pathname.startsWith("/api/")) {
          if (request.method() !== "GET") writes.push(`${request.method()} ${url.pathname}`);
          if (url.pathname === "/api/me") return route.fulfill({ status: 401, contentType: "application/json", body: '{"error":"fixture: signed out"}' });
          return route.fulfill({ status: 200, contentType: "application/json", body: url.pathname === "/api/config" ? '{"allow_signup":true}' : '[]' });
        }
        return route.continue();
      });
      await page.goto(origin.href, { waitUntil: "networkidle" });
      await page.locator('input[type="email"]').waitFor({ state: "visible" });
      assert((await page.locator("body").innerText()).includes(process.env.MULTICA_SMOKE_TITLE || "Multica"));
      assert.equal(new URL(page.url()).pathname, "/");
      assert.equal(await page.locator('a[href="/download"]').count(), 0);
      const runtime = await page.evaluate(() => ({
        sorted: [3, 1, 2].toSorted().join(","),
        last: [1, 2, 3].findLast(value => value < 3),
        index: [1, 2, 3].findLastIndex(value => value < 3),
        cloned: structuredClone({ map: new Map([["fixture", 1]]) }).map.get("fixture"),
        mode: getComputedStyle(document.documentElement).getPropertyValue("--multica-compat-mode").trim(),
        horizontalOverflow: document.documentElement.scrollWidth > innerWidth,
      }));
      assert.deepEqual(runtime, { sorted: "1,2,3", last: 2, index: 1, cloned: 1, mode: "modern", horizontalOverflow: false });
      await page.screenshot({ path: path.join(root, "..", legacyBuiltins ? "login-mobile.png" : "login-desktop.png"), fullPage: true });
      await page.goto(new URL("/login", origin).href, { waitUntil: "networkidle" });
      await page.locator('input[type="email"]').waitFor({ state: "visible" });
      assert.deepEqual(errors, []);
      assert.deepEqual(writes, []);
      console.log(JSON.stringify({ viewport: legacyBuiltins ? "phone" : "desktop", missingBuiltinsRestored: legacyBuiltins, rootAndLogin: "pass", runtime }));
      await context.close();
    }
  } finally {
    await browser.close();
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
