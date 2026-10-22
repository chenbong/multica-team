"""Checked, composable remote workspace integration; never edits upstream."""
def replace(text, before, after):
    if text.count(before) != 1:
        raise SystemExit(f"remote workspace anchor changed: {before[:100]!r}")
    return text.replace(before, after, 1)

def extend(upstream, files, generated):
    texts = dict(generated)
    def patch(rel, fn):
        source = texts.get(rel)
        if source is None:
            p = files / rel
            source = (p if p.exists() else upstream / rel).read_text()
        texts[rel] = fn(source)
    patch('server/internal/daemonws/hub.go', lambda s: replace(replace(s,
        'type Hub struct {', 'type Hub struct {\n workspaceMu sync.Mutex\n workspacePending map[string]workspacePending'),
        'case protocol.EventDaemonHeartbeat:\n\t\tc.handleHeartbeatFrame(msg.Payload)',
        'case protocol.RemoteWorkspaceResponseEvent:\n c.handleWorkspaceResponse(msg.Payload)\n case protocol.EventDaemonHeartbeat:\n\t\tc.handleHeartbeatFrame(msg.Payload)'))
    # daemon.go is large; keep the executor hooks small and independently checked.
    def daemon(s):
        s = replace(s, 'type Daemon struct {', 'type Daemon struct {\n remoteWorkspaceMu sync.Mutex\n remoteWorkspaceSlots chan struct{}\n remoteWorkspaces map[string]remoteWorkspaceRoot')
        s = replace(s, '\tif env.LocalWorktree != nil {\n\t\tdefer func() {\n\t\t\tif taskResult.WorkDir == "" {',
            '\td.registerRemoteWorkspace(task, env.WorkDir)\n\tdefer func() {\n if taskResult.DurableWorkDir != "" { d.registerRemoteWorkspace(task, taskResult.DurableWorkDir) }\n }()\n\tif env.LocalWorktree != nil {\n\t\tdefer func() {\n\t\t\tif taskResult.WorkDir == "" {')
        return s
    patch('server/internal/daemonws/hub.go', lambda s: replace(s, 'c.conn.SetReadLimit(64 * 1024)', 'c.conn.SetReadLimit(2 << 20) // Bounded workspace previews and snapshots.'))
    patch('server/internal/daemon/daemon.go', daemon)
    patch('server/internal/daemon/client.go', lambda s: replace(s, '\t\tprotocol.DaemonCapabilityRPCV1,', '\t\tprotocol.DaemonCapabilityRPCV1,\n protocol.RemoteWorkspaceCapability,'))
    patch('server/internal/daemon/wakeup.go', lambda s: replace(s, '\t\tcase protocol.EventDaemonTaskAvailable:', '\t\tcase protocol.RemoteWorkspaceRequestEvent:\n d.dispatchRemoteWorkspace(msg.Payload, wsRPCGeneration)\n\t\tcase protocol.EventDaemonTaskAvailable:'))
    patch('server/cmd/server/router.go', lambda s: replace(s, '\t\t\t\t\tr.Get("/", h.GetChatSession)', '\t\t\t\t\tr.Get("/", h.GetChatSession)\n r.Post("/workspace", h.ChatWorkspace)'))
    def chat(s):
        s = replace(s, 'type SendChatMessageRequest struct {', 'type SendChatMessageRequest struct {\n WorkspaceContext *ChatWorkspaceContext `json:"workspace_context,omitempty"`')
        anchor = '\tsent, err := h.TaskService.SendDirectChatMessage('
        s = replace(s, anchor, '\tif req.WorkspaceContext != nil {\n var valid bool\n req.Content, valid = h.resolveChatWorkspaceContext(w, r, session, userID, req.Content, *req.WorkspaceContext)\n if !valid { return }\n }\n' + anchor)
        s = replace(s, 'type SendChatMessageResponse struct {', 'type SendChatMessageResponse struct {\n Content string `json:"content,omitempty"`')
        s = replace(s, '\twriteJSON(w, http.StatusCreated, SendChatMessageResponse{', '\twriteJSON(w, http.StatusCreated, SendChatMessageResponse{\n Content: req.Content,')
        return s
    patch('server/internal/handler/chat.go', chat)
    return list(texts.items())
