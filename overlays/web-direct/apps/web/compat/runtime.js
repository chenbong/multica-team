import cssHasPseudo from './node_modules/css-has-pseudo/dist/browser.mjs';
import './node_modules/core-js/actual/structured-clone.js';

// Loaded by Next instrumentation-client before hydration. Core-js preserves native
// structuredClone when it is usable, and supports cycles, Map, Set and typed arrays.
const modern = typeof CSS !== 'undefined' && CSS.supports(
  '(color: oklch(0.5 0.1 120)) and (color: color-mix(in srgb, red, blue)) and (height: 100dvh) and (translate: 1px) and selector(:has(*))',
);
if (!modern) {
  const start = () => cssHasPseudo(document, {
    forcePolyfill:true,
    observedAttributes:['class','id','aria-expanded','aria-checked','aria-selected','data-state','data-active','data-separator','data-slot','data-placeholder','data-side','hidden','disabled'],
  });
  if(document.readyState === 'loading') document.addEventListener('DOMContentLoaded',start,{once:true});
  else start();
}
