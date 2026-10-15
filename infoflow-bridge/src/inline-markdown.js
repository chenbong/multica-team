// Display untrusted names/titles on one line without creating Markdown links,
// mentions, headings or broken escape sequences after byte-budget clipping.
export function inlineMarkdown(value, maxBytes = 180) {
  const text = String(value ?? "").replace(/[\x00-\x1f\x7f]/g, " ").replace(/[\u200b-\u200f\u202a-\u202e]/g, "").replace(/\s+/g, " ").trim();
  const units = [...text].map(char => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;" })[char] || (/^[\\`*_{}\[\]()!|~]$/.test(char) ? "\\" + char : char));
  if (Buffer.byteLength(units.join("")) <= maxBytes) return units.join("");
  let result = "";
  for (const unit of units) {
    if (Buffer.byteLength(result + unit) > maxBytes - 3) break;
    result += unit;
  }
  return result + "...";
}
