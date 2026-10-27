package service

import (
	"context"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// The transfer transaction already persisted cancellation and settled receipts.
// Publish notifications only after it commits; do not cancel a second time.
func (s *TaskService) FinalizeTransferredTaskCancellations(ctx context.Context, tasks []db.AgentTaskQueue) {
	for _, task := range tasks {
		s.captureTaskCancelled(ctx, task)
		s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, task)
	}
	for _, agent := range distinctAgentIDs(tasks) {
		s.ReconcileAgentStatus(ctx, agent)
	}
	s.notifyTasksFinished(tasks)
}

type issueDeleteCancellationScope struct{ workspace, user pgtype.UUID }

func (s *TaskService) CancelTasksForIssue(ctx context.Context, issue pgtype.UUID) error {
	return s.cancelTasksForIssue(ctx, issue, nil)
}
func (s *TaskService) CancelIssueForHumanDeletion(ctx context.Context, issue, workspace, user pgtype.UUID) error {
	return s.cancelTasksForIssue(ctx, issue, &issueDeleteCancellationScope{workspace: workspace, user: user})
}
