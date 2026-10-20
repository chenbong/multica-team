package handler

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

const infoFlowRequestCancelled = "request_cancelled"

type infoFlowWebActor struct {
	Workspace   string
	Issue, User pgtype.UUID
}

type infoFlowActionError struct {
	Status  int
	Message string
}

func (e *infoFlowActionError) Error() string { return e.Message }
func infoFlowActionFailure(status int, message string) error {
	return &infoFlowActionError{status, message}
}
func infoFlowWriteActionError(w http.ResponseWriter, err error) {
	var failure *infoFlowActionError
	if errors.As(err, &failure) {
		writeError(w, failure.Status, failure.Message)
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "issue not found")
		return
	}
	writeError(w, 500, "Failed to update conversation binding")
}

func infoFlowWebRequestTable(kind string) (string, error) {
	switch kind {
	case "group":
		return "infoflow_web_group_request", nil
	case "private":
		return "infoflow_web_private_request", nil
	default:
		return "", infoFlowActionFailure(400, "Invalid conversation kind")
	}
}

func infoFlowPrivateWebLock(ctx context.Context, tx pgx.Tx, issue, user pgtype.UUID) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "infoflow-web-private:"+uuidToString(issue))
	return err
}

// All explicit binding changes invalidate pending admission in the SAME
// transaction. Snapshot equality alone cannot detect empty -> bound -> empty.
// Table names come only from the closed kind switch above, never request text.
func infoFlowInvalidateWebRequest(ctx context.Context, tx pgx.Tx, kind string, actor infoFlowWebActor, reason string) error {
	table, err := infoFlowWebRequestTable(kind)
	if err != nil {
		return err
	}
	query := `UPDATE ` + table + ` SET state='failed',reason=$2,generation=$3,lease_token=NULL,lease_robot_id=NULL,lease_agent_id=NULL,lease_until=NULL,updated_at=clock_timestamp() WHERE issue_id=$1 AND workspace_id=$4 AND state IN('queued','pending')`
	args := []any{actor.Issue, reason, dbid.NewV7(), actor.Workspace}
	if kind == "private" {
		if err = infoFlowPrivateWebLock(ctx, tx, actor.Issue, actor.User); err != nil {
			return err
		}
		query += ` AND requester_id=$5`
		args = append(args, actor.User)
	}
	_, err = tx.Exec(ctx, query, args...)
	return err
}

func infoFlowWebRequestPending(state string, expires time.Time) bool {
	return (state == "queued" || state == "pending") && expires.After(time.Now())
}

func infoFlowWebLeaseCurrent(state string, generation, token pgtype.UUID, until pgtype.Timestamptz, expires time.Time, expectedGeneration, expectedToken pgtype.UUID) bool {
	now := time.Now()
	return state == "pending" && generation.Valid && token.Valid && generation == expectedGeneration && token == expectedToken && until.Valid && until.Time.After(now) && expires.After(now)
}

// The caller must be a current human member. Cancellation never needs agent
// invocation or a still-valid assignee: it grants no binding or execution.
func (h *Handler) infoFlowCanCancelWebRequest(ctx context.Context, tx pgx.Tx, actor infoFlowWebActor, kind string, requester pgtype.UUID) (bool, error) {
	var role, email string
	err := tx.QueryRow(ctx, `SELECT m.role,u.email FROM member m JOIN "user" u ON u.id=m.user_id WHERE m.workspace_id=$1 AND m.user_id=$2 FOR SHARE OF m,u`, actor.Workspace, actor.User).Scan(&role, &email)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if auth.IsTemporarilyDisabledUser(uuidToString(actor.User), email) {
		return false, nil
	}
	if requester == actor.User {
		return true, nil
	}
	if kind == "private" {
		return false, nil
	}
	if role == "owner" || role == "admin" {
		return true, nil
	}
	b, _, err := infoFlowWebBinding(ctx, tx, actor.Workspace, actor.Issue)
	if err != nil {
		return false, err
	}
	sh, _ := h.infoFlowScoped(tx)
	_, _, manage, err := sh.infoFlowWebAuthority(ctx, actor.Workspace, actor.Issue, actor.User, b)
	return manage, err
}

// An exact request ID AND generation fence an old tab's cancel against a newer
// submission. Both kinds share invalidation, lease clearing and terminal state.
func (h *Handler) infoFlowCancelWebRequest(ctx context.Context, actor infoFlowWebActor, kind string, id, expected pgtype.UUID) (infoFlowConversationsView, error) {
	var out infoFlowConversationsView
	table, err := infoFlowWebRequestTable(kind)
	if err != nil {
		return out, err
	}
	var group string
	if kind == "group" {
		err = h.DB.QueryRow(ctx, `SELECT group_id FROM infoflow_web_group_request WHERE id=$1 AND issue_id=$2 AND workspace_id=$3`, id, actor.Issue, actor.Workspace).Scan(&group)
		if err != nil {
			return out, err
		}
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	if kind == "group" {
		err = infoFlowWebLock(ctx, tx, actor.Workspace, group, actor.Issue)
	} else {
		err = infoFlowPrivateWebLock(ctx, tx, actor.Issue, actor.User)
	}
	if err != nil {
		return out, err
	}
	var live pgtype.UUID
	if err = tx.QueryRow(ctx, `SELECT id FROM issue WHERE id=$1 AND workspace_id=$2 FOR SHARE`, actor.Issue, actor.Workspace).Scan(&live); err != nil {
		return out, err
	}
	var requester, generation pgtype.UUID
	var state, reason string
	err = tx.QueryRow(ctx, `SELECT requester_id,generation,state,reason FROM `+table+` WHERE id=$1 AND issue_id=$2 AND workspace_id=$3 FOR UPDATE`, id, actor.Issue, actor.Workspace).Scan(&requester, &generation, &state, &reason)
	if err != nil {
		return out, err
	}
	allowed, err := h.infoFlowCanCancelWebRequest(ctx, tx, actor, kind, requester)
	if err != nil {
		return out, err
	}
	if !allowed {
		return out, infoFlowActionFailure(403, "Cannot cancel this conversation request")
	}
	// Already cancelled is an idempotent no-op. A later live submission has a
	// fresh generation and MUST still reject the earlier cancel below.
	if !(state == "failed" && reason == infoFlowRequestCancelled) {
		if generation != expected || state != "queued" && state != "pending" {
			return out, infoFlowActionFailure(409, "Request changed; refresh before cancelling")
		}
		if err = infoFlowInvalidateWebRequest(ctx, tx, kind, infoFlowWebActor{actor.Workspace, actor.Issue, requester}, infoFlowRequestCancelled); err != nil {
			return out, err
		}
	}
	out, err = h.infoFlowConversationView(ctx, tx, actor.Workspace, actor.Issue, actor.User)
	if err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, err
	}
	return out, nil
}
