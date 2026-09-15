// Offline stand-in for the Google Fonts API, used only to measure build time on a
// machine that cannot reach fonts.googleapis.com. Next reads this through
// NEXT_FONT_GOOGLE_MOCKED_RESPONSES and treats a non-absolute font URL as the file
// contents itself, so the emitted font files are placeholders, not real glyphs.
const css = (url) => {
  const family = decodeURIComponent(String(url)).replace(/^.*family=/, "").split(/[:&]/)[0].replace(/\+/g, " ");
  return `/* latin */
@font-face {
  font-family: '${family}';
  font-style: normal;
  font-weight: 400;
  font-display: swap;
  src: url(https://fonts.gstatic.com/s/offline-placeholder.woff2) format('woff2');
  unicode-range: U+0000-00FF;
}`;
};

module.exports = new Proxy({}, { get: (_target, url) => (typeof url === "string" ? css(url) : undefined) });
