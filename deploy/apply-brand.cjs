const fs = require("node:fs");
const path = require("node:path");
const assert = require("node:assert/strict");

assert(process.argv[2], "usage: node deploy/apply-brand.cjs <staging-root> [--check]");
const root = fs.realpathSync(process.argv[2]);
assert(!fs.existsSync(path.join(root, ".git")), "Use the separate frontend staging copy");
assert(fs.existsSync(path.join(root, "apps/web/package.json")), "Invalid frontend staging root");
// Both overrides are opt-in. Unset values preserve the staged configuration.
const display = process.env.MULTICA_BRAND_NAME?.trim();
const bridgeUrl = process.env.MULTICA_BRAND_BRIDGE_URL?.trim();
const planned = new Map();

function replaceOnce(text, pattern, replacement, label) {
  const matches = [...text.matchAll(new RegExp(pattern.source, "g"))];
  assert.equal(matches.length, 1, `Upstream changed: ${label} must match exactly once`);
  return text.replace(pattern, typeof replacement === "function" ? replacement : () => replacement);
}

function edit(rel, transform) {
  const source = fs.readFileSync(path.join(root, rel), "utf8");
  planned.set(rel, transform(source));
}

if (display) {
  const titleSource = fs.readFileSync(path.join(root, "apps/web/platform/document-title.ts"), "utf8");
  const suffixMatch = titleSource.match(/export const TITLE_SUFFIX = ("(?:\\.|[^"\\])*");/);
  assert(suffixMatch, "Upstream changed: missing title suffix");
  const previousSuffix = JSON.parse(suffixMatch[1]);
  const nextSuffix = " | " + display;
  // Keep expectations valid inside both quoted strings and template literals.
  const escapeTestString = (value) => JSON.stringify(value).slice(1, -1)
    .replace(/`/g, "\\`").replace(/\$\{/g, "\\${");

  for (const locale of ["zh-Hans", "en"]) {
    edit(`packages/views/locales/${locale}/auth.json`, (source) => {
      const json = JSON.parse(source);
      assert.equal(typeof json.signin?.title, "string", "Missing sign-in title");
      json.signin.title = display;
      return JSON.stringify(json, null, 2) + "\n";
    });
  }
  edit("apps/web/platform/document-title.ts", (source) => {
    source = replaceOnce(source, /export const SITE_TITLE = "(?:\\.|[^"\\])*";/,
      `export const SITE_TITLE = ${JSON.stringify(display)};`, "site title");
    return replaceOnce(source, /export const TITLE_SUFFIX = "(?:\\.|[^"\\])*";/,
      `export const TITLE_SUFFIX = ${JSON.stringify(nextSuffix)};`, "title suffix");
  });
  edit("apps/web/platform/document-title.test.tsx", (source) => {
    const previous = escapeTestString(previousSuffix);
    assert(source.includes(previous), "Upstream title tests changed");
    return source.split(previous).join(escapeTestString(nextSuffix));
  });
  edit("apps/web/app/manifest.ts", (source) => {
    source = replaceOnce(source, /    name: "(?:\\.|[^"\\])*",/,
      `    name: ${JSON.stringify(display)},`, "manifest name");
    return replaceOnce(source, /    short_name: "(?:\\.|[^"\\])*",/,
      `    short_name: ${JSON.stringify(display)},`, "manifest short name");
  });
  edit("apps/web/app/layout.tsx", (source) => {
    source = replaceOnce(source, /(appleWebApp:\s*{\s*capable: true,\s*title: )"(?:\\.|[^"\\])*",/,
      (_match, prefix) => `${prefix}${JSON.stringify(display)},`, "Apple web app title");
    return replaceOnce(source, /    siteName: "(?:\\.|[^"\\])*",/,
      `    siteName: ${JSON.stringify(display)},`, "Open Graph site name");
  });
}
if (bridgeUrl) {
  edit("packages/views/agents/components/agents-page.tsx", (source) => {
    // An unconfigured public build has no InfoFlow entry point to rebrand.
    if (!source.includes("const INFOFLOW_BRIDGE_URL =")) return source;
    return replaceOnce(source, /const INFOFLOW_BRIDGE_URL = "(?:\\.|[^"\\])*";/,
      `const INFOFLOW_BRIDGE_URL = ${JSON.stringify(bridgeUrl)};`, "InfoFlow link");
  });
}

// Validate the complete plan first: anchor drift must not leave mixed branding.
if (process.argv[3] !== "--check") {
  assert(!process.argv[3], "Unknown branding option");
  for (const [rel, content] of planned) fs.writeFileSync(path.join(root, rel), content);
}
console.log(`${process.argv[3] === "--check" ? "Validated" : "Applied"} configured branding (${planned.size} files).`);
