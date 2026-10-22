package daemonws

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type workspacePending struct {
	client *client
	result chan protocol.WorkspaceResponse
}

// WorkspaceCall is intentionally connection-local. Multi-node installations must
// route this API to the daemon's node; no unauthenticated broadcast fallback.
func (h *Hub) WorkspaceCall(ctx context.Context, runtimeID, userID string, req protocol.WorkspaceRequest) (protocol.WorkspaceResponse, error) {
	h.mu.RLock()
	var chosen *client
	supported := false
	for c := range h.byRuntime[runtimeID] {
		// Runtime ownership is checked by HTTP. A daemon token has no UserID.
		if c.identity.UserID != "" && c.identity.UserID != userID {
			continue
		}
		for _, capability := range strings.Split(c.identity.Capabilities, ",") {
			if capability == protocol.RemoteWorkspaceCapability {
				if chosen != nil {
					h.mu.RUnlock()
					return protocol.WorkspaceResponse{}, errors.New("workspace_ambiguous_connection")
				}
				supported = true
				chosen = c
				break
			}
		}
	}
	h.mu.RUnlock()
	if !supported {
		return protocol.WorkspaceResponse{}, errors.New("workspace_unavailable_or_upgrade_required")
	}
	req.ID = uuid.NewString()
	pending := workspacePending{chosen, make(chan protocol.WorkspaceResponse, 1)}
	h.workspaceMu.Lock()
	if h.workspacePending == nil {
		h.workspacePending = make(map[string]workspacePending)
	}
	clientPending := 0
	for _, p := range h.workspacePending {
		if p.client == chosen {
			clientPending++
		}
	}
	if len(h.workspacePending) >= 64 || clientPending >= 8 {
		h.workspaceMu.Unlock()
		return protocol.WorkspaceResponse{}, errors.New("workspace_busy")
	}
	h.workspacePending[req.ID] = pending
	h.workspaceMu.Unlock()
	defer func() { h.workspaceMu.Lock(); delete(h.workspacePending, req.ID); h.workspaceMu.Unlock() }()
	payload, _ := json.Marshal(req)
	frame, _ := json.Marshal(protocol.Message{Type: protocol.RemoteWorkspaceRequestEvent, Payload: payload})
	if !chosen.allowsRuntime(runtimeID) {
		return protocol.WorkspaceResponse{}, errors.New("workspace_disconnected")
	}
	if !chosen.trySend(frame) {
		return protocol.WorkspaceResponse{}, errors.New("workspace_disconnected")
	}
	select {
	case response := <-pending.result:
		return response, nil
	case <-ctx.Done():
		return protocol.WorkspaceResponse{}, errors.New("workspace_timeout")
	case <-chosen.ctx.Done():
		return protocol.WorkspaceResponse{}, errors.New("workspace_disconnected")
	}
}
func (c *client) handleWorkspaceResponse(raw json.RawMessage) {
	if len(raw) > 2<<20 {
		return
	}
	var response protocol.WorkspaceResponse
	if json.Unmarshal(raw, &response) != nil {
		return
	}
	c.hub.workspaceMu.Lock()
	pending, ok := c.hub.workspacePending[response.ID]
	c.hub.workspaceMu.Unlock()
	if !ok || pending.client != c {
		return
	}
	select {
	case pending.result <- response:
	default:
	}
}
