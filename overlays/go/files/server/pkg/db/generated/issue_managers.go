package db

import (
	"context"
	"github.com/jackc/pgx/v5/pgtype"
)

// Serialize deletion's cancellation phase against permission and workspace moves.
func (q *Queries) LockManagedIssueForHumanDeletion(ctx context.Context, issue, workspace, user pgtype.UUID) error {
	var id pgtype.UUID
	return q.db.QueryRow(ctx, `SELECT i.id FROM issue i JOIN issue_manager im ON im.issue_id=i.id AND im.user_id=$3 JOIN member m ON m.workspace_id=i.workspace_id AND m.user_id=$3 WHERE i.id=$1 AND i.workspace_id=$2 FOR UPDATE OF i FOR SHARE OF im,m`, issue, workspace, user).Scan(&id)
}

// Bind once per token. Reusing a token hash cannot replace its owner grant.
func (q *Queries) BindAgentOwnerReadScope(ctx context.Context, hash string, task, agent, owner, workspace pgtype.UUID) error {
	if _, err := q.db.Exec(ctx, `WITH stale AS(SELECT cap.token_hash FROM agent_token_read_scope cap WHERE cap.created_at<now()-interval '1 day' AND NOT EXISTS(SELECT 1 FROM task_token t WHERE t.token_hash=cap.token_hash AND t.expires_at>now()) LIMIT 100 FOR UPDATE SKIP LOCKED) DELETE FROM agent_token_read_scope WHERE token_hash IN(SELECT token_hash FROM stale)`); err != nil {
		return err
	}
	var bound pgtype.UUID
	return q.db.QueryRow(ctx, `INSERT INTO agent_token_read_scope(token_hash,task_id,agent_id,owner_id,workspace_id) VALUES($1,$2,$3,$4,$5) ON CONFLICT(token_hash) DO UPDATE SET token_hash=excluded.token_hash WHERE agent_token_read_scope.task_id=excluded.task_id AND agent_token_read_scope.agent_id=excluded.agent_id AND agent_token_read_scope.owner_id=excluded.owner_id AND agent_token_read_scope.workspace_id=excluded.workspace_id RETURNING owner_id`, hash, task, agent, owner, workspace).Scan(&bound)
}
