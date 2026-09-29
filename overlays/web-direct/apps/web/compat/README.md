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
- The `:has` runtime is enabled only for the legacy branch and observes application state attributes.
- `getComputedStyle(document.documentElement).getPropertyValue('--multica-compat-mode')` reports `modern` or `legacy`.

Chrome 97 is the tested target, not a promise of support for arbitrary old browsers. CSS cannot emulate every future browser feature. Runtime-generated inline colors/transforms and container queries must be assessed individually. Full end-to-end login/chat testing and an actual iPhone check are separate from transform unit tests.

Only the Web service needs deployment; API, Bridge and client daemons do not change.
