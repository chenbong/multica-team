package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// chatAccess is shared by HTTP reads and realtime delivery. A session never
// grants access to its agent; membership and the current agent ACL are checked
// independently, including for the original creator.
func (h *Handler) chatAccess(ctx context.Context, userID, workspaceID string, session db.ChatSession) bool {
	if uuidToString(session.WorkspaceID) != workspaceID || userID == "" {
		return false
	}
	_, err := h.getWorkspaceMember(ctx, userID, workspaceID)
	if err != nil {
		return false
	}
	agent, err := h.Queries.GetAgent(ctx, session.AgentID)
	if err != nil || uuidToString(agent.WorkspaceID) != workspaceID {
		return false
	}
	// Builders are hidden system carriers, not shareable chat agents. Their
	// creator may use the creation flow without granting access to other members.
	if isAgentBuilderCarrier(agent) {
		return uuidToString(session.CreatorID) == userID && uuidToString(agent.OwnerID) == userID
	}
	if agent.Kind != "user" {
		return false
	}
	// Ordinary user chat access follows explicit grants and shared/private
	// agent visibility. No creator or workspace-admin exception is added.
	return h.invokeAgentDecision(ctx, agent, "member", userID, userID, workspaceID)
}

func (h *Handler) accessibleChatAgentIDs(ctx context.Context, workspaceID, userID string) (map[string]struct{}, bool) {
	ws, err := util.ParseUUID(workspaceID)
	if err != nil {
		return nil, false
	}
	if _, err := h.getWorkspaceMember(ctx, userID, workspaceID); err != nil {
		return nil, false
	}
	agents, err := h.Queries.ListAllAgents(ctx, ws)
	if err != nil {
		return nil, false
	}
	allowed := make(map[string]struct{})
	for _, agent := range agents {
		if h.invokeAgentDecision(ctx, agent, "member", userID, userID, workspaceID) {
			allowed[uuidToString(agent.ID)] = struct{}{}
		}
	}
	return allowed, true
}

// CanReadChatSession is the scope-authorizer contract. Hidden channel command
// sessions remain hidden even when the agent itself is accessible.
func (h *Handler) CanReadChatSession(ctx context.Context, userID, workspaceID, sessionID string) bool {
	id, err := util.ParseUUID(sessionID)
	if err != nil {
		return false
	}
	ws, err := util.ParseUUID(workspaceID)
	if err != nil {
		return false
	}
	session, err := h.Queries.GetPublicChatSessionInWorkspace(ctx, db.GetPublicChatSessionInWorkspaceParams{ID: id, WorkspaceID: ws})
	return err == nil && h.chatAccess(ctx, userID, workspaceID, session)
}

func (h *Handler) gateAccessibleChatSession(w http.ResponseWriter, r *http.Request, userID, workspaceID, sessionID string) (db.ChatSession, bool) {
	id, ok := parseUUIDOrBadRequest(w, sessionID, "chat session id")
	if !ok {
		return db.ChatSession{}, false
	}
	ws, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return db.ChatSession{}, false
	}
	session, err := h.Queries.GetPublicChatSessionInWorkspace(r.Context(), db.GetPublicChatSessionInWorkspaceParams{ID: id, WorkspaceID: ws})
	if err != nil {
		writeError(w, http.StatusNotFound, "chat session not found")
		return db.ChatSession{}, false
	}
	if !h.chatAccess(r.Context(), userID, workspaceID, session) {
		writeError(w, http.StatusForbidden, "you do not have access to this chat agent")
		return db.ChatSession{}, false
	}
	return session, true
}

// CanDeliverChatEvent filters both workspace/user fanout and explicit chat/task
// rooms at delivery time. Rechecking here closes the already-subscribed socket
// gap when agent grants or workspace membership are revoked.
func (h *Handler) CanDeliverChatEvent(ctx context.Context, userID, workspaceID string, frame []byte) bool {
	var envelope struct {
		Type    string `json:"type"`
		Payload struct {
			ID            string `json:"id"`
			ChatSessionID string `json:"chat_session_id"`
			TaskID        string `json:"task_id"`
			Session       struct {
				ID string `json:"id"`
			} `json:"session"`
			Task struct {
				ID            string `json:"id"`
				ChatSessionID string `json:"chat_session_id"`
			} `json:"task"`
		} `json:"payload"`
	}
	if json.Unmarshal(frame, &envelope) != nil {
		return false
	}
	chatID := envelope.Payload.ChatSessionID
	if chatID == "" {
		chatID = envelope.Payload.Task.ChatSessionID
	}
	if strings.HasPrefix(envelope.Type, "chat:") {
		if chatID == "" {
			chatID = envelope.Payload.Session.ID
		}
		return h.CanReadChatSession(ctx, userID, workspaceID, chatID)
	}
	if !strings.HasPrefix(envelope.Type, "task:") {
		return true
	}
	if chatID == "" {
		taskID := envelope.Payload.TaskID
		if taskID == "" {
			taskID = envelope.Payload.Task.ID
		}
		if taskID == "" {
			taskID = envelope.Payload.ID
		}
		if taskID == "" {
			return false
		}
		id, err := util.ParseUUID(taskID)
		if err != nil {
			return false
		}
		task, err := h.Queries.GetAgentTask(ctx, id)
		if err != nil {
			return false
		}
		if !task.ChatSessionID.Valid {
			ws, err := h.TaskService.ResolveTaskWorkspaceIDChecked(ctx, task)
			if err != nil || ws != workspaceID {
				return false
			}
			_, err = h.getWorkspaceMember(ctx, userID, workspaceID)
			return err == nil
		}
		chatID = uuidToString(task.ChatSessionID)
	}
	return h.CanReadChatSession(ctx, userID, workspaceID, chatID)
}

func (h *Handler) gateChatAttachment(w http.ResponseWriter, r *http.Request, att db.Attachment) bool {
	if !att.ChatSessionID.Valid {
		return true
	}
	// Task-owned attachment reads remain available to the exact authenticated
	// producing run. Client headers cannot create this identity: middleware
	// strips them and only stamps them for a validated task token.
	if r.Header.Get("X-Actor-Source") == "task_token" {
		id, err := util.ParseUUID(r.Header.Get("X-Task-ID"))
		if err == nil {
			task, loadErr := h.Queries.GetAgentTask(r.Context(), id)
			if loadErr == nil && task.ChatSessionID == att.ChatSessionID && uuidToString(att.WorkspaceID) == middleware.WorkspaceIDFromContext(r.Context()) {
				return true
			}
		}
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return false
	}
	if !h.CanReadChatSession(r.Context(), userID, uuidToString(att.WorkspaceID), uuidToString(att.ChatSessionID)) {
		writeError(w, http.StatusNotFound, "attachment not found")
		return false
	}
	return true
}

// hydrateChatMessageSenders uses the persisted per-message author, not the
// run's initiator or the session owner. Coalesced inputs can share a task.
// Existing unattributed user rows retain a null sender rather than inventing
// an author. Assistant rows are always attributed by their agent UI.
func (h *Handler) hydrateChatMessageSenders(ctx context.Context, responses []ChatMessageResponse) error {
	if len(responses) == 0 {
		return nil
	}
	ids := make([]pgtype.UUID, 0, len(responses))
	for _, response := range responses {
		ids = append(ids, parseUUID(response.ID))
	}
	rows, err := h.DB.Query(ctx, `SELECT m.id::text, m.author_user_id::text, u.name FROM chat_message m LEFT JOIN "user" u ON u.id=m.author_user_id WHERE m.id=ANY($1::uuid[]) AND m.role='user' AND m.author_user_id IS NOT NULL`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	byID := make(map[string]*ChatMessageSenderResponse)
	for rows.Next() {
		var id, userID string
		var name *string
		if err := rows.Scan(&id, &userID, &name); err != nil {
			return err
		}
		label := ""
		if name != nil {
			label = *name
		}
		byID[id] = &ChatMessageSenderResponse{UserID: userID, Name: label}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range responses {
		responses[i].Sender = byID[responses[i].ID]
	}
	return nil
}

type ChatMessageSenderResponse struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
}

func (h *Handler) filterReadableChatTasks(ctx context.Context, userID, workspaceID string, tasks []db.AgentTaskQueue) []db.AgentTaskQueue {
	visible := make([]db.AgentTaskQueue, 0, len(tasks))
	allowed := make(map[string]bool)
	for _, task := range tasks {
		if task.ChatSessionID.Valid {
			id := uuidToString(task.ChatSessionID)
			canRead, known := allowed[id]
			if !known {
				canRead = h.CanReadChatSession(ctx, userID, workspaceID, id)
				allowed[id] = canRead
			}
			if !canRead {
				continue
			}
		}
		visible = append(visible, task)
	}
	return visible
}
