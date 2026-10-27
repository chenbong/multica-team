package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"net/http"
)

// The trusted Bridge supplies the real sender. A robot-owner PAT and arbitrary
// user headers cannot use this endpoint to substitute a human identity.
func (h *Handler) InfoFlowChatInput(w http.ResponseWriter, r *http.Request) {
	if !infoFlowAuthenticate(w, r) {
		return
	}
	var req struct {
		infoFlowScope
		Sender        string               `json:"sender"`
		Conversation  infoFlowConversation `json:"conversation"`
		SessionID     string               `json:"session_id"`
		Content       string               `json:"content"`
		Title         string               `json:"title"`
		AttachmentIDs []string             `json:"attachment_ids"`
		CreateIssue   bool                 `json:"create_issue"`
	}
	if !infoFlowDecode(w, r, &req) {
		return
	}
	if !req.infoFlowScope.valid() || !infoFlowUsername.MatchString(req.Sender) || len(req.Content) > 65536 || len(req.AttachmentIDs) > 32 {
		infoFlowError(w, 400, "invalid chat input")
		return
	}
	preflight := infoFlowRelayRequest{infoFlowScope: req.infoFlowScope, Sender: req.Sender, Conversation: req.Conversation, Operation: "binding"}
	body, _ := json.Marshal(preflight)
	probe, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, "/api/integrations/infoflow/issue-relay", bytes.NewReader(body))
	probe.Header.Set("Authorization", r.Header.Get("Authorization"))
	captured := &infoFlowCapture{}
	h.InfoFlowIssueRelay(captured, probe)
	if captured.status != 200 {
		infoFlowError(w, captured.status, "chat authorization failed")
		return
	}
	var admission infoFlowRelayResponse
	if json.Unmarshal(captured.Bytes(), &admission) != nil {
		infoFlowError(w, 503, "chat admission unavailable")
		return
	}
	if admission.Handled || !admission.CanComment || admission.ConversationMode != "chat" {
		infoFlowError(w, 403, "chat invocation rejected")
		return
	}
	user, _, err := h.infoFlowIdentity(r.Context(), req.Sender, req.WorkspaceID)
	if err != nil {
		infoFlowError(w, 403, "workspace membership required")
		return
	}
	member, err := h.Queries.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{WorkspaceID: parseUUID(req.WorkspaceID), UserID: user.ID})
	if err != nil {
		infoFlowError(w, 403, "workspace membership required")
		return
	}
	var encoded []byte
	var uploadScope *infoFlowChatUploadScope
	if req.CreateIssue {
		encoded, _ = json.Marshal(map[string]any{"title": req.Title, "description": req.Content, "assignee_type": "agent", "assignee_id": req.AgentID, "attachment_ids": req.AttachmentIDs, "allow_duplicate": true})
	} else {
		session, ok := parseUUIDOrBadRequest(w, req.SessionID, "chat session id")
		if !ok {
			return
		}
		current, e := h.Queries.GetChatSessionInWorkspace(r.Context(), db.GetChatSessionInWorkspaceParams{ID: session, WorkspaceID: parseUUID(req.WorkspaceID)})
		if e != nil || uuidToString(current.AgentID) != req.AgentID || current.Status != "active" {
			infoFlowError(w, 404, "chat session not available")
			return
		}
		if len(req.AttachmentIDs) > 0 {
			ids, valid := parseUUIDSliceOrBadRequest(w, req.AttachmentIDs, "attachment_ids")
			if !valid {
				return
			}
			var staged int
			err = h.DB.QueryRow(r.Context(), `SELECT count(*) FROM attachment WHERE id=ANY($1::uuid[]) AND workspace_id=$2 AND chat_session_id=$3 AND uploader_type='member' AND uploader_id=$4 AND chat_message_id IS NULL AND issue_id IS NULL AND comment_id IS NULL AND task_id IS NULL AND source_context_id IS NULL`, ids, current.WorkspaceID, current.ID, current.CreatorID).Scan(&staged)
			if err != nil {
				infoFlowError(w, 503, "chat attachments unavailable")
				return
			}
			if staged != len(ids) {
				infoFlowError(w, 403, "invalid chat staging attachments")
				return
			}
			uploadScope = &infoFlowChatUploadScope{Session: current.ID, Stager: current.CreatorID}
		}
		encoded, _ = json.Marshal(map[string]any{"content": req.Content, "attachment_ids": req.AttachmentIDs})
	}
	inner := infoFlowMemberRequest(r.Context(), uuidToString(user.ID), req.WorkspaceID, "", encoded)
	ctx := middleware.SetMemberContext(inner.Context(), req.WorkspaceID, member)
	if uploadScope != nil {
		ctx = context.WithValue(ctx, infoFlowChatUploadKey{}, *uploadScope)
	}
	route := chi.NewRouteContext()
	route.URLParams.Add("sessionId", req.SessionID)
	ctx = context.WithValue(ctx, chi.RouteCtxKey, route)
	inner = inner.WithContext(ctx)
	if req.CreateIssue {
		h.CreateIssue(w, inner)
	} else {
		h.SendChatMessage(w, inner)
	}
}

// Only the trusted intake can separate a message author from the account used
// to stage its incoming images. Ordinary HTTP callers cannot set this context.
type infoFlowChatUploadKey struct{}
type infoFlowChatUploadScope struct{ Session, Stager pgtype.UUID }

func infoFlowChatAttachmentUploader(r *http.Request, session db.ChatSession, fallback pgtype.UUID) pgtype.UUID {
	scope, ok := r.Context().Value(infoFlowChatUploadKey{}).(infoFlowChatUploadScope)
	if ok && scope.Session == session.ID && scope.Stager == session.CreatorID {
		return scope.Stager
	}
	return fallback
}
