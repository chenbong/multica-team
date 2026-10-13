import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { Script } from 'node:vm';

const html = readFileSync(new URL('../public/index.html', import.meta.url), 'utf8');
const css = html.match(/<style>([\s\S]*?)<\/style>/)[1];
const roots = [...css.matchAll(/:root\s*\{([^}]+)\}/g)].map(match =>
  Object.fromEntries([...match[1].matchAll(/(--[\w-]+):\s*(#[\da-f]{6})/g)].map(m => [m[1], m[2]])));

function luminance(hex) {
  const rgb = [1, 3, 5].map(i => parseInt(hex.slice(i, i + 2), 16) / 255)
    .map(c => c <= .04045 ? c / 12.92 : ((c + .055) / 1.055) ** 2.4);
  return rgb[0] * .2126 + rgb[1] * .7152 + rgb[2] * .0722;
}
function contrast(a, b) {
  const [light, dark] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (light + .05) / (dark + .05);
}

test('both color schemes define the same complete palette', () => {
  assert.equal(roots.length, 2);
  assert.deepEqual(Object.keys(roots[0]).sort(), Object.keys(roots[1]).sort());
  assert.match(css, /@media \(prefers-color-scheme: dark\)/);
  assert.match(css, /color-scheme: light;/);
  assert.match(css, /color-scheme: dark;/);
});

for (const [index, name] of ['light', 'dark'].entries()) {
  test(`${name} text contrast is at least 4.5:1 in every button and status state`, () => {
    const colors = roots[index];
    const pairs = [
      ['text', 'page-bg'], ['text', 'surface'], ['text', 'field-bg'],
      ['muted', 'surface'], ['muted', 'field-bg'], ['link', 'surface'],
      ['text', 'button-bg'], ['text', 'button-hover'], ['text', 'button-active'],
      ['primary-text', 'primary-bg'], ['primary-text', 'primary-hover'],
      ['disabled-text', 'disabled-bg'], ['danger-text', 'button-bg'],
      ['danger-text', 'danger-bg'], ['danger-text', 'surface'], ['warning-text', 'surface'],
      ['success-text', 'success-bg'], ['info-text', 'info-bg'], ['warning-text', 'warning-bg'],
      ['muted', 'button-bg'], ['text', 'code-bg'], ['toast-text', 'toast-bg'],
    ];
    for (const [fg, bg] of pairs) {
      const ratio = contrast(colors[`--${fg}`], colors[`--${bg}`]);
      assert.ok(ratio >= 4.5, `${name} ${fg}/${bg}: ${ratio.toFixed(2)}:1`);
    }
    for (const bg of ['field-bg', 'button-bg']) {
      assert.ok(contrast(colors['--control-border'], colors[`--${bg}`]) >= 3, `${name} ${bg} border`);
    }
  });
}

test('controls set explicit text/background and preserve interactive states', () => {
  for (const selector of ['button', 'a.action-link', 'button.primary', 'button:disabled, input:disabled, select:disabled']) {
    const rule = css.slice(css.indexOf(`${selector} {`)).split('}')[0];
    assert.match(rule, /(?:^|[;{])\s*color: var\(--[\w-]+\)/);
    assert.match(rule, /background: var\(--[\w-]+\)/);
  }
  assert.match(css, /button:focus-visible/);
  assert.match(css, /button:hover:not\(:disabled\)/);
  assert.match(css, /\.session\[hidden\] \{ display: none; \}/);
  assert.match(html, /detail\.style\.color = status\.state === "reconnecting" \? "var\(--warning-text\)" : "var\(--danger-text\)"/);
});

test('inline behavior scripts still parse after visual changes', () => {
  const scripts = [...html.matchAll(/<script[^>]*>([\s\S]*?)<\/script>/g)];
  assert.ok(scripts.length >= 2);
  for (const [, script] of scripts) {
    assert.doesNotThrow(() => new Script(script.replace('{{BRIDGE_PAGE_CONFIG}}', '{}')));
  }
});
