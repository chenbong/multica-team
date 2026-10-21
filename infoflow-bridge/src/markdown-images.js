// Only rendered Markdown images are rewritten; code and escaped syntax stay opaque.
export function markdownImages(text) {
  const found = [];
  let fence = null;
  for (let i = 0; i < text.length;) {
    if (i === 0 || text[i - 1] === "\n") {
      const lineEnd = text.indexOf("\n", i);
      const end = lineEnd < 0 ? text.length : lineEnd;
      const match = text.slice(i, end).match(/^ {0,3}(`{3,}|~{3,})(.*)$/);
      if (match && (!fence || (match[1][0] === fence[0] && match[1].length >= fence.length && !match[2].trim()))) {
        fence = fence ? null : match[1]; i = end + 1; continue;
      }
      if (fence) { i = end + 1; continue; }
    }
    if (text[i] === "\\") { i += 2; continue; }
    if (text[i] === "`") {
      let n = 1;
      while (text[i + n] === "`") n++;
      let closing = text.indexOf("`".repeat(n), i + n);
      while (closing >= 0 && (text[closing - 1] === "`" || text[closing + n] === "`")) closing = text.indexOf("`".repeat(n), closing + n);
      i = closing < 0 ? i + n : closing + n; continue;
    }
    if (text[i] !== "!" || text[i + 1] !== "[") { i++; continue; }
    let j = i + 2, depth = 1;
    for (; j < text.length && depth; j++) {
      if (text[j] === "\\") { j++; continue; }
      if (text[j] === "\n") break;
      if (text[j] === "[") depth++;
      if (text[j] === "]") depth--;
    }
    if (depth || text[j] !== "(") { i++; continue; }
    const alt = text.slice(i + 2, j - 1), start = ++j;
    let targetEnd, end;
    if (text[j] === "<") {
      targetEnd = text.indexOf(">", j + 1);
      if (targetEnd < 0) { i++; continue; }
      j = targetEnd + 1;
    } else {
      depth = 0;
      for (; j < text.length; j++) {
        if (text[j] === "\\") { j++; continue; }
        if (text[j] === "(") depth++;
        else if (text[j] === ")") { if (!depth) break; depth--; }
        else if (/\s/.test(text[j]) && !depth) break;
      }
      targetEnd = j;
    }
    while (text[j] === " " || text[j] === "\t") j++;
    if (["\"", "'"].includes(text[j])) {
      const quote = text[j++];
      while (j < text.length && text[j] !== quote && text[j] !== "\n") { if (text[j] === "\\") j++; j++; }
      if (text[j] !== quote) { i++; continue; }
      j++; while (text[j] === " " || text[j] === "\t") j++;
    }
    if (text[j] !== ")") { i++; continue; }
    end = j + 1;
    const raw = text.slice(start, targetEnd + (text[start] === "<" ? 1 : 0));
    const url = (raw.startsWith("<") ? raw.slice(1, -1) : raw).replace(/\\([()\\])/g, "$1");
    found.push({ start: i, end, alt, url, text: text.slice(i, end) }); i = end;
  }
  return found;
}

export function replaceMarkdownImages(text, replace) {
  let out = "", cursor = 0;
  for (const image of markdownImages(text)) { out += text.slice(cursor, image.start) + replace(image); cursor = image.end; }
  return out + text.slice(cursor);
}

export function imageMarkdown(label, url) {
  const safe = Array.from(String(label || "图片").replace(/[\x00-\x1f\x7f-\x9f]/g, " ")).slice(0, 160).join("").replace(/[\\[\]]/g, "\\$&");
  return `![${safe}](${url})`;
}

// Preserve complete image tokens while spending the remaining budget on text.
// An impossible image-only budget omits whole images, never partial bearer URLs.
export function trimImageMarkdown(text, budget, measure = value => value.length) {
  if (budget <= 0) return "";
  const images = markdownImages(text);
  const retained = new Set(); let imageSize = 0;
  for (const image of images) if (imageSize + measure(image.text) <= budget) { retained.add(image); imageSize += measure(image.text); }
  let remaining = budget - imageSize, out = "", cursor = 0;
  const take = value => {
    let result = "";
    for (const ch of value) { if (measure(ch) > remaining) break; result += ch; remaining -= measure(ch); }
    return result;
  };
  for (const image of images) { out += take(text.slice(cursor, image.start)); if (retained.has(image)) out += image.text; cursor = image.end; }
  return out + take(text.slice(cursor));
}
