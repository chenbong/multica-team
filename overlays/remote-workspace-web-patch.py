"""Small frontend hooks; remote workspace UI stays in web-direct."""
def extend(changes):
    def add(path, before, after):
        changes.setdefault(path, []).append((before, after))
    client='packages/core/api/client.ts'
    add(client, 'import type { ZodType } from "zod";', 'import type { ZodType } from "zod";\nimport type { WorkspaceContext, WorkspaceQuery, WorkspaceResult } from "../types/remote-workspace";')
    add(client, '  async sendChatMessage(\n', '''  async chatWorkspace(sessionId: string, query: WorkspaceQuery, signal?: AbortSignal): Promise<WorkspaceResult> {
    return this.fetch<WorkspaceResult>(`/api/chat/sessions/${encodeURIComponent(sessionId)}/workspace`, {
      method: "POST", body: JSON.stringify(query), signal,
    });
  }

  async sendChatMessage(
''')
    add(client, '    attachmentIds?: string[],\n  ): Promise<SendChatMessageResponse>', '    attachmentIds?: string[],\n    workspaceContext?: WorkspaceContext,\n  ): Promise<SendChatMessageResponse>')
    add(client, '      attachment_ids?: string[];\n    } = { content };', '      attachment_ids?: string[];\n      workspace_context?: WorkspaceContext;\n    } = { content, ...(workspaceContext ? { workspace_context: workspaceContext } : {}) };')
    add('packages/core/types/chat.ts', 'export interface SendChatMessageResponse {', 'export interface SendChatMessageResponse {\n  content?: string;')
    add('packages/core/api/schemas.ts', 'export const SendChatMessageResponseSchema: z.ZodType<SendChatMessageResponse> = z.object({', 'export const SendChatMessageResponseSchema: z.ZodType<SendChatMessageResponse> = z.object({\n  content: z.string().optional(),')
    page='packages/views/chat/chat-page.tsx'
    add(page, 'import { ChatInput } from "./components/chat-input";', 'import { ChatInput } from "./components/chat-input";\nimport { RemoteWorkspace } from "./components/remote-workspace";')
    add(page, '  const conversation = (\n    <div', '  const conversation = (\n    <RemoteWorkspace sessionId={c.activeSessionId ?? null} onOpenChange={setWorkspaceOpen} onToggleSessions={() => setWorkspaceSessionsVisible((value) => !value)} sessionsHidden={!workspaceSessionsVisible}>\n    <div')
    add(page, '    </div>\n  );\n\n  // -- Compact:', '    </div>\n    </RemoteWorkspace>\n  );\n\n  // -- Compact:')
    add(page, '  const isCompact = useIsCompact();', '  const isCompact = useIsCompact();\n  const [workspaceOpen, setWorkspaceOpen] = useState(false);\n  const [workspaceSessionsVisible, setWorkspaceSessionsVisible] = useState(false);')
    add(page, '      className="flex-1 min-h-0"\n      defaultLayout={defaultLayout}', '      className={workspaceOpen && !workspaceSessionsVisible ? "flex-1 min-h-0 [&>[data-panel][id=list]]:hidden!" : "flex-1 min-h-0"}\n      defaultLayout={defaultLayout}')
    add(page, '      <ResizableHandle />\n      <ResizablePanel id="detail"', '      <ResizableHandle className={workspaceOpen && !workspaceSessionsVisible ? "hidden" : undefined} />\n      <ResizablePanel id="detail"')
    composer='packages/views/chat/components/chat-input.tsx'
    add(composer, 'import type { ReactNode } from "react";', 'import type { ReactNode } from "react";\nimport type { WorkspaceContext } from "@multica/core/types/remote-workspace";\nimport { RemoteWorkspaceChips, useRemoteWorkspace } from "./remote-workspace";')
    add(composer, '    draftAttachments: Attachment[],\n', '    draftAttachments: Attachment[],\n    workspaceContext?: WorkspaceContext,\n')
    add(composer, '  const { t } = useT("chat");', '  const remoteWorkspace = useRemoteWorkspace();\n  const { t } = useT("chat");')
    add(composer, '      const keyAtSend = draftKey;', '      const keyAtSend = draftKey;\n      const workspaceAtSend = remoteWorkspace?.draft;')
    add(composer, '        committed = true;\n', '        committed = true;\n        if (workspaceAtSend) remoteWorkspace?.clearSent(workspaceAtSend);\n')
    add(composer, '        draftAttachments.filter((attachment) => uniqueActiveIds.includes(attachment.id)),', '        draftAttachments.filter((attachment) => uniqueActiveIds.includes(attachment.id)),\n        ...(workspaceAtSend?.references.length ? [workspaceAtSend] as [WorkspaceContext] : []),')
    add(composer, '        {selectedProject && (', '        <RemoteWorkspaceChips />\n        {selectedProject && (')
    for rel in ('packages/views/chat/components/use-chat-controller.ts','packages/views/chat/components/chat-window.tsx'):
        add(rel, '"use client";', '"use client";\nimport type { WorkspaceContext } from "@multica/core/types/remote-workspace";\nimport { workspaceError } from "./remote-workspace-state";')
        add(rel, '      draftAttachments: Attachment[] = [],', '      draftAttachments: Attachment[] = [],\n      workspaceContext?: WorkspaceContext,')
        add(rel, 'await api.sendChatMessage(sessionId, finalContent, attachmentIds)', 'await api.sendChatMessage(sessionId, finalContent, attachmentIds, workspaceContext)')
        add(rel, '      apiLogger.info("sendChatMessage.success", {', '      const acceptedContent = result.content ?? finalContent;\n      apiLogger.info("sendChatMessage.success", {')
        # Each occurrence is a separately checked patch: message and pending seed.
        add(rel, '        content: finalContent,\n        task_id: result.task_id,', '        content: acceptedContent,\n        task_id: result.task_id,')
        add(rel, '        content: finalContent,\n        supports_queue: result.supports_queue,', '        content: acceptedContent,\n        supports_queue: result.supports_queue,')
        add(rel, '        apiLogger.error("sendChatMessage.error", { sessionId, err });', '        apiLogger.error("sendChatMessage.error", { sessionId, err });\n        if (workspaceContext) { toast.error(workspaceError(err)); return false; }')
