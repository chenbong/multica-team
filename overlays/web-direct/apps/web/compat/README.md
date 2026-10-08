# WebView compatibility

The web app keeps one React tree and one API. Every processed stylesheet contains two mutually exclusive `@supports` branches. Capable browsers retain the original declarations/layers; older browsers use a Chrome 97-targeted conversion. This also covers lazy route CSS without waiting for JavaScript, changing the URL or sniffing user agents.

The tradeoff is additional CSS download bytes in both branches (compressible over HTTP), not a second frontend. This is intentional to avoid stylesheet replacement races and hydration differences.

## Build

Before building the Web package, run `npm ci --ignore-scripts --no-audit --no-fund` in `apps/web/compat`. The pinned package lock belongs in source control; node_modules does not. `postcss.config.mjs` runs Tailwind then the compatibility plugin. Run `node --test compat/test.cjs` from apps/web.

## Conversion and runtime

- postcss-preset-env handles cascade layer specificity, static colors, nesting and `:has` selectors.
- Property defaults are reset in the lowest layer before conversion; no global high-specificity resets.
- Legacy independent transforms are composed with ordinary transforms; viewport units fall back to vh/vw.
- Next instrumentation-client loads structuredClone support before hydration; native functionality is retained when usable. This is not JSON cloning.
- The same early entry point fills missing Array.toSorted, Array.findLast and Array.findLastIndex methods used by task details, comments, scheduling and runtime views. This is independent of CSS branch selection. Regression tests remove these methods to simulate Chrome 97, and separately verify that modern native methods remain unchanged.
- The `:has` runtime is enabled only for the legacy branch and observes application state attributes.
- `getComputedStyle(document.documentElement).getPropertyValue('--multica-compat-mode')` reports `modern` or `legacy`.

Chrome 97 is the tested target, not a promise of support for arbitrary old browsers. CSS cannot emulate every future browser feature. Runtime-generated inline colors/transforms and container queries must be assessed individually. Full end-to-end login/chat testing and an actual iPhone check are separate from transform unit tests.

Only the Web service needs deployment; API, Bridge and client daemons do not change.

## v0.6.1 upgrade checks

The compatibility entry point still runs before hydration, and the reviewed
v0.6.1 source uses the same `toSorted`, `findLast`, and `findLastIndex` APIs
covered by the bootstrap. Keep this package installed inside the frontend
staging tree; do not install it into the upstream submodule.

The new in-page find and comment-annotation features guard the CSS Custom
Highlight API. Chrome 97 keeps match counting and scroll navigation, but does
not paint the newer highlight tint. The CSS optimizer may warn about the two
`::highlight(multica-find*)` selectors; this does not fail the build. The find
hook's upstream tests exercise its no-Highlight fallback.

The local production smoke script also removes these array methods and
`structuredClone` before hydration, then checks the root/login routes at desktop
and phone widths. This uses current Chromium with missing builtins and does not
replace a real Chrome 97 or iPhone check. Container queries and subgrid remain
subject to the limitations noted above.
