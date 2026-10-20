import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it, vi } from "vitest";
import { render } from "@testing-library/react";
import { ReadonlyContent } from "../editor/readonly-content";
import { RichContent } from "./rich-content";
import katex from "katex";

vi.mock("../editor/link-hover-card", () => ({ useLinkHover: () => ({}), LinkHoverCard: () => null }));

const samples = readFileSync(resolve(process.cwd(), "rich-content/latex-samples.md"), "utf8");
const sections = samples.split(/^## /m).slice(1);
const expectedCounts = [4, 2, 1, 1, 1, 3, 2, 5, 11, 2, 3, 4, 3, 3, 1, 7, 5, 5];

describe("user-provided LaTeX compatibility samples", () => {
  it.each(sections.map((content, index) => ({ content, section: index + 1, count: expectedCounts[index] })))(
    "renders section $section with its expected formulas", ({ content, section, count }) => {
      const { container } = render(<ReadonlyContent content={`## ${content}`} />);
      expect(container.querySelectorAll(".katex")).toHaveLength(count!);
      expect(container.querySelectorAll(".katex-error")).toHaveLength(0);
      // KaTeX can emit red unknown commands inside a .katex span on fallback;
      // class presence alone is not proof that the expression was understood.
      for (const annotation of container.querySelectorAll('annotation[encoding="application/x-tex"]')) {
        expect(() => katex.renderToString(annotation.textContent ?? "", {
          displayMode: annotation.closest(".katex-display") != null,
          throwOnError: true, strict: "ignore", trust: false,
        })).not.toThrow();
      }
      if (section === 17) expect(container.querySelectorAll("table .katex")).toHaveLength(5);
      if (section === 18) {
        expect(container.textContent).toContain("售价 $100，另一件 $200");
        expect(container.querySelector("li em, li strong")).toBeNull();
      }
    });

  it.each(["compact", "document"] as const)("renders single-dollar formulas while streaming in %s mode", (density) => {
    const view = render(<RichContent density={density} phase="streaming" content={"结果 $x^2"} />);
    expect(view.container.querySelector(".katex")).toBeNull();
    view.rerender(<RichContent density={density} phase="streaming" content={"结果 $x^2$"} />);
    expect(view.container.querySelectorAll(".katex")).toHaveLength(1);
    expect(view.container.querySelector(".katex-display")).toBeNull();
  });
});
