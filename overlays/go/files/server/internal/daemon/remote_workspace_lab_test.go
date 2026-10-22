package daemon

// Opt-in interactive lab, compiled ONLY into the test binary. It reuses the
// production Hub and daemon filesystem handlers in TWO OS processes. It does
// not simulate a model or exercise production database authentication.
import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestRemoteWorkspaceInteractiveLab(t *testing.T) {
	role := os.Getenv("TEAMMESH_LAB_ROLE")
	if role == "" {
		t.Skip("opt-in two-process interactive lab")
	}
	token := os.Getenv("TEAMMESH_LAB_TOKEN")
	if len(token) < 32 {
		t.Fatal("lab token must be at least 32 characters")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if role == "server" {
		runWorkspaceLabServer(t, ctx, token)
		return
	}
	if role == "daemon" {
		runWorkspaceLabDaemon(t, ctx, token)
		return
	}
	t.Fatal("unknown lab role")
}
func workspaceLabJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func runWorkspaceLabServer(t *testing.T, ctx context.Context, token string) {
	hub := daemonws.NewHub()
	mux := http.NewServeMux()
	authorize := func(w http.ResponseWriter, r *http.Request) bool {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			workspaceLabJSON(w, 401, map[string]string{"error": "lab_unauthorized"})
			return false
		}
		return true
	}
	mux.HandleFunc("/lab/daemon", func(w http.ResponseWriter, r *http.Request) {
		if !authorize(w, r) {
			return
		}
		hub.HandleWebSocket(w, r, daemonws.ClientIdentity{UserID: "lab-owner", WorkspaceID: "lab-workspace", RuntimeIDs: []string{"lab-runtime"}, Capabilities: protocol.RemoteWorkspaceCapability})
	})
	mux.HandleFunc("/lab/workspace", func(w http.ResponseWriter, r *http.Request) {
		if !authorize(w, r) {
			return
		}
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		var req protocol.WorkspaceRequest
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&req) != nil {
			w.WriteHeader(400)
			return
		}
		if req.SessionID != "lab-chat-one" && req.SessionID != "lab-chat-two" {
			workspaceLabJSON(w, 403, map[string]string{"error": "lab_session_denied"})
			return
		}
		req.RuntimeID = "lab-runtime"
		req.WorkspaceID = "lab-workspace"
		callCtx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		response, err := hub.WorkspaceCall(callCtx, "lab-runtime", "lab-owner", req)
		if err != nil {
			workspaceLabJSON(w, 503, map[string]string{"error": err.Error()})
			return
		}
		if response.Error != "" {
			workspaceLabJSON(w, 409, map[string]string{"error": response.Error})
			return
		}
		workspaceLabJSON(w, 200, response)
	})
	// No root path is configured in the server. Only the separate daemon knows it.
	listener, err := net.Listen("tcp", "127.0.0.1:4181")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { <-ctx.Done(); _ = server.Close() }()
	fmt.Println("LAB SERVER 127.0.0.1:4181 — relay only; no filesystem root")
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		t.Error(err)
	}
}
func runWorkspaceLabDaemon(t *testing.T, ctx context.Context, token string) {
	root := os.Getenv("TEAMMESH_LAB_ROOT")
	if !filepath.IsAbs(root) {
		t.Fatal("lab root must be explicit and absolute")
	}
	// Script creates the sandbox. Refuse arbitrary pre-existing directories.
	if data, err := os.ReadFile(filepath.Join(root, ".teammesh-lab")); err != nil || string(data) != token {
		t.Fatal("missing lab sandbox marker")
	}
	d := &Daemon{wsRPC: newWSRPCClient(time.Second)}
	for _, session := range []string{"lab-chat-one", "lab-chat-two"} {
		directory := filepath.Join(root, session)
		if err := os.MkdirAll(filepath.Join(directory, "src", "auth"), 0700); err != nil {
			t.Fatal(err)
		}
		for path, body := range map[string]string{
			"README.md":         "# Live daemon filesystem\n\nThis file is read from disk, not a browser fixture.\nSession: " + session + "\n",
			"src/auth/login.ts": "// Edit this file on disk and refresh the browser.\nexport const verification = \"LIVE-" + session + "\";\nexport function login() {\n  return verification;\n}\n",
			".env":              "FAKE_SECRET=must_not_be_visible\n",
		} {
			file := filepath.Join(directory, filepath.FromSlash(path))
			if _, err := os.Stat(file); os.IsNotExist(err) {
				if err := os.WriteFile(file, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
		}
		d.registerRemoteWorkspace(Task{ChatSessionID: session, WorkspaceID: "lab-workspace", RuntimeID: "lab-runtime"}, directory)
	}
	fmt.Println("LAB DAEMON ROOT", root)
	for ctx.Err() == nil {
		header := http.Header{}
		header.Set("Authorization", "Bearer "+token)
		conn, _, err := websocket.DefaultDialer.DialContext(ctx, "ws://127.0.0.1:4181/lab/daemon", header)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
				continue
			}
		}
		conn.SetReadLimit(2 << 20)
		// Use the production serialized writer and generation-aware response path.
		writes := make(chan *wsOutbound, 16)
		writerDone := make(chan struct{})
		go d.runWSWriter(conn, writes, writerDone)
		generation := d.wsRPC.attach(func(frame []byte) (*wsOutbound, error) {
			item := &wsOutbound{data: frame}
			select {
			case writes <- item:
				return item, nil
			default:
				return nil, errWSRPCWriteBufferFull
			}
		})
		connectionDone := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				conn.Close()
			case <-connectionDone:
			}
		}()
		for ctx.Err() == nil {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				break
			}
			var message protocol.Message
			if json.Unmarshal(raw, &message) == nil && strings.EqualFold(message.Type, protocol.RemoteWorkspaceRequestEvent) {
				d.dispatchRemoteWorkspace(message.Payload, generation)
			}
		}
		close(connectionDone)
		d.wsRPC.attach(nil)
		close(writes)
		conn.Close()
		<-writerDone
	}
}
