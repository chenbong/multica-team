"""Apply shared-chat client changes to an isolated frontend tree only."""
from pathlib import Path


def add(changes, relative, replacements):
    changes.setdefault(relative, []).extend(replacements)


def extend(changes):
    if any("Actual initiating member" in after for _, after in changes.get("packages/core/types/chat.ts", [])):
        return
    add(changes, "packages/core/types/chat.ts", [
        ('export interface ChatMessage {', 'export interface ChatMessage {\n  /** Actual initiating member for this turn, absent on unattributed historical rows. */\n  sender?: { user_id: string; name: string } | null;'),
    ])
    add(changes, "packages/core/api/schemas.ts", [
        ('export const ChatMessageSchema = z.object({', 'export const ChatMessageSchema = z.object({\n  sender: z.object({ user_id: z.string(), name: z.string().default("") }).nullable().optional().catch(undefined),'),
    ])
    add(changes, "packages/core/chat/queries.ts", [
        ('    queryFn: () => api.listChatSessions({ status: "all" }),\n    staleTime: Infinity,', '    queryFn: () => api.listChatSessions({ status: "all" }),\n    staleTime: 30_000,\n    refetchInterval: 30_000,'),
    ])
    add(changes, "packages/views/chat/components/chat-message-list.tsx", [
        ('          {/* User messages are authored as markdown', '          {message.sender?.name && <div className="mb-1 text-caption text-muted-foreground">{message.sender.name}</div>}\n          {/* User messages are authored as markdown'),
    ])
    add(changes, "packages/views/chat/components/use-chat-controller.ts", [
        ('  const activeAgent =\n    sessionAgent ??\n    availableAgents.find((a) => a.id === selectedAgentId) ??\n    availableAgents[0] ??\n    null;', '  const activeAgent = currentSession ? sessionAgent : (\n    availableAgents.find((a) => a.id === selectedAgentId) ??\n    availableAgents[0] ??\n    null\n  );'),
        ('  const isSessionArchived = currentSession?.status === "archived";', '  const isSessionOwner = !activeSessionId || (!!user?.id && currentSession?.creator_id === user.id);\n  const isSessionArchived = currentSession?.status === "archived";'),
        ('    if (!currentHasUnread) return;', '    if (!currentHasUnread || !isSessionOwner) return;'),
        ('[isActive, appForeground, activeSessionId, currentHasUnread]', '[isActive, appForeground, activeSessionId, currentHasUnread, isSessionOwner]'),
        ('    if (!pendingTaskId || !activeSessionId) {', '    if (!isSessionOwner || !pendingTaskId || !activeSessionId) {'),
        ('[pendingTaskId, activeSessionId, cancelChatTask]', '[pendingTaskId, activeSessionId, cancelChatTask, isSessionOwner]'),
        ('    isSessionArchived,\n', '    isSessionArchived,\n    isSessionOwner,\n'),
        ('      currentSession,\n', '      currentSession,\n      isSessionOwner,\n'),
        ('useChatDraftRestore(activeSessionId, isActive && appForeground)', 'useChatDraftRestore(activeSessionId, isActive && appForeground && (!activeSessionId || sessions.some((s) => s.id === activeSessionId && s.creator_id === user?.id)))'),
        ('      if (projectId === activeProjectId) return;', '      if (activeSessionId && !isSessionOwner) return;\n      if (projectId === activeProjectId) return;'),
    ])
    add(changes, "packages/views/chat/components/chat-session-header.tsx", [
        ('  onArchive,\n}: {', '  onArchive,\n  canManage = false,\n}: {'),
        ('  session: ChatSession;', '  session: ChatSession;\n  canManage?: boolean;'),
        ('            onClick={startRename}', '            onClick={canManage ? startRename : undefined}\n            disabled={!canManage}'),
        ('      <DropdownMenu>\n', '      {canManage && <DropdownMenu>\n'),
        ('      </DropdownMenu>\n', '      </DropdownMenu>}\n'),
    ])
    add(changes, "packages/views/chat/components/chat-thread-list.tsx", [
        ('  onArchive,\n}: {', '  onArchive,\n  userId,\n}: {'),
        ('  sessions: ChatSession[];', '  sessions: ChatSession[];\n  userId?: string;'),
        ('    const rowActions: RowActionItem[] =\n      view', '    const rowActions: RowActionItem[] =\n      session.creator_id !== userId ? [] : view'),
        ('{!isConfirmingAction && (\n          <RowActionsMenu', '{!isConfirmingAction && rowActions.length > 0 && (\n          <RowActionsMenu'),
    ])
    add(changes, "packages/views/chat/chat-page.tsx", [
        ('        sessions={c.sessions}', '        sessions={c.sessions}\n        userId={c.user?.id}'),
        ('          onArchive={handleArchive}', '          onArchive={handleArchive}\n          canManage={c.isSessionOwner}'),
        ('          onRegenerateQuickActions={(message) =>', '          onRegenerateQuickActions={c.isSessionOwner ? (message) =>'),
        ('              : undefined\n          }', '              : undefined\n            : undefined\n          }'),
        ('      <ChatQueue\n', '      {c.isSessionOwner && <ChatQueue\n'),
        ('        onClear={c.handleClearQueuedTasks}\n      />', '        onClear={c.handleClearQueuedTasks}\n      />}'),
        ('        onStop={c.handleStop}', '        onStop={c.isSessionOwner ? c.handleStop : undefined}'),
        ('        onProjectChange={changeProjectContext}', '        onProjectChange={c.isSessionOwner ? changeProjectContext : undefined}'),
    ])
    add(changes, "packages/views/chat/components/chat-window.tsx", [
        ('  const activeAgent =\n    sessionAgent ??\n    availableAgents.find((a) => a.id === selectedAgentId) ??\n    availableAgents[0] ??\n    null;', '  const activeAgent = currentSession ? sessionAgent : (\n    availableAgents.find((a) => a.id === selectedAgentId) ??\n    availableAgents[0] ??\n    null\n  );'),
        ('  const isSessionArchived = currentSession?.status === "archived";', '  const isSessionOwner = !activeSessionId || (!!user?.id && currentSession?.creator_id === user.id);\n  const isSessionArchived = currentSession?.status === "archived";'),
        ('    if (!currentHasUnread) return;', '    if (!currentHasUnread || !isSessionOwner) return;'),
        ('[isOpen, appForeground, activeSessionId, currentHasUnread]', '[isOpen, appForeground, activeSessionId, currentHasUnread, isSessionOwner]'),
        ('useChatDraftRestore(activeSessionId, isOpen && appForeground)', 'useChatDraftRestore(activeSessionId, isOpen && appForeground && (!activeSessionId || sessions.some((s) => s.id === activeSessionId && s.creator_id === user?.id)))'),
        ('        onStop={handleStop}', '        onStop={isSessionOwner ? handleStop : undefined}'),
        ('      <ChatQueue\n', '      {isSessionOwner && <ChatQueue\n'),
        ('        onClear={handleClearQueuedTasks}\n      />', '        onClear={handleClearQueuedTasks}\n      />}'),
        ('        onProjectChange={handleProjectChange}', '        onProjectChange={isSessionOwner ? handleProjectChange : undefined}'),
        ('  const title = activeSession?.title?.trim() || t(($) => $.window.untitled);', '  const viewerID = useAuthStore((s) => s.user?.id);\n  const title = activeSession?.title?.trim() || t(($) => $.window.untitled);'),
        ('    const rowActions: SessionRowAction[] = isRunning', '    const rowActions: SessionRowAction[] = session.creator_id !== viewerID ? [] : isRunning'),
        ('              <RowActionsMenu\n', '              {rowActions.length > 0 && <RowActionsMenu\n'),
        ('                groups={[rowActions]}\n              />', '                groups={[rowActions]}\n              />}'),
    ])
    # Existing tests explicitly identify their creator-owned fixtures.
    add(changes, "packages/views/chat/components/chat-session-header.test.tsx", [
        ('<ChatSessionHeader session={session} agent={null} />', '<ChatSessionHeader session={session} agent={null} canManage />'),
    ])
    add(changes, "packages/views/chat/components/chat-thread-list.test.tsx", [
        ('        sessions={renderedSessions}', '        sessions={renderedSessions}\n        userId="user-1"'),
    ])


def apply(root):
    root = Path(root)
    if (root / ".git").exists(): raise SystemExit("Chat patches require a generated frontend tree")
    changes = {}
    extend(changes)
    planned = []
    for relative, replacements in changes.items():
        path = root / relative
        source = path.read_text()
        for before, after in replacements:
            if source.count(before) != 1:
                raise SystemExit(f"chat web anchor changed: {relative}: {before[:90]!r}")
            source = source.replace(before, after, 1)
        planned.append((path, source))
    for path, source in planned:
        path.write_text(source)
