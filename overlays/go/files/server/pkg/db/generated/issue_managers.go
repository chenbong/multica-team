package db

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// CanManageIssueByCreator is shared by deletion and cross-workspace transfer.
// Agent-created issues belong to the agent's current owner, including archived
// agents. A missing agent never grants authority to a guessed owner.
func (q *Queries) CanManageIssueByCreator(ctx context.Context, issue, workspace, user pgtype.UUID) (bool, error) {
	var allowed bool
	err := q.db.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM issue i JOIN member m ON m.workspace_id=i.workspace_id AND m.user_id=$3
		WHERE i.id=$1 AND i.workspace_id=$2 AND (m.role IN ('owner','admin')
		 OR (i.creator_type='member' AND i.creator_id=$3)
		 OR (i.creator_type='agent' AND EXISTS (SELECT 1 FROM agent a WHERE a.id=i.creator_id AND a.owner_id=$3)))
	)`, issue, workspace, user).Scan(&allowed)
	return allowed, err
}

// Lock the issue, actor membership, and (when needed) agent owner until the
// caller's transaction ends. An agent disappearing or changing owner cannot
// turn a stale authorization check into an accepted delete.
func (q *Queries) LockManagedIssueForHumanDeletion(ctx context.Context, issue, workspace, user pgtype.UUID) error {
	var kind, role string
	var creator pgtype.UUID
	err := q.db.QueryRow(ctx, `SELECT i.creator_type,i.creator_id,m.role FROM issue i
		JOIN member m ON m.workspace_id=i.workspace_id AND m.user_id=$3
		WHERE i.id=$1 AND i.workspace_id=$2 FOR UPDATE OF i FOR SHARE OF m`, issue, workspace, user).Scan(&kind, &creator, &role)
	if err != nil {
		return err
	}
	if role == "owner" || role == "admin" || (kind == "member" && creator == user) {
		return nil
	}
	if kind == "agent" {
		var id pgtype.UUID
		return q.db.QueryRow(ctx, `SELECT id FROM agent WHERE id=$1 AND owner_id=$2 FOR SHARE`, creator, user).Scan(&id)
	}
	return pgx.ErrNoRows
}

// Bind once per token. Reusing a token hash cannot replace its owner grant.
func (q *Queries) BindAgentOwnerReadScope(ctx context.Context, hash string, task, agent, owner, workspace pgtype.UUID) error {
	if _, err := q.db.Exec(ctx, `WITH stale AS(SELECT cap.token_hash FROM agent_token_read_scope cap WHERE cap.created_at<now()-interval '1 day' AND NOT EXISTS(SELECT 1 FROM task_token t WHERE t.token_hash=cap.token_hash AND t.expires_at>now()) LIMIT 100 FOR UPDATE SKIP LOCKED) DELETE FROM agent_token_read_scope WHERE token_hash IN(SELECT token_hash FROM stale)`); err != nil {
		return err
	}
	var bound pgtype.UUID
	return q.db.QueryRow(ctx, `INSERT INTO agent_token_read_scope(token_hash,task_id,agent_id,owner_id,workspace_id) VALUES($1,$2,$3,$4,$5) ON CONFLICT(token_hash) DO UPDATE SET token_hash=excluded.token_hash WHERE agent_token_read_scope.task_id=excluded.task_id AND agent_token_read_scope.agent_id=excluded.agent_id AND agent_token_read_scope.owner_id=excluded.owner_id AND agent_token_read_scope.workspace_id=excluded.workspace_id RETURNING owner_id`, hash, task, agent, owner, workspace).Scan(&bound)
}
