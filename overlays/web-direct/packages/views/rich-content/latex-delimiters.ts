import { fromMarkdown } from "mdast-util-from-markdown";
import type { Nodes } from "mdast";

interface Range { start: number; end: number }
const LITERAL_NODES = new Set(["code", "inlineCode", "html", "link", "linkReference", "image", "imageReference", "definition"]);

function literalRanges(source: string): Range[] {
  const ranges: Range[] = [];
  function visit(node: Nodes) {
    if (LITERAL_NODES.has(node.type)) {
      const start = node.position?.start.offset;
      const end = node.position?.end.offset;
      if (start != null && end != null) ranges.push({ start, end });
      return;
    }
    if ("children" in node) node.children.forEach(visit);
  }
  visit(fromMarkdown(source));
  // CommonMark represents inline HTML tags separately from their contents.
  // Code/raw-text elements must protect their entire body, including an open
  // streaming element that has not received its closing tag yet.
  for (const match of source.matchAll(/<(code|pre|script|style|textarea)\b[^>]*>[\s\S]*?(?:<\/\1\s*>|$)/gi)) {
    ranges.push({ start: match.index, end: match.index + match[0].length });
  }
  return ranges.sort((a, b) => a.start - b.start || b.end - a.end);
}

function isEscaped(source: string, offset: number): boolean {
  let slashes = 0;
  for (let index = offset - 1; index >= 0 && source[index] === "\\"; index--) slashes++;
  return slashes % 2 === 1;
}

function continuationPrefix(before: string): string {
  const prefix = before.match(/^(?:[ \t]+|>[ \t]?|(?:[-+*]|\d{1,9}[.)])[ \t]+)*/)?.[0] ?? "";
  return prefix.replace(/(?:[-+*]|\d{1,9}[.)])[ \t]+/g, (marker) => " ".repeat(marker.length));
}

function expressionBody(body: string, prefix: string): string {
  return body.split(/\r?\n/).map((line, index) => index > 0 && prefix && line.startsWith(prefix) ? line.slice(prefix.length) : line).join("\n").trim();
}

function displayMath(source: string, start: number, close: number, body: string): string {
  const before = source.slice(source.lastIndexOf("\n", start - 1) + 1, start);
  const closingBefore = source.slice(source.lastIndexOf("\n", close - 1) + 1, close);
  const lineEnd = source.indexOf("\n", close + 2);
  const after = source.slice(close + 2, lineEnd < 0 ? source.length : lineEnd);
  const prefix = continuationPrefix(before);
  const structural = /^(?:[ \t]+|>[ \t]?|(?:[-+*]|\d{1,9}[.)])[ \t]+)*$/.test(before);
  if (structural && /^[ \t]*\r?\n/.test(body) && closingBefore.trim() === prefix.trim() && !after.trim()) {
    // Preserve existing list/quote prefixes and line endings byte-for-byte.
    return `$$${body}$$`;
  }
  const expression = expressionBody(body, prefix);
  const block = `$$\n${prefix}${expression.split("\n").join(`\n${prefix}`)}\n${prefix}$$`;
  if (structural && !after.trim()) return block;
  const separator = `\n${prefix}\n${prefix}`;
  return `${separator}${block}${separator}`;
}

/**
 * Accept $...$, AI-authored \(...\) and \[...\] before link/highlight preprocessing.
 * Convert only complete pairs in prose. Storage and editable source stay raw;
 * existing double-dollar math, code, links and HTML literals are never rewritten.
 */
export function normalizeLatexDelimiters(source: string): string {
  if (!/\\[[(]|\$/.test(source)) return source;
  const ranges = literalRanges(source);
  const tokens = /\\[[(]|\$+/g;
  const output: string[] = [];
  let copied = 0;
  let rangeIndex = 0;
  let token: RegExpExecArray | null;
  while ((token = tokens.exec(source)) !== null) {
    const start = token.index;
    while (rangeIndex < ranges.length && ranges[rangeIndex]!.end <= start) rangeIndex++;
    const range = ranges[rangeIndex];
    if (range && range.start <= start) { tokens.lastIndex = range.end; continue; }
    if (isEscaped(source, start)) continue;
    if (token[0].startsWith("$") && token[0].length > 1) {
      let closing = source.indexOf(token[0], tokens.lastIndex);
      while (closing >= 0 && isEscaped(source, closing)) closing = source.indexOf(token[0], closing + token[0].length);
      tokens.lastIndex = closing < 0 ? source.length : closing + token[0].length;
      continue;
    }
    const singleDollar = token[0] === "$";
    if (singleDollar && /\s/.test(source[start + 1] ?? "")) continue;
    const closingToken = singleDollar ? "$" : token[0] === "\\[" ? "\\]" : "\\)";
    let close = source.indexOf(closingToken, tokens.lastIndex);
    while (close >= 0 && isEscaped(source, close)) close = source.indexOf(closingToken, close + closingToken.length);
    if (close < 0 || (range && range.start < close + closingToken.length)) continue;
    const body = source.slice(start + token[0].length, close);
    // Conservative inline grammar: no multiline/edge whitespace, no closing
    // dollar followed by a digit (e.g. "$100 ... $200" or "$100~$120").
    // Literal currency can always be made unambiguous with an escaped dollar.
    if (singleDollar && (/\r|\n/.test(body) || /\s$/.test(body) || source[close + 1] === "$" || /\d/.test(source[close + 1] ?? ""))) continue;
    if (!body.trim() || (closingToken === "\\)" && /\r?\n[ \t]*\r?\n/.test(body))) continue;
    const prefix = continuationPrefix(source.slice(source.lastIndexOf("\n", start - 1) + 1, start));
    const replacement = closingToken === "\\]" ? displayMath(source, start, close, body) : `$$${expressionBody(body, prefix)}$$`;
    output.push(source.slice(copied, start), replacement);
    copied = close + closingToken.length;
    tokens.lastIndex = copied;
  }
  return output.length ? output.join("") + source.slice(copied) : source;
}
