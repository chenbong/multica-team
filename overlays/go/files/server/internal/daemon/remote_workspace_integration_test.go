package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Real websocket -> daemon reader -> filesystem -> websocket response. No model,
// DB, public ports or production machine is involved in this deterministic test.
func TestRemoteWorkspaceEndToEnd(t *testing.T) {
	hub := daemonws.NewHub()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, daemonws.ClientIdentity{UserID: "owner", RuntimeIDs: []string{"rt"}, Capabilities: protocol.RemoteWorkspaceCapability})
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	d := &Daemon{wsRPC: newWSRPCClient(time.Second)}
	root := workspaceFixture(t)
	d.registerRemoteWorkspace(Task{ChatSessionID: "session", WorkspaceID: "ws", RuntimeID: "rt"}, root.Path)
	generation := d.wsRPC.attach(func(frame []byte) (*wsOutbound, error) {
		err := conn.WriteMessage(websocket.TextMessage, frame)
		return &wsOutbound{data: frame}, err
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg protocol.Message
			if json.Unmarshal(raw, &msg) == nil && msg.Type == protocol.RemoteWorkspaceRequestEvent {
				d.dispatchRemoteWorkspace(msg.Payload, generation)
			}
		}
	}()
	t.Cleanup(func() { conn.Close(); <-done })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req := protocol.WorkspaceRequest{WorkspaceID: "ws", RuntimeID: "rt", SessionID: "session", Operation: "list"}
	listed, err := hub.WorkspaceCall(ctx, "rt", "owner", req)
	if err != nil || listed.Error != "" || listed.Binding == "" {
		t.Fatalf("list: %+v %v", listed, err)
	}
	req.Operation = "read"
	req.Binding = listed.Binding
	req.Path = "main.go"
	file, err := hub.WorkspaceCall(ctx, "rt", "owner", req)
	if err != nil || file.Revision == "" {
		t.Fatalf("read: %+v %v", file, err)
	}
	req.Operation = "context"
	req.References = []protocol.WorkspaceReference{{Path: "main.go", Kind: "file", Revision: file.Revision, StartLine: 2, EndLine: 2}}
	attached, err := hub.WorkspaceCall(ctx, "rt", "owner", req)
	if err != nil || !strings.Contains(attached.Context, "func main()") || strings.Contains(attached.Context, "package main") {
		t.Fatalf("context: %+v %v", attached, err)
	}
}
