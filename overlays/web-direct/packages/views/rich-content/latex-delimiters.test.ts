// @vitest-environment node
import { describe, expect, it } from "vitest";
import { normalizeLatexDelimiters } from "./latex-delimiters";

describe("read-only LaTeX delimiter normalization", () => {
  it("accepts block and inline delimiters without modifying their TeX", () => {
    expect(normalizeLatexDelimiters(String.raw`inline \(x_i+\alpha\)`)).toBe(String.raw`inline $$x_i+\alpha$$`);
    expect(normalizeLatexDelimiters("\\[\n\\frac{a}{b}\n\\]")).toBe("$$\n\\frac{a}{b}\n$$");
    expect(normalizeLatexDelimiters(String.raw`\[x^2\]`)).toBe("$$\nx^2\n$$");
  });
  it("preserves quote and list continuation prefixes", () => {
    expect(normalizeLatexDelimiters("> \\[\n> x^2\n> \\]")).toBe("> $$\n> x^2\n> $$");
    expect(normalizeLatexDelimiters(String.raw`- \[x^2\]`)).toBe("- $$\n  x^2\n  $$");
    expect(normalizeLatexDelimiters(String.raw`> \[x^2\]`)).toBe("> $$\n> x^2\n> $$");
  });
  it.each([
    "```latex\n\\[x\\]\n```", "````latex\n```\n\\[x\\]\n````", "~~~latex\n\\(x\\)\n~~~", "```latex\n\\[x\\]",
    "    \\[x\\]", "\t\\(x\\)", "`\\(x\\)`", "``\\[x`y\\]``", "`first\n\\[x\\]`",
    String.raw`[link](https://example.test/\(x\))`, String.raw`[\(label\)](https://example.test/)`,
    String.raw`<code>\(literal\)</code>`, String.raw`<code>\(unfinished\)`, String.raw`<span title="\(literal\)">value</span>`,
    String.raw`$$\text{\(literal\)}$$`, String.raw`$$\text{cost \$$} \(literal\)$$`, "$$\n\\[literal\\]\n$$", "$$\n\\(unfinished dollar math\\)",
    String.raw`\\[literal\\]`, String.raw`\\(literal\\)`, String.raw`\[unfinished`, String.raw`\(unfinished`,
    "普通 [括号] 和价格 $120、$85", "\\(one\n\ntwo\\)",
  ])("leaves literal or incomplete source unchanged: %s", (source) => {
    expect(normalizeLatexDelimiters(source)).toBe(source);
  });
  it("does not consume code between an opening and closing delimiter", () => {
    const source = "\\[\n\n```\nx\n```\n\\]";
    expect(normalizeLatexDelimiters(source)).toBe(source);
  });
  it("handles multiple formulas and preserves CRLF display lines", () => {
    expect(normalizeLatexDelimiters("\\[\r\nx^2\r\n\\]\r\ninline \\(y\\)")).toBe("$$\r\nx^2\r\n$$\r\ninline $$y$$");
  });
});
