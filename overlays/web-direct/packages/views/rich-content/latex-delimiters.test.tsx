import { describe, expect, it, vi } from "vitest";
import { render } from "@testing-library/react";
import { ReadonlyContent } from "../editor/readonly-content";
import { RichContent } from "./rich-content";

vi.mock("../editor/link-hover-card", () => ({ useLinkHover: () => ({}), LinkHoverCard: () => null }));

const EQUATION = String.raw`\mathrm{BO}(h,e)=\mathrm{RMSNorm}(h)+\alpha e`;

describe("LaTeX delimiter rendering in task replies", () => {
  it("renders the screenshot's bracket-delimited equation as display math", () => {
    const { container } = render(<ReadonlyContent content={`Prelude 先得到输入表示。\n\n\\[\n${EQUATION}\n\\]`} />);
    expect(container.querySelector(".katex-display")).not.toBeNull();
    expect(container.querySelector('annotation[encoding="application/x-tex"]')?.textContent?.trim()).toBe(EQUATION);
  });

  it("renders parenthesis-delimited inline math without losing surrounding Markdown", () => {
    const { container } = render(<ReadonlyContent content={String.raw`**说明**：行内 \(x_i^2 + \alpha\)，保留 [链接](https://example.test/docs)。`} />);
    expect(container.querySelector(".katex")).not.toBeNull();
    expect(container.querySelector(".katex-display")).toBeNull();
    expect(container.querySelector("strong")).toHaveTextContent("说明");
    expect(container.querySelector('a[href="https://example.test/docs"]')).not.toBeNull();
  });

  it("renders single-line display delimiters as a display block", () => {
    const { container } = render(<ReadonlyContent content={String.raw`前文 \[a^2+b^2=c^2\] 后文`} />);
    expect(container.querySelector(".katex-display")).not.toBeNull();
    expect(container.textContent).toContain("前文");
    expect(container.textContent).toContain("后文");
  });

  it("keeps display math in block quotes and list items", () => {
    const { container } = render(<ReadonlyContent content={"> \\[\n> x^2\n> \\]\n\n- 公式：\n  \\[\n  y^2\n  \\]"} />);
    expect(container.querySelector("blockquote .katex-display")).not.toBeNull();
    expect(container.querySelector("li .katex-display")).not.toBeNull();
  });

  it.each(["compact", "document"] as const)("also renders complete math in the shared %s streaming renderer", (density) => {
    const view = render(<RichContent density={density} phase="streaming" content={String.raw`结果 \(x^2`} />);
    expect(view.container.querySelector(".katex")).toBeNull();
    view.rerender(<RichContent density={density} phase="streaming" content={String.raw`结果 \(x^2\)`} />);
    expect(view.container.querySelectorAll(".katex")).toHaveLength(1);
  });

  it("leaves fenced and inline code examples as source", () => {
    const source = "```latex\n\\[x^2\\]\n```\n\n``\\(y\\)``\n\n~~~text\n\\[z\\]\n~~~\n\n    \\(literal\\)";
    const { container } = render(<ReadonlyContent content={source} />);
    expect(container.querySelector(".katex")).toBeNull();
    expect(container.textContent).toContain(String.raw`\[x^2\]`);
    expect(container.textContent).toContain(String.raw`\(y\)`);
  });

  it("does not reinterpret ordinary brackets or dollar amounts", () => {
    const { container } = render(<ReadonlyContent content={"[不是公式]\nRevenue $120/mo gross (~$85 net)"} />);
    expect(container.querySelector(".katex")).toBeNull();
    expect(container.textContent).toContain("$120/mo gross (~$85 net)");
  });

  it("retains the existing double-dollar syntax", () => {
    const { container } = render(<ReadonlyContent content={"$$x^2$$\n\n$$\ny^2\n$$"} />);
    expect(container.querySelectorAll(".katex")).toHaveLength(2);
    expect(container.querySelectorAll(".katex-display")).toHaveLength(1);
  });

  it("keeps issue keys and URLs inside TeX out of Markdown autolinking", () => {
    const expression = String.raw`\text{ICLR-3 https://example.test}`;
    const { container } = render(<ReadonlyContent content={`\\[\n${expression}\n\\]`} />);
    expect(container.querySelector(".katex-display")).not.toBeNull();
    expect(container.querySelector("annotation")?.textContent?.trim()).toBe(expression);
    expect(container.querySelector("a")).toBeNull();
  });

  it("does not enable unsafe KaTeX links or HTML", () => {
    const { container } = render(<ReadonlyContent content={String.raw`\[\href{javascript:alert(1)}{x}\]`} />);
    expect(container.querySelector(".katex-display")).not.toBeNull();
    expect(container.querySelector('a[href^="javascript:"], script, [onerror]')).toBeNull();
  });

  it("keeps following Markdown readable when a formula is invalid", () => {
    const { container } = render(<ReadonlyContent content={"\\[\n\\notARealMathCommand{\n\\]\n\n**后续内容**"} />);
    expect(container.querySelector(".katex-error")).not.toBeNull();
    expect(container.querySelector("strong")).toHaveTextContent("后续内容");
  });
});
