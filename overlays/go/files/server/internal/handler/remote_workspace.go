package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type ChatWorkspaceContext struct {
	Binding    string                        `json:"binding"`
	References []protocol.WorkspaceReference `json:"references"`
}

func (h *Handler) workspaceRuntime(w http.ResponseWriter, r *http.Request, session db.ChatSession, userID string) (string, bool) {
	agent, err := h.Queries.GetAgent(r.Context(), session.AgentID)
	if err != nil {
		writeError(w, 404, "workspace_unavailable")
		return "", false
	}
	// Chat visibility is not a disk grant. Both the machine and conversation must
	// belong to the caller. Shared transcripts must not receive private snapshots.
	if !remoteWorkspaceAgentAllowed(session, agent, userID) {
		writeError(w, 403, "workspace_requires_private_owned_agent")
		return "", false
	}
	runtimeID := session.RuntimeID
	if !runtimeID.Valid {
		runtimeID = agent.RuntimeID
	}
	if !runtimeID.Valid {
		writeError(w, 409, "workspace_not_ready")
		return "", false
	}
	rt, err := h.Queries.GetAgentRuntime(r.Context(), runtimeID)
	if err != nil || !remoteWorkspaceRuntimeAllowed(session, agent, rt, userID) {
		writeError(w, 403, "workspace_requires_machine_owner")
		return "", false
	}
	return uuidToString(runtimeID), true
}
func (h *Handler) callWorkspace(w http.ResponseWriter, r *http.Request, session db.ChatSession, userID string, req protocol.WorkspaceRequest) (protocol.WorkspaceResponse, bool) {
	runtimeID, ok := h.workspaceRuntime(w, r, session, userID)
	if !ok {
		return protocol.WorkspaceResponse{}, false
	}
	req.RuntimeID = runtimeID
	req.SessionID = uuidToString(session.ID)
	req.WorkspaceID = uuidToString(session.WorkspaceID)
	if h.DaemonHub == nil {
		writeError(w, 503, "workspace_unavailable")
		return protocol.WorkspaceResponse{}, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	response, err := h.DaemonHub.WorkspaceCall(ctx, runtimeID, userID, req)
	if err != nil {
		writeError(w, 503, err.Error())
		return response, false
	}
	if response.Error != "" {
		writeError(w, 409, response.Error)
		return response, false
	}
	// Recheck ACLs after the remote read and before disclosing any bytes.
	if !h.CanReadChatSession(r.Context(), userID, uuidToString(session.WorkspaceID), uuidToString(session.ID)) {
		writeError(w, 403, "workspace_access_revoked")
		return response, false
	}
	if _, ok := h.workspaceRuntime(w, r, session, userID); !ok {
		return response, false
	}
	w.Header().Set("Cache-Control", "no-store")
	return response, true
}
func (h *Handler) ChatWorkspace(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	session, ok := h.gateAccessibleChatSession(w, r, userID, ctxWorkspaceID(r.Context()), chi.URLParam(r, "sessionId"))
	if !ok {
		return
	}
	var req protocol.WorkspaceRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&req) != nil || (req.Operation != "list" && req.Operation != "read") || len(req.Path) > 4096 {
		writeError(w, 400, "invalid_workspace_request")
		return
	}
	response, ok := h.callWorkspace(w, r, session, userID, req)
	if ok {
		writeJSON(w, 200, response)
	}
}
func (h *Handler) resolveChatWorkspaceContext(w http.ResponseWriter, r *http.Request, session db.ChatSession, userID, content string, refs ChatWorkspaceContext) (string, bool) {
	if refs.Binding == "" || len(refs.References) == 0 || len(refs.References) > 12 {
		writeError(w, 400, "invalid_workspace_context")
		return "", false
	}
	for _, ref := range refs.References {
		if len(ref.Path) > 4096 || len(ref.Revision) > 64 {
			writeError(w, 400, "invalid_workspace_context")
			return "", false
		}
	}
	response, ok := h.callWorkspace(w, r, session, userID, protocol.WorkspaceRequest{Operation: "context", Binding: refs.Binding, References: refs.References})
	if !ok {
		return "", false
	}
	if len(response.Context) > 2<<20 {
		writeError(w, 413, "context_too_large")
		return "", false
	}
	return content + response.Context, true
}
