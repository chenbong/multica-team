package realtime

import (
	"context"
	"testing"
)

func TestChatDeliveryFilterChecksWorkspaceUserAndResourceRooms(t *testing.T) {
	h := NewHub()
	member := &Client{hub: h, userID: "member", workspaceID: "workspace", send: make(chan []byte, 8)}
	outsider := &Client{hub: h, userID: "outsider", workspaceID: "workspace", send: make(chan []byte, 8)}
	for _, key := range []scopeKey{sk(ScopeWorkspace, "workspace"), sk(ScopeUser, "member"), sk(ScopeChat, "chat"), sk(ScopeTask, "task")} {
		h.rooms[key] = map[*Client]bool{member: true, outsider: true}
	}
	allowed := true
	h.SetDeliveryFilter(func(_ context.Context, user, workspace string, _ []byte) bool {
		// This would deadlock if fanout held the hub read lock across the policy
		// callback. Database/policy work must not block disconnect cleanup.
		h.mu.Lock()
		h.mu.Unlock()
		return allowed && user == "member" && workspace == "workspace"
	})
	frame := []byte(`{"type":"chat:message","payload":{"chat_session_id":"chat"}}`)
	for scope, id := range map[string]string{ScopeWorkspace: "workspace", ScopeChat: "chat", ScopeTask: "task"} {
		h.BroadcastToScope(scope, id, frame)
		if len(member.send) != 1 || len(outsider.send) != 0 {
			t.Fatalf("bad recipient set in %s", scope)
		}
		<-member.send
	}
	h.SendToUser("member", frame)
	if len(member.send) != 1 || len(outsider.send) != 0 {
		t.Fatal("user-scope filter bypassed")
	}
	<-member.send
	allowed = false
	for scope, id := range map[string]string{ScopeWorkspace: "workspace", ScopeChat: "chat", ScopeTask: "task"} {
		h.BroadcastToScope(scope, id, frame)
	}
	h.SendToUser("member", frame)
	if len(member.send) != 0 || len(outsider.send) != 0 {
		t.Fatal("already subscribed revoked client received content")
	}
}
