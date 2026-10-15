package main

import (
	"context"
	"testing"

	"github.com/multica-ai/multica/server/internal/realtime"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestSharedChatScopeUsesCurrentAgentAccess(t *testing.T) {
	ws, wsID := mustUUID(t)
	creator, creatorID := mustUUID(t)
	peer, _ := mustUUID(t)
	chat, chatID := mustUUID(t)
	task, taskID := mustUUID(t)
	otherWS, _ := mustUUID(t)
	q := &fakeScopeQuerier{
		sessions: map[[16]byte]db.ChatSession{chatID.Bytes: {ID: chatID, WorkspaceID: wsID, CreatorID: creatorID}},
		tasks:    map[[16]byte]db.AgentTaskQueue{taskID.Bytes: {ID: taskID, ChatSessionID: chatID}},
	}
	allowed := map[string]bool{peer: true}
	a := newChatScopeAuthorizer(q, func(_ context.Context, userID, workspaceID, sessionID string) bool {
		return workspaceID == ws && sessionID == chat && allowed[userID]
	})
	for scope, id := range map[string]string{realtime.ScopeChat: chat, realtime.ScopeTask: task} {
		ok, err := a.AuthorizeScope(context.Background(), peer, ws, scope, id)
		if err != nil || !ok {
			t.Fatalf("permitted peer %s: %v %v", scope, ok, err)
		}
		ok, err = a.AuthorizeScope(context.Background(), creator, ws, scope, id)
		if err != nil || ok {
			t.Fatalf("creator without agent access %s: %v %v", scope, ok, err)
		}
		ok, err = a.AuthorizeScope(context.Background(), peer, otherWS, scope, id)
		if err != nil || ok {
			t.Fatalf("crossworkspace %s: %v %v", scope, ok, err)
		}
	}
	delete(allowed, peer)
	if ok, _ := a.AuthorizeScope(context.Background(), peer, ws, realtime.ScopeChat, chat); ok {
		t.Fatal("revocation was not rechecked")
	}
}
