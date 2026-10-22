package handler

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestRemoteWorkspacePolicy(t *testing.T) {
	owner := workspaceTestUUID("11111111-1111-4111-8111-111111111111")
	peer := workspaceTestUUID("22222222-2222-4222-8222-222222222222")
	ws := workspaceTestUUID("33333333-3333-4333-8333-333333333333")
	agentID := workspaceTestUUID("44444444-4444-4444-8444-444444444444")
	rt := workspaceTestUUID("55555555-5555-4555-8555-555555555555")
	session := db.ChatSession{CreatorID: owner, WorkspaceID: ws, AgentID: agentID, RuntimeID: rt}
	agent := db.Agent{ID: agentID, WorkspaceID: ws, OwnerID: owner, PermissionMode: "private", RuntimeID: rt}
	runtime := db.AgentRuntime{ID: rt, WorkspaceID: ws, OwnerID: owner}
	user := workspacePolicyUUID(owner)
	if !remoteWorkspaceAgentAllowed(session, agent, user) || !remoteWorkspaceRuntimeAllowed(session, agent, runtime, user) {
		t.Fatal("valid owner denied")
	}
	for _, change := range []func(*db.ChatSession, *db.Agent){
		func(s *db.ChatSession, a *db.Agent) { s.CreatorID = peer },
		func(s *db.ChatSession, a *db.Agent) { a.OwnerID = peer },
		func(s *db.ChatSession, a *db.Agent) { a.PermissionMode = "public_to" },
		func(s *db.ChatSession, a *db.Agent) { a.WorkspaceID = peer },
		func(s *db.ChatSession, a *db.Agent) { s.AgentID = peer },
	} {
		s, a := session, agent
		change(&s, &a)
		if remoteWorkspaceAgentAllowed(s, a, user) {
			t.Fatal("disk access granted without private ownership")
		}
	}
	for _, change := range []func(*db.ChatSession, *db.Agent, *db.AgentRuntime){
		func(s *db.ChatSession, a *db.Agent, r *db.AgentRuntime) { r.OwnerID = peer },
		func(s *db.ChatSession, a *db.Agent, r *db.AgentRuntime) { r.WorkspaceID = peer },
		func(s *db.ChatSession, a *db.Agent, r *db.AgentRuntime) { a.RuntimeID = peer },
		func(s *db.ChatSession, a *db.Agent, r *db.AgentRuntime) { s.RuntimeID = peer },
	} {
		s, a, r := session, agent, runtime
		change(&s, &a, &r)
		if remoteWorkspaceRuntimeAllowed(s, a, r, user) {
			t.Fatal("cross runtime access granted")
		}
	}
	if remoteWorkspaceAgentAllowed(session, agent, "") || remoteWorkspaceRuntimeAllowed(session, agent, runtime, "") {
		t.Fatal("anonymous access")
	}
}

func workspaceTestUUID(value string) pgtype.UUID {
	return pgtype.UUID{Bytes: uuid.MustParse(value), Valid: true}
}
