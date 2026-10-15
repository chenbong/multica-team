import { describe, expect, it, vi } from "vitest";
import { cloneElement, type ReactElement, type ReactNode } from "react";
import { screen } from "@testing-library/react";
import { renderWithI18n } from "../test/i18n";
import { SidebarShortcutLinks } from "./sidebar-shortcut-links";

vi.mock("@multica/ui/components/ui/sidebar", () => ({
  SidebarMenuItem: ({ children }: { children: ReactNode }) => <li>{children}</li>,
  SidebarMenuButton: ({ render, children, className }: { render: ReactElement; children: ReactNode; className?: string }) =>
    cloneElement(render, { className } as object, children),
}));

describe("sidebar shortcut links", () => {
  it("shows the two configured destinations in order with the requested Chinese labels", () => {
    renderWithI18n(<ul><SidebarShortcutLinks modelTraceUrl="https://example.test/trace/" quickstartUrl="https://docs.example.test/quickstart" /></ul>, { locale: "zh-Hans" });
    const links = screen.getAllByRole("link");
    expect(links.map(a => a.textContent)).toEqual(["oneAPI 降智探测", "快速上手 & 许愿板"]);
    expect(links[0]).toHaveAttribute("href", "https://example.test/trace/");
    expect(links[1]).toHaveAttribute("href", "https://docs.example.test/quickstart");
    for (const link of links) {
      expect(link).toHaveAttribute("target", "_blank");
      expect(link).toHaveAttribute("rel", "noopener noreferrer");
    }
  });
  it.each([undefined, "", "javascript:alert(1)", "http://example.test", "https://user:secret@example.test"]) (
    "omits missing or unsafe destinations: %s", (value) => {
      renderWithI18n(<ul><SidebarShortcutLinks modelTraceUrl={value} quickstartUrl={value} /></ul>);
      expect(screen.queryByRole("link")).not.toBeInTheDocument();
    });
});
