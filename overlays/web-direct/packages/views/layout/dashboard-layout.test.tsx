import type { ReactNode } from "react";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

const survey = vi.hoisted(() => vi.fn(() => <div data-testid="source-survey" />));
vi.mock("../onboarding", () => ({ SourceBackfillModal: survey }));
vi.mock("@multica/ui/components/ui/sidebar", () => ({
  SidebarProvider: ({ children }: { children: ReactNode }) => <>{children}</>,
  SidebarInset: ({ children }: { children: ReactNode }) => <>{children}</>,
}));
vi.mock("./dashboard-guard", () => ({
  DashboardGuard: ({ children }: { children: ReactNode }) => <>{children}</>,
}));
vi.mock("./app-sidebar", () => ({ AppSidebar: () => <aside data-testid="sidebar" /> }));
vi.mock("../modals/registry", () => ({ ModalRegistry: () => <div data-testid="modal-registry" /> }));
vi.mock("./navigation-progress", () => ({ NavigationProgress: () => null }));
vi.mock("./workspace-presence-prefetch", () => ({ WorkspacePresencePrefetch: () => null }));
vi.mock("./global-shortcuts", () => ({ GlobalShortcuts: () => null }));
vi.mock("../web-panels/panel-shell", () => ({
  ResourceWebPanelShellProvider: ({ children }: { children: ReactNode }) => <>{children}</>,
  ResourceWebPanelLayout: ({ children }: { children: ReactNode }) => <div data-testid="web-panel-layout">{children}</div>,
}));

import { DashboardLayout } from "./dashboard-layout";

describe("dashboard without source attribution prompt", () => {
  it("does not mount the survey while preserving content and other modals", () => {
    const { rerender } = render(<DashboardLayout><main>Tasks</main></DashboardLayout>);
    expect(screen.getByText("Tasks")).toBeInTheDocument();
    expect(screen.getByTestId("sidebar")).toBeInTheDocument();
    expect(screen.getByTestId("web-panel-layout")).toBeInTheDocument();
    expect(screen.getByTestId("modal-registry")).toBeInTheDocument();
    expect(screen.queryByTestId("source-survey")).toBeNull();
    expect(survey).not.toHaveBeenCalled();
    rerender(<DashboardLayout extra={<div>Extra content</div>}><main>Chat</main></DashboardLayout>);
    expect(screen.getByText("Chat")).toBeInTheDocument();
    expect(screen.getByText("Extra content")).toBeInTheDocument();
    expect(survey).not.toHaveBeenCalled();
  });
});
