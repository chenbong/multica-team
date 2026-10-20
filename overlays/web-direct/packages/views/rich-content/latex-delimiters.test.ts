// @vitest-environment node
import { describe, expect, it } from "vitest";
import { normalizeLatexDelimiters } from "./latex-delimiters";

describe("read-only LaTeX delimiter normalization", () => {
  it("accepts block and inline delimiters without modifying their TeX", () => {
    expect(normalizeLatexDelimiters(String.raw`inline \(x_i+\alpha\)`)).toBe(String.raw`inline $$x_i+\alpha$$`);
    expect(normalizeLatexDelimiters("\\[\n\\frac{a}{b}\n\\]")).toBe("$$\n\\frac{a}{b}\n$$");
    expect(normalizeLatexDelimiters(String.raw`\[x^2\]`)).toBe("$$\nx^2\n$$");
  });
  it.each([
    [String.raw`质能 $E = mc^2$`, String.raw`质能 $$E = mc^2$$`],
    [String.raw`设$x>0$，则$\sqrt{x}$有意义`, String.raw`设$$x>0$$，则$$\sqrt{x}$$有意义`],
    [String.raw`$a_1 + a_2$ 和 $a * b * c$`, String.raw`$$a_1 + a_2$$ 和 $$a * b * c$$`],
    [String.raw`$\text{ICLR-3 https://example.test}$`, String.raw`$$\text{ICLR-3 https://example.test}$$`],
    [String.raw`$\text{\(literal\)}$`, String.raw`$$\text{\(literal\)}$$`],
  ])("normalizes a complete single-dollar expression: %s", (source, expected) => {
    expect(normalizeLatexDelimiters(source)).toBe(expected);
  });
  it.each([
    String.raw`售价 \$100，另一件 \$200`, "Revenue $120/mo gross (~$85 net)", "$100~$120",
    "$100 and $200", "$ x$", "$x $", "$x\ny$", "$unfinished", "$x\n\n$y",
    "`$x$`", "```latex\n$x$\n```", "    $x$", String.raw`<code>$x$</code>`,
    String.raw`[$x$](https://example.test/$y$)`, String.raw`<span title="$x$">literal</span>`,
  ])("preserves currency, incomplete input and literal single dollars: %s", (source) => {
    expect(normalizeLatexDelimiters(source)).toBe(source);
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
