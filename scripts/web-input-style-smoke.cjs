#!/usr/bin/env node
// A fresh local staging context only: no real accounts or API writes.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { createRequire } = require("node:module");

const root = fs.realpathSync(process.argv[2]);
const origin = new URL(process.argv[3] || "http://127.0.0.1:3047");
assert(["127.0.0.1", "localhost"].includes(origin.hostname), "Local staging only");
assert(!fs.existsSync(path.join(root, ".git")), "Use an isolated generated Web tree");
const { chromium } = createRequire(path.join(root, "package.json"))("@playwright/test");
const foreignStyle = ".border-input{border:none;width:28px;height:100%;padding:0;font-family:monospace;font-size:11px}";

(async () => {
  const executablePath = [process.env.MULTICA_SMOKE_CHROME, chromium.executablePath(),
    "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
  ].find(file => file && fs.existsSync(file));
  assert(executablePath, "A local Chromium executable is required");
  const browser = await chromium.launch({ headless: true, executablePath });
  try {
    for (const colorScheme of ["light", "dark"]) {
      for (const width of [375, 1280]) {
        const context = await browser.newContext({ viewport: { width, height: 812 }, colorScheme });
        try {
          const page = await context.newPage();
          await context.addCookies([{ name: "locale", value: "zh-Hans", url: origin.origin }]);
          const errors = [], writes = [];
          page.on("pageerror", error => errors.push(error.message));
          await page.route("**/*", route => {
            const request = route.request(), url = new URL(request.url());
            if (url.origin !== origin.origin) return route.abort();
            if (url.pathname.startsWith("/api/")) {
              if (url.pathname === "/api/auth/send-code" && request.method() === "POST") {
                assert.equal(JSON.parse(request.postData()).email, "fixture@baidu.com");
                return route.fulfill({ status: 200, contentType: "application/json", body: '{}' });
              }
              if (request.method() !== "GET") {
                writes.push(request.method() + " " + url.pathname);
                return route.abort();
              }
              return route.fulfill({ status: url.pathname === "/api/me" ? 401 : 200,
                contentType: "application/json", body: url.pathname === "/api/me" ?
                  '{"error":"fixture: signed out"}' : url.pathname === "/api/config" ? '{"allow_signup":true}' : '[]' });
            }
            return route.continue();
          });
          await page.goto(new URL("/login", origin).href, { waitUntil: "networkidle" });
          const email = page.locator('input[type="email"]');
          await email.waitFor({ state: "visible" });
          const measure = () => email.evaluate(element => {
            const style = getComputedStyle(element);
            return { width: element.getBoundingClientRect().width, height: element.getBoundingClientRect().height,
              borderStyle: style.borderTopStyle, borderWidth: style.borderTopWidth, borderColor: style.borderTopColor,
              fontSize: style.fontSize, padding: style.padding, collides: element.matches(".border-input") };
          });
          const before = await measure();
          assert(before.width > 250 && before.height >= 30, JSON.stringify(before));
          assert.equal(before.borderStyle, "solid");
          assert.equal(before.borderWidth, "1px");
          assert.equal(before.collides, false);
          await page.addStyleTag({ content: foreignStyle });
          assert.deepEqual(await measure(), before, "Foreign control CSS must not shrink or restyle the email field");
          const robotLink = page.locator('a[href^="infoflow://"]');
          await robotLink.waitFor({ state: "visible" });
          assert((await page.locator("body").innerText()).includes("baidu"), "Missing company email guidance");
          await email.fill("fixture@baidu.com");
          await page.locator('button[type="submit"]').click();
          await page.locator('[data-slot="input-otp-slot"]').first().waitFor({ state: "visible" });
          assert.equal(await page.locator('[data-slot="input-otp-slot"]').count(), 6);
          await page.locator('a[href^="infoflow://"]').waitFor({ state: "visible" });
          const slots = await page.locator('[data-slot="input-otp-slot"]').evaluateAll(elements => elements.map(element => ({
            width: element.getBoundingClientRect().width, height: element.getBoundingClientRect().height,
            collides: element.matches(".border-input"),
          })));
          assert(slots.every(slot => slot.width >= 30 && slot.height >= 30 && !slot.collides), JSON.stringify(slots));
          assert.deepEqual(errors, []);
          assert.deepEqual(writes, []);
          console.log(JSON.stringify({ colorScheme, viewportWidth: width, foreignStylePreserved: true, loginAndCodeGuidancePresent: true, before, slots }));
        } finally { await context.close(); }
      }
    }
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
