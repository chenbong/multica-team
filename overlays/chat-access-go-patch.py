"""Checked shared-chat overlays; no schema changes or upstream writes."""
import re


def replace_once(source, before, after):
    if source.count(before) != 1:
        raise SystemExit(f"chat access anchor changed: {before[:100]!r}")
    return source.replace(before, after, 1)


def function_patch(source, name, rewrite):
    pattern = rf"(?ms)^func \(h \*Handler\) {name}\(.*?(?=^func |\Z)"
    match = re.search(pattern, source)
    if not match:
        raise SystemExit(f"chat access function absent: {name}")
    return source[:match.start()] + rewrite(match.group()) + source[match.end():]


def shared_gate(text):
    return replace_once(text, "h.gatePublicChatSessionForUser(", "h.gateAccessibleChatSession(")


def chat_handler(source):
    for name in ("GetChatSession", "ListChatMessages", "ListChatMessagesPage", "GetPendingChatTask", "SendChatMessage"):
        source = function_patch(source, name, shared_gate)
    def list_sessions(text):
        start = text.index("\tmember, ok := h.workspaceMember(")
        end = text.index("\n\tif !ok {", text.index("\tallowed, ok :=", start))
        text = text[:start] + "\tallowed, ok := h.accessibleChatAgentIDs(r.Context(), workspaceID, userID)" + text[end:]
        text = text.replace("ListAllChatSessionsByCreator", "ListAllChatSessionsForViewer").replace("ListChatSessionsByCreator", "ListChatSessionsForViewer")
        text = text.replace("\t\t\t})\n\t\t}", "\t\t\t})\n\t\t\tif uuidToString(s.CreatorID) != userID {\n\t\t\t\tresp[len(resp)-1].HasUnread = false\n\t\t\t\tresp[len(resp)-1].UnreadCount = 0\n\t\t\t\tresp[len(resp)-1].Pinned = false\n\t\t\t}\n\t\t}")
        return text
    source = function_patch(source, "ListChatSessions", list_sessions)
    for name in ("ListChatMessages", "ListChatMessagesPage"):
        source = function_patch(source, name, lambda text: replace_once(text, "\twriteJSON(w, http.StatusOK,", "\tif err := h.hydrateChatMessageSenders(r.Context(), resp); err != nil {\n\t\twriteError(w, http.StatusInternalServerError, \"failed to load chat senders\")\n\t\treturn\n\t}\n\twriteJSON(w, http.StatusOK,"))
    source = replace_once(source, "type ChatMessageResponse struct {", "type ChatMessageResponse struct {\n\tSender *ChatMessageSenderResponse `json:\"sender,omitempty\"`")
    return source


def viewer_queries(source):
    clones = []
    for old, new in (("ListChatSessionsByCreator", "ListChatSessionsForViewer"), ("ListAllChatSessionsByCreator", "ListAllChatSessionsForViewer")):
        const = old[0].lower() + old[1:]
        match = re.search(rf"(?ms)^const {const} = .*?(?=^const |\Z)", source)
        if not match: raise SystemExit(f"chat list query absent: {old}")
        clone = match.group().replace(old, new).replace(const, new[0].lower() + new[1:])
        clone = replace_once(clone, "AND cs.creator_id = $2", "AND $2::uuid IS NOT NULL")
        clone = replace_once(clone, "ORDER BY (cs.pinned_at IS NOT NULL) DESC, cs.pinned_at DESC,", "ORDER BY (cs.creator_id = $2 AND cs.pinned_at IS NOT NULL) DESC, CASE WHEN cs.creator_id = $2 THEN cs.pinned_at END DESC,")
        clones.append(clone)
    source = replace_once(source, "    channel_context_revision, id\n)\nVALUES (", "    channel_context_revision, id, author_user_id\n)\nVALUES (")
    source = replace_once(source, "    COALESCE($12::uuid, gen_random_uuid())\n)", "    COALESCE($12::uuid, gen_random_uuid()), $13\n)")
    source = replace_once(source, "type CreateChatMessageParams struct {", "type CreateChatMessageParams struct {\n\tAuthorUserID pgtype.UUID `json:\"author_user_id\"`")
    marker = "\t\targ.ChannelContextRevision,\n\t\targ.ID,\n\t)\n\tvar i ChatMessage\n"
    source = replace_once(source, marker, "\t\targ.ChannelContextRevision,\n\t\targ.ID,\n\t\targ.AuthorUserID,\n\t)\n\tvar i ChatMessage\n")
    return source + "\n" + "\n".join(clones)


def task_service(source):
    start = source.index("func (s *TaskService) SendDirectChatMessage(")
    end = source.index("\nfunc ", start + 1)
    text = source[start:end]
    text = replace_once(text, "\t\tmsg, err := qtx.CreateChatMessage(ctx, db.CreateChatMessageParams{", "\t\tmsg, err := qtx.CreateChatMessage(ctx, db.CreateChatMessageParams{\n\t\t\tAuthorUserID: initiatorUserID,")
    return source[:start] + text + source[end:]


def file_handler(source):
    source = function_patch(source, "UploadFile", shared_gate)
    source = function_patch(source, "loadAttachmentForRequest", lambda text: replace_once(text, "\treturn att, true", "\tif !h.gateChatAttachment(w, r, att) { return db.Attachment{}, false }\n\treturn att, true"))
    source = function_patch(source, "loadAttachmentForDownload", lambda text: text.replace("return att, true", "if !h.gateChatAttachment(w, r, att) { return db.Attachment{}, false }; return att, true"))
    source = function_patch(source, "DeleteAttachment", lambda text: replace_once(text, "\t// Captured-context attachments", "\tif att.ChatSessionID.Valid {\n\t\tif _, ok := h.loadChatSessionForUser(w, r, userID, workspaceID, uuidToString(att.ChatSessionID)); !ok { return }\n\t}\n\t// Captured-context attachments"))
    source = function_patch(source, "buildMarkdownURL", lambda text: replace_once(text, "\tif h.storageURLIsPubliclyReadable(a.Url) {", "\tif a.ChatSessionID.Valid {\n\t\tif publicURL != \"\" { return publicURL + relPath }\n\t\treturn relPath\n\t}\n\tif h.storageURLIsPubliclyReadable(a.Url) {"))
    source = function_patch(source, "ServeLocalUpload", lambda text: replace_once(text, "\tlocal.ServeFile(w, r, key)", "\t// Chat objects are served through authenticated download/capability routes.\n\t// Never trust user-supplied identity headers on this unauthenticated route.\n\tif h.DB != nil {\n\t\tvar chatObject bool\n\t\tif err := h.DB.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM attachment WHERE chat_session_id IS NOT NULL AND (url=$1 OR right(url,length($2))=$2))`, h.Storage.ObjectURL(key), \"/\"+key).Scan(&chatObject); err != nil || chatObject {\n\t\t\thttp.NotFound(w, r); return\n\t\t}\n\t}\n\tlocal.ServeFile(w, r, key)"))
    return source


def daemon_handler(source):
    def task_messages(text):
        return replace_once(text, "\tvar (\n\t\tmessages []db.TaskMessage", "\tif task.ChatSessionID.Valid {\n\t\tuserID, ok := requireUserID(w, r)\n\t\tif !ok { return }\n\t\tif !h.CanReadChatSession(r.Context(), userID, wsID, uuidToString(task.ChatSessionID)) {\n\t\t\twriteError(w, http.StatusForbidden, \"you do not have access to this chat agent\")\n\t\t\treturn\n\t\t}\n\t}\n\tvar (\n\t\tmessages []db.TaskMessage")
    return function_patch(source, "ListTaskMessagesByUser", task_messages)


def agent_handler(source):
    source = function_patch(source, "ListAgentTasks", lambda text: replace_once(text, "\tresp := make([]AgentTaskResponse, len(tasks))", "\ttasks = h.filterReadableChatTasks(r.Context(), requestUserID(r), workspaceID, tasks)\n\tresp := make([]AgentTaskResponse, len(tasks))"))
    source = function_patch(source, "ListWorkspaceAgentTaskSnapshot", lambda text: replace_once(text, "\tresp := make([]AgentTaskResponse, 0, len(tasks))", "\ttasks = h.filterReadableChatTasks(r.Context(), requestUserID(r), workspaceID, tasks)\n\tresp := make([]AgentTaskResponse, 0, len(tasks))"))
    return source


def scope_authorizer(source):
    source = replace_once(source, "type dbScopeAuthorizer struct{ q scopeAuthQuerier }", "type dbScopeAuthorizer struct {\n\tq scopeAuthQuerier\n\tchatAccess func(context.Context, string, string, string) bool\n}")
    source += "\nfunc newChatScopeAuthorizer(q scopeAuthQuerier, access func(context.Context, string, string, string) bool) *dbScopeAuthorizer {\n\treturn &dbScopeAuthorizer{q:q, chatAccess:access}\n}\n"
    needle = "\n\t\t\tuidUUID, err := util.ParseUUID(userID)"
    source = replace_once(source, needle, "\n\t\t\tif a.chatAccess != nil { return a.chatAccess(ctx, userID, workspaceID, util.UUIDToString(sess.ID)), nil }" + needle)
    needle = "\n\t\tuidUUID, err := util.ParseUUID(userID)"
    source = replace_once(source, needle, "\n\t\tif a.chatAccess != nil { return a.chatAccess(ctx, userID, workspaceID, util.UUIDToString(sess.ID)), nil }" + needle)
    return source


def realtime_hub(source):
    source = replace_once(source, "\tauthorizer ScopeAuthorizer", "\tauthorizer ScopeAuthorizer\n\tdeliveryFilter func(context.Context, string, string, []byte) bool")
    for name, body in (
        ("BroadcastToScopeDedup", '\n\th.broadcastFiltered(scopeType, scopeID, message, eventID, "")\n'),
        ("fanoutUser", '\n\th.broadcastFiltered(ScopeUser, userID, message, eventID, excludeWorkspace)\n'),
    ):
        match = re.search(rf"(?ms)^func \(h \*Hub\) {name}\([^\n]+\{{\n.*?^\}}", source)
        if not match: raise SystemExit(f"hub function absent: {name}")
        signature = match.group().split("\n", 1)[0]
        source = source[:match.start()] + signature + body + "}" + source[match.end():]
    source += '''
// SetDeliveryFilter checks current chat ACLs on every outbound frame.
func (h *Hub) SetDeliveryFilter(filter func(context.Context, string, string, []byte) bool) {
    h.mu.Lock(); defer h.mu.Unlock(); h.deliveryFilter = filter
}

func (h *Hub) broadcastFiltered(scopeType, scopeID string, message []byte, eventID, excludeWorkspace string) {
    if scopeType == "" || scopeID == "" { return }
    key := sk(scopeType, scopeID)
    h.mu.RLock()
    clients := make([]*Client, 0, len(h.rooms[key]))
    for client := range h.rooms[key] { clients = append(clients, client) }
    filter := h.deliveryFilter
    h.mu.RUnlock()
    var slow []*Client
    var sent int64
    for _, client := range clients {
        if excludeWorkspace != "" && client.workspaceID == excludeWorkspace { continue }
        // Do not hold the hub lock across a policy/database lookup.
        if filter != nil && !filter(context.Background(), client.userID, client.workspaceID, message) { continue }
        h.mu.RLock()
        if !h.rooms[key][client] || !client.markSeen(eventID) { h.mu.RUnlock(); continue }
        select {
        case client.send <- message: sent++
        default: slow = append(slow, client)
        }
        h.mu.RUnlock()
    }
    if sent > 0 { M.MessagesSentTotal.Add(sent) }
    if len(slow) > 0 { h.evictSlow(slow) }
}
'''
    return source


def listeners(source):
    source = replace_once(source, "\t\tprotocol.EventChatSessionCreated: true,\n\t\tprotocol.EventChatSessionUpdated: true,\n", "")
    start = source.index("\t// A Chat session is creator-private.")
    end = source.index("\t// member:added", start)
    return source[:start] + source[end:]


def generated(upstream, files):
    for relative, rewrite in (
        ("server/internal/handler/chat.go", chat_handler),
        ("server/internal/handler/file.go", file_handler),
        ("server/internal/handler/daemon.go", daemon_handler),
        ("server/internal/handler/agent.go", agent_handler),
        ("server/pkg/db/generated/chat.sql.go", viewer_queries),
        ("server/internal/service/task.go", task_service),
        ("server/cmd/server/scope_authorizer.go", scope_authorizer),
        ("server/internal/realtime/hub.go", realtime_hub),
        ("server/cmd/server/listeners.go", listeners),
    ):
        source = files / relative
        if not source.is_file(): source = upstream / relative
        yield relative, rewrite(source.read_text())
