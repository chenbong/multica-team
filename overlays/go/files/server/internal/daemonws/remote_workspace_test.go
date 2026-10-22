package daemonws

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestRemoteWorkspaceRoundTrip(t *testing.T) {
	hub := NewHub()
	conn := dialRPCTestConn(t, hub, ClientIdentity{UserID: "owner", RuntimeIDs: []string{"rt"}, Capabilities: protocol.RemoteWorkspaceCapability})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := make(chan protocol.WorkspaceResponse, 1)
	errs := make(chan error, 1)
	go func() {
		r, e := hub.WorkspaceCall(ctx, "rt", "owner", protocol.WorkspaceRequest{SessionID: "session", Operation: "read", Path: "main.go"})
		result <- r
		errs <- e
	}()
	conn.SetReadDeadline(time.Now().Add(time.Second))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var message protocol.Message
	json.Unmarshal(raw, &message)
	if message.Type != protocol.RemoteWorkspaceRequestEvent {
		t.Fatal(message.Type)
	}
	var request protocol.WorkspaceRequest
	json.Unmarshal(message.Payload, &request)
	if request.Path != "main.go" || request.SessionID != "session" {
		t.Fatal(request)
	}
	payload, _ := json.Marshal(protocol.WorkspaceResponse{ID: request.ID, Content: strings.Repeat("x", 128<<10)})
	conn.WriteJSON(protocol.Message{Type: protocol.RemoteWorkspaceResponseEvent, Payload: payload})
	if response := <-result; len(response.Content) != 128<<10 {
		t.Fatal(response)
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	hub.workspaceMu.Lock()
	defer hub.workspaceMu.Unlock()
	if len(hub.workspacePending) != 0 {
		t.Fatal("request leaked")
	}
}
func TestRemoteWorkspaceOwnershipCapabilityTimeout(t *testing.T) {
	for _, test := range []struct{ name, user, caps string }{{"wrong owner", "other", protocol.RemoteWorkspaceCapability}, {"old daemon", "owner", ""}} {
		t.Run(test.name, func(t *testing.T) {
			hub := NewHub()
			dialRPCTestConn(t, hub, ClientIdentity{UserID: "owner", RuntimeIDs: []string{"rt"}, Capabilities: test.caps})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err := hub.WorkspaceCall(ctx, "rt", test.user, protocol.WorkspaceRequest{}); err == nil {
				t.Fatal("unauthorized request accepted")
			}
		})
	}
	hub := NewHub()
	dialRPCTestConn(t, hub, ClientIdentity{UserID: "owner", RuntimeIDs: []string{"rt"}, Capabilities: protocol.RemoteWorkspaceCapability})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := hub.WorkspaceCall(ctx, "rt", "owner", protocol.WorkspaceRequest{}); err == nil || err.Error() != "workspace_timeout" {
		t.Fatal(err)
	}
	hub.workspaceMu.Lock()
	defer hub.workspaceMu.Unlock()
	if len(hub.workspacePending) != 0 {
		t.Fatal("timeout leaked")
	}
}
func TestRemoteWorkspaceForgedResponse(t *testing.T) {
	hub := NewHub()
	good := &client{hub: hub}
	bad := &client{hub: hub}
	pending := workspacePending{client: good, result: make(chan protocol.WorkspaceResponse, 1)}
	hub.workspacePending = map[string]workspacePending{"request": pending}
	raw := json.RawMessage(`{"id":"request","content":"secret"}`)
	bad.handleWorkspaceResponse(raw)
	select {
	case <-pending.result:
		t.Fatal("cross connection reply accepted")
	default:
	}
	good.handleWorkspaceResponse(raw)
	select {
	case <-pending.result:
	default:
		t.Fatal("valid response dropped")
	}
}
