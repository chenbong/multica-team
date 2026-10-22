package handler

import (
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Kept pure so deny-by-default disk policy can be tested without a database.
func remoteWorkspaceAgentAllowed(session db.ChatSession, agent db.Agent, userID string) bool {
	return userID != "" && workspacePolicyUUID(session.CreatorID) == userID && workspacePolicyUUID(agent.OwnerID) == userID && agent.PermissionMode == "private" && session.WorkspaceID == agent.WorkspaceID && session.AgentID == agent.ID
}
func remoteWorkspaceRuntimeAllowed(session db.ChatSession, agent db.Agent, runtime db.AgentRuntime, userID string) bool {
	if userID == "" || workspacePolicyUUID(runtime.OwnerID) != userID || runtime.WorkspaceID != session.WorkspaceID {
		return false
	}
	// Reassigning an agent must never attach old-machine paths to a new-machine run.
	if !agent.RuntimeID.Valid || agent.RuntimeID != runtime.ID {
		return false
	}
	return !session.RuntimeID.Valid || session.RuntimeID == runtime.ID
}

func workspacePolicyUUID(value pgtype.UUID) string {
	if !value.Valid {
		return ""
	}
	return uuid.UUID(value.Bytes).String()
}
