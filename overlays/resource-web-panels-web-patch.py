"""Checked feature wiring; all writes remain in the explicit frontend stage."""
import json
from pathlib import Path


CLIENT_METHODS = '''  private resourceWebPanelPath(resource: WebPanelResource, panelId?: string): string {
    if (resource.scope !== "issue" && resource.scope !== "workspace") throw new Error("Invalid web panel scope");
    const prefix = resource.scope === "issue" ? "issues" : "workspaces";
    const base = `/api/${prefix}/${encodeURIComponent(resource.resourceId)}/web-panels`;
    return panelId ? `${base}/${encodeURIComponent(panelId)}` : base;
  }

  async listResourceWebPanels(resource: WebPanelResource, workspaceSlug: string, signal?: AbortSignal): Promise<ResourceWebPanelList> {
    const endpoint = this.resourceWebPanelPath(resource);
    const raw = await this.fetch<unknown>(endpoint, { signal, headers: { "X-Workspace-Slug": workspaceSlug } });
    const value = parseWithFallback<ResourceWebPanelList | null>(raw, ResourceWebPanelListSchema, null, { endpoint });
    if (!value) throw new Error("Invalid web panel list response");
    return value;
  }

  async createResourceWebPanel(resource: WebPanelResource, input: WebPanelInput, workspaceSlug: string): Promise<ResourceWebPanel> {
    const endpoint = this.resourceWebPanelPath(resource);
    const raw = await this.fetch<unknown>(endpoint, { method: "POST", body: JSON.stringify(input), headers: { "X-Workspace-Slug": workspaceSlug } });
    const value = parseWithFallback<ResourceWebPanel | null>(raw, ResourceWebPanelSchema, null, { endpoint });
    if (!value) throw new Error("Invalid web panel response");
    return value;
  }

  async updateResourceWebPanel(resource: WebPanelResource, panelId: string, input: WebPanelInput, workspaceSlug: string): Promise<ResourceWebPanel> {
    const endpoint = this.resourceWebPanelPath(resource, panelId);
    const raw = await this.fetch<unknown>(endpoint, { method: "PATCH", body: JSON.stringify(input), headers: { "X-Workspace-Slug": workspaceSlug } });
    const value = parseWithFallback<ResourceWebPanel | null>(raw, ResourceWebPanelSchema, null, { endpoint });
    if (!value) throw new Error("Invalid web panel response");
    return value;
  }

  async deleteResourceWebPanel(resource: WebPanelResource, panelId: string, workspaceSlug: string): Promise<void> {
    await this.fetch(this.resourceWebPanelPath(resource, panelId), { method: "DELETE", headers: { "X-Workspace-Slug": workspaceSlug } });
  }

'''


def extend(changes):
    def add(path, before, after):
        changes.setdefault(path, []).append((before, after))

    add("packages/core/package.json", '    "./api": "./api/index.ts",\n',
        '    "./web-panels": "./web-panels/index.ts",\n    "./api": "./api/index.ts",\n')
    add("packages/core/api/client.ts", 'import type { ZodType } from "zod";\n',
        'import type { ZodType } from "zod";\n'
        'import type { ResourceWebPanel, ResourceWebPanelList, WebPanelInput, WebPanelResource } from "../web-panels/types";\n'
        'import { ResourceWebPanelSchema, ResourceWebPanelListSchema } from "./web-panel-schema";\n')
    anchor = '  async getIssue(id: string, options?: { signal?: AbortSignal }): Promise<Issue> {\n'
    add("packages/core/api/client.ts", anchor, CLIENT_METHODS + anchor)
    add("packages/core/types/events.ts", '  | "issue_metadata:changed"\n',
        '  | "issue_metadata:changed"\n  | "resource_web_panel:changed"\n')
    # Panel width keys also contain an account component. Keep them separate
    # from the legacy `${base}:${slug}` keys when clearing one workspace.
    add("packages/core/platform/storage-cleanup.ts", '  clearRegisteredWorkspaceDrafts(adapter, slug);\n',
        '  for (const key of adapter.keys?.() ?? []) {\n'
        '    if (key.startsWith("multica_resource_web_panels:") && key.endsWith(`:${encodeURIComponent(slug)}`)) adapter.removeItem(key);\n'
        '  }\n  clearRegisteredWorkspaceDrafts(adapter, slug);\n')
    add("packages/core/platform/storage-cleanup.ts", '  const prefixes = [...WORKSPACE_SCOPED_KEYS, ...drafts.workspaceScoped].map(\n',
        '  const prefixes = ["multica_resource_web_panels", ...WORKSPACE_SCOPED_KEYS, ...drafts.workspaceScoped].map(\n')

    dashboard = "packages/views/layout/dashboard-layout.tsx"
    add(dashboard, 'import { GlobalShortcuts } from "./global-shortcuts";\n',
        'import { GlobalShortcuts } from "./global-shortcuts";\n'
        'import { ResourceWebPanelShellProvider, ResourceWebPanelLayout } from "../web-panels/panel-shell";\n')
    add(dashboard, '      <SidebarProvider className="h-svh bg-app-shell">\n',
        '      <ResourceWebPanelShellProvider>\n      <SidebarProvider className="h-svh bg-app-shell">\n')
    add(dashboard, '      </SidebarProvider>\n', '      </SidebarProvider>\n      </ResourceWebPanelShellProvider>\n')
    add(dashboard, '        <SidebarInset className="relative overflow-hidden">\n',
        '        <SidebarInset className="relative overflow-hidden">\n          <ResourceWebPanelLayout>\n')
    add(dashboard, '        </SidebarInset>\n', '          </ResourceWebPanelLayout>\n        </SidebarInset>\n')

    sidebar = "packages/views/layout/app-sidebar.tsx"
    add(sidebar, 'import { AppLink, useNavigation } from "../navigation";\n',
        'import { AppLink, useNavigation } from "../navigation";\n'
        'import { WorkspaceWebPanelsNav } from "../web-panels/panel-list";\n')
    add(sidebar, '          </SidebarGroup>\n        </SidebarContent>\n',
        '          </SidebarGroup>\n          <WorkspaceWebPanelsNav />\n        </SidebarContent>\n')

    issue = "packages/views/issues/components/issue-detail.tsx"
    add(issue, 'import { PluginPanelSection } from "../../plugins";\n',
        'import { PluginPanelSection } from "../../plugins";\n'
        'import { useResourceWebPanelIssueScope, useResourceWebPanelOpen, useResourceWebPanelContext } from "@multica/core/web-panels";\n'
        'import { useWebPanelInspector } from "../../web-panels/use-web-panel-inspector";\n'
        'import { IssueWebPanelsSection } from "../../web-panels/panel-list";\n')
    add(issue, '  const isPeek = variant === "peek";\n',
        '  const isPeek = variant === "peek";\n'
        '  const webPanelOpen = useResourceWebPanelOpen();\n'
        '  const webPanelContext = useResourceWebPanelContext();\n')
    anchor = '  const { data: issueAttachments } = useQuery(issueAttachmentsOptions(id));\n'
    add(issue, anchor, '  useResourceWebPanelIssueScope(issue?.id ?? null);\n' + anchor)
    add(issue, '  const [mobileSidebarOpen, setMobileSidebarOpen] = useState(false);\n',
        '  const webPanelInspector = useWebPanelInspector({ panelRef: sidebarRef, disabled: isMobile || isPeek, initiallyOpen: desktopSidebarInitialOpen, onLayoutChanged, onResize: handleDesktopSidebarResize });\n'
        '  const [mobileSidebarOpen, setMobileSidebarOpen] = useState(false);\n')
    add(issue, '      <PluginPanelSection issueId={issue.id} />\n',
        '      <PluginPanelSection issueId={issue.id} />\n      <IssueWebPanelsSection issueId={issue.id} />\n')
    add(issue, '  const handleToggleSidebar = useCallback(() => {\n',
        '  const handleToggleSidebar = useCallback(() => {\n'
        '    if (webPanelOpen) { webPanelContext?.store.getState().close(); return; }\n')
    add(issue, '  }, [beginDesktopSidebarToggle, isMobile, sidebarRef]);\n',
        '  }, [beginDesktopSidebarToggle, isMobile, sidebarRef, webPanelOpen, webPanelContext]);\n')
    add(issue, '      <ResizableHandle />\n      <ResizablePanel\n        id="sidebar"\n',
        '      <ResizableHandle className={webPanelOpen ? "hidden" : undefined} />\n      <ResizablePanel\n        id="sidebar"\n        className={webPanelOpen ? "hidden" : undefined}\n')
    # Panel className styles its content, not the sizing wrapper. Hide the
    # direct wrapper too, without remounting the task or its editor drafts.
    add(issue, '<ResizablePanelGroup orientation="horizontal" className="flex-1 min-h-0" defaultLayout={defaultLayout} onLayoutChanged={onLayoutChanged}>',
        '<ResizablePanelGroup orientation="horizontal" className={webPanelOpen ? "flex-1 min-h-0 [&>[data-panel][id=sidebar]]:hidden!" : "flex-1 min-h-0"} defaultLayout={defaultLayout} onLayoutChanged={webPanelInspector.onLayoutChanged}>')
    add(issue, '        onResize={handleDesktopSidebarResize}\n', '        onResize={webPanelInspector.onResize}\n')
    add(issue, '        <Sheet open={mobileSidebarOpen} onOpenChange={setMobileSidebarOpen}>\n',
        '        <Sheet open={mobileSidebarOpen && !webPanelOpen} onOpenChange={setMobileSidebarOpen}>\n')

    labels = json.loads((Path(__file__).with_name("resource-web-panels-labels.json")).read_text())
    expected = set(labels["en"])
    for locale, values in labels.items():
        if set(values) != expected:
            raise SystemExit(f"web panel labels differ for {locale}")
        add(f"packages/views/locales/{locale}/layout.json", '  "sidebar": {\n',
            '  "sidebar": {\n    "web_panels": ' + json.dumps(values, ensure_ascii=False) + ',\n')
