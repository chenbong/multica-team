package handler

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
)

// The ordinary enqueue path catches unique-slot races and merges/defer inputs.
// Under a relay outer transaction a bare failed INSERT would poison the whole
// comment transaction. Isolate that specific sqlc operation in a savepoint,
// including when the service itself opened a nested transaction.
type infoFlowSavepointTx struct{ pgx.Tx }

func (tx *infoFlowSavepointTx) Begin(ctx context.Context) (pgx.Tx, error) {
	child, err := tx.Tx.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &infoFlowSavepointTx{Tx: child}, nil
}

func (tx *infoFlowSavepointTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if !strings.HasPrefix(sql, "-- name: CreateAgentTask :one") {
		return tx.Tx.QueryRow(ctx, sql, args...)
	}
	child, err := tx.Tx.Begin(ctx)
	if err != nil {
		return infoFlowErrorRow{err}
	}
	return &infoFlowSavepointRow{row: child.QueryRow(ctx, sql, args...), tx: child, ctx: ctx}
}

type infoFlowErrorRow struct{ err error }

func (r infoFlowErrorRow) Scan(...any) error { return r.err }

type infoFlowSavepointRow struct {
	row pgx.Row
	tx  pgx.Tx
	ctx context.Context
}

func (r *infoFlowSavepointRow) Scan(out ...any) error {
	if err := r.row.Scan(out...); err != nil {
		_ = r.tx.Rollback(r.ctx)
		return err
	}
	return r.tx.Commit(r.ctx)
}
