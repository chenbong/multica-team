package realtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/auth"
)

func TestAllowAllOriginsRequiresExplicitDeploymentOptIn(t *testing.T) {
	prev := allowedWSOrigins.Load().([]string)
	SetAllowedOrigins([]string{"https://known.example.test"})
	t.Cleanup(func() { SetAllowedOrigins(prev) })
	for _, value := range []string{"", "false", "1", "true", " TRUE "} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("MULTICA_WS_ALLOW_ALL_ORIGINS", value)
			for _, origin := range []string{"https://new.example.test", "http://192.0.2.1:8004", "null"} {
				r := httptest.NewRequest(http.MethodGet, "http://api.internal/ws", nil)
				r.Header.Set("Origin", origin)
				want := strings.EqualFold(strings.TrimSpace(value), "true")
				if checkOrigin(r) != want {
					t.Fatalf("origin %q with opt-in %q: wanted %v", origin, value, want)
				}
			}
		})
	}
}

type originPolicyMember struct{}

func (originPolicyMember) IsMember(_ context.Context, user, workspace string) bool {
	return user == testUserID && workspace == testWorkspaceID
}

type originPolicyScope struct{}

func (originPolicyScope) AuthorizeScope(_ context.Context, user, workspace, scope, id string) (bool, error) {
	return user == testUserID && workspace == testWorkspaceID && scope == ScopeChat && id == "allowed-chat", nil
}

func TestAllowAllOriginsPreservesAuthMembershipAndScopePermissions(t *testing.T) {
	t.Setenv("MULTICA_WS_ALLOW_ALL_ORIGINS", "true")
	hub := NewHub()
	hub.SetAuthorizer(originPolicyScope{})
	go hub.Run()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		HandleWebSocket(hub, originPolicyMember{}, staticPATResolver{"mul_origin_fixture": testUserID}, nil, w, r)
	}))
	defer server.Close()
	dial := func(workspace string) *websocket.Conn {
		t.Helper()
		url := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?workspace_id=" + workspace
		c, response, e := websocket.DefaultDialer.Dial(url, http.Header{"Origin": []string{"https://unlisted.example.test"}})
		if e != nil {
			t.Fatalf("upgrade failed: %v (response %v)", e, response)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	sendAuth := func(c *websocket.Conn, token string) string {
		t.Helper()
		if e := c.WriteJSON(map[string]any{"type": "auth", "payload": map[string]string{"token": token}}); e != nil {
			t.Fatal(e)
		}
		c.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, b, e := c.ReadMessage()
		if e != nil {
			t.Fatal(e)
		}
		return string(b)
	}
	invalid := dial(testWorkspaceID)
	if reply := sendAuth(invalid, "not-valid"); strings.Contains(reply, "auth_ack") {
		t.Fatal("invalid token authenticated")
	}
	foreign := dial("foreign-workspace")
	if reply := sendAuth(foreign, "mul_origin_fixture"); !strings.Contains(reply, "not a member") {
		t.Fatalf("membership gate missing: %s", reply)
	}
	// Cookie authentication must still fail before the upgrade.
	_, response, e := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/ws?workspace_id="+testWorkspaceID, http.Header{"Origin": []string{"https://unlisted.example.test"}, "Cookie": []string{auth.AuthCookieName + "=not-valid"}})
	if e == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatal("invalid cookie was admitted")
	}
	response.Body.Close()
	allowed := dial(testWorkspaceID)
	if reply := sendAuth(allowed, "mul_origin_fixture"); !strings.Contains(reply, "auth_ack") {
		t.Fatal("valid authentication missing")
	}
	for _, row := range []struct{ id, want string }{{"allowed-chat", "subscribe_ack"}, {"foreign-chat", "subscribe_error"}} {
		if e := allowed.WriteJSON(map[string]any{"type": "subscribe", "payload": map[string]string{"scope": ScopeChat, "id": row.id}}); e != nil {
			t.Fatal(e)
		}
		_, b, e := allowed.ReadMessage()
		if e != nil || !strings.Contains(string(b), row.want) {
			t.Fatalf("scope gate failed: %s %v", b, e)
		}
	}
	event := []byte(`{"type":"chat:message_added","payload":{"fixture":true}}`)
	hub.BroadcastToScope(ScopeChat, "allowed-chat", event)
	_, received, e := allowed.ReadMessage()
	if e != nil || string(received) != string(event) {
		t.Fatalf("authenticated realtime event missing: %s %v", received, e)
	}
}
