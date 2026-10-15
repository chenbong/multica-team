import { inlineMarkdown } from "./inline-markdown.js";

// Presentation only: internal member/agent mentions are not InfoFlow URLs.
// Run at the transport boundary, after canonical content was checkpointed.
// Keep code examples opaque and leave ordinary links byte-for-byte unchanged.
export function infoflowMarkdown(content) {
  const text = String(content);
  let output = "", cursor = 0, fence = null;
  for (const match of text.matchAll(/^ {0,3}(`{3,}|~{3,})([^\r\n]*)(?:\r?\n|$)/gm)) {
    const marker = match[1];
    if (!fence) {
      if (marker[0] === "`" && match[2].includes("`")) continue;
      output += inlineMentions(text.slice(cursor, match.index));
      cursor = match.index;
      fence = marker;
    } else if (marker[0] === fence[0] && marker.length >= fence.length && !match[2].trim()) {
      const end = match.index + match[0].length;
      output += text.slice(cursor, end);
      cursor = end;
      fence = null;
    }
  }
  return output + (fence ? text.slice(cursor) : inlineMentions(text.slice(cursor)));
}

function inlineMentions(text) {
  // Find matching backtick runs once, rather than rescanning the suffix for
  // each unmatched delimiter. Escaped backticks cannot open/close code spans.
  const closing = new Map(), next = new Map();
  const runs = [...text.matchAll(/`+/g)].filter(match => {
    let start = match.index;
    while (start > 0 && text[start - 1] === "\\") start--;
    return (match.index - start) % 2 === 0;
  });
  for (let index = runs.length - 1; index >= 0; index--) {
    const run = runs[index], length = run[0].length;
    if (next.has(length)) closing.set(run.index, next.get(length) + length);
    next.set(length, run.index);
  }
  let codeEnd = 0;
  return text.replace(/`+|\\[^\r\n]|!?\[(@(?:\\[^\r\n]|[^\\[\]\r\n])+)\]\(mention:\/\/(?:member|agent|squad|all)\/[^\s()]+\)/g,
    (token, label, offset) => {
      if (offset < codeEnd) return token;
      if (token.startsWith("`")) {
        codeEnd = closing.get(offset) || 0;
        return token;
      }
      if (!label || token.startsWith("!")) return token;
      const name = label.replace(/\\([\[\]\\()])/g, "$1");
      // Escaping cannot introduce a fresh link or formatting from a label, nor
      // enlarge a message beyond the send budget already applied by callers.
      return inlineMarkdown(name, Buffer.byteLength(token));
    });
}
