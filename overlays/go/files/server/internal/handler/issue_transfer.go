package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type issueTransferRequest struct {
	TargetWorkspaceID string `json:"target_workspace_id"`
	TargetStatus      string `json:"target_status"`
	Fingerprint       string `json:"fingerprint"`
	ConfirmUnassign   bool   `json:"confirm_unassign"`
}

type issueTransferStatus struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type issueTransferPlan struct {
	IssueID              string                `json:"issue_id"`
	SourceWorkspaceID    string                `json:"source_workspace_id"`
	TargetWorkspaceID    string                `json:"target_workspace_id"`
	SourceIdentifier     string                `json:"source_identifier"`
	TargetName           string                `json:"target_name"`
	TargetSlug           string                `json:"target_slug"`
	TargetPrefix         string                `json:"target_prefix"`
	TargetStatus         string                `json:"target_status"`
	Statuses             []issueTransferStatus `json:"statuses"`
	Revision             int64                 `json:"revision"`
	Comments             int                   `json:"comments"`
	Runs                 int                   `json:"runs"`
	Messages             int                   `json:"messages"`
	Unassign             bool                  `json:"unassign"`
	RetiredSubscriptions int                   `json:"retired_subscriptions"`
	Blockers             []string              `json:"blockers"`
	Fingerprint          string                `json:"fingerprint"`
	Identifier           string                `json:"identifier,omitempty"`
	Path                 string                `json:"path,omitempty"`
}

type issueTransferFailure struct {
	status        int
	code, message string
}

func (e *issueTransferFailure) Error() string { return e.message }
func transferFailure(status int, code, message string) error {
	return &issueTransferFailure{status, code, message}
}

// Deliberately separate from board /move: cross-workspace transfer has a
// different permission boundary and must never dispatch an agent or plugin.
func (h *Handler) PreviewIssueTransfer(w http.ResponseWriter, r *http.Request) {
	h.issueTransfer(w, r, false)
}
func (h *Handler) TransferIssue(w http.ResponseWriter, r *http.Request) { h.issueTransfer(w, r, true) }

func (h *Handler) issueTransfer(w http.ResponseWriter, r *http.Request, apply bool) {
	if isMachineCredentialActor(r) || r.Header.Get("X-Agent-ID") != "" || r.Header.Get("X-Daemon-ID") != "" {
		writeError(w, 403, "only a human can move issues between workspaces")
		return
	}
	user, ok := requireUserID(w, r)
	if !ok {
		return
	}
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	var req issueTransferRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, 400, "invalid transfer request")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, 400, "invalid transfer request")
		return
	}
	target, ok := parseUUIDOrBadRequest(w, req.TargetWorkspaceID, "target_workspace_id")
	if !ok {
		return
	}
	actor, ok := parseUUIDOrBadRequest(w, user, "user_id")
	if !ok {
		return
	}
	plan, err := h.runIssueTransfer(r.Context(), issue.ID, issue.WorkspaceID, target, actor, req, apply)
	if err != nil {
		var failure *issueTransferFailure
		if errors.As(err, &failure) {
			writeJSON(w, failure.status, map[string]string{"code": failure.code, "error": failure.message})
			return
		}
		writeJSON(w, 409, map[string]string{"code": "transfer_retry", "error": "Transfer could not finish safely. Refresh the preview and retry."})
		return
	}
	if apply {
		// A dedicated event avoids the issue-created/deleted plugin and dispatch
		// hooks. Each audience receives only the ID it was already allowed to see.
		for _, ws := range []string{plan.SourceWorkspaceID, plan.TargetWorkspaceID} {
			h.publish("issue:workspace_changed", ws, "member", user, map[string]string{"issue_id": plan.IssueID})
		}
	}
	writeJSON(w, 200, plan)
}

// No schema migration is needed. Old task IDs, messages, actors and results
// remain immutable; user-facing run access resolves through the owning issue.
func (h *Handler) runIssueTransfer(ctx context.Context, id, source, target, actor pgtype.UUID, req issueTransferRequest, apply bool) (*issueTransferPlan, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if source == target {
		return nil, transferFailure(400, "same_workspace", "Choose a different workspace.")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SET LOCAL lock_timeout='2s'; SET LOCAL statement_timeout='10s'"); err != nil {
		return nil, err
	}
	// The relay is an optional overlay. Keep transfers usable before its
	// migrations are installed, but never move an actively routed issue.
	var hasInfoFlowBindings bool
	if err = tx.QueryRow(ctx, `SELECT to_regclass('infoflow_issue_binding') IS NOT NULL`).Scan(&hasInfoFlowBindings); err != nil {
		return nil, err
	}
	var hasInfoFlowWebRequests bool
	if err = tx.QueryRow(ctx, `SELECT to_regclass('infoflow_web_group_request') IS NOT NULL`).Scan(&hasInfoFlowWebRequests); err != nil {
		return nil, err
	}
	var hasInfoFlowPrivateRequests bool
	if err = tx.QueryRow(ctx, `SELECT to_regclass('infoflow_web_private_request') IS NOT NULL`).Scan(&hasInfoFlowPrivateRequests); err != nil {
		return nil, err
	}
	if apply {
		if _, err = tx.Exec(ctx, `SELECT id FROM workspace WHERE id IN ($1,$2) ORDER BY id FOR UPDATE NOWAIT`, source, target); err != nil {
			return nil, err
		}
		// Some legacy relationship writers have no FK/issue-row lock. A bounded
		// table fence prevents a relationship or queued run appearing after the
		// final validation. Preview never takes this write fence. Contention fails
		// closed after two seconds instead of holding a long production lock.
		_, err = tx.Exec(ctx, `LOCK TABLE issue, member, issue_status, agent, comment, comment_reaction,
          agent_task_queue, task_token, activity_log, inbox_item, issue_subscriber, attachment,
          issue_dependency, issue_to_label, issue_source_context, issue_wakeup, issue_child_event,
          task_supplement, task_supplement_capability, issue_reaction, pinned_item,
          issue_pull_request, issue_vcs_pull_request, issue_pr_automation, issue_pull_request_exclusion,
          autopilot_run IN SHARE ROW EXCLUSIVE MODE NOWAIT`)
		if err != nil {
			return nil, err
		}
		if hasInfoFlowBindings {
			// Include relay association writes in the existing transfer fence.
			// Source/delivery receipts remain immutable historical records.
			if _, err = tx.Exec(ctx, `LOCK TABLE infoflow_issue_binding IN SHARE ROW EXCLUSIVE MODE NOWAIT`); err != nil {
				return nil, err
			}
		}
		if hasInfoFlowWebRequests {
			if _, err = tx.Exec(ctx, `LOCK TABLE infoflow_web_group_request IN SHARE ROW EXCLUSIVE MODE NOWAIT`); err != nil {
				return nil, err
			}
		}
		if hasInfoFlowPrivateRequests {
			if _, err = tx.Exec(ctx, `LOCK TABLE infoflow_web_private_request IN SHARE ROW EXCLUSIVE MODE NOWAIT`); err != nil {
				return nil, err
			}
		}
		if _, err = tx.Exec(ctx, `SELECT id FROM issue WHERE id=$1 FOR UPDATE NOWAIT`, id); err != nil {
			return nil, err
		}
	}
	// Membership rows are checked inside the same transaction as the write.
	var sourceRole, targetRole string
	err = tx.QueryRow(ctx, `SELECT s.role,t.role FROM member s JOIN member t ON t.user_id=s.user_id WHERE s.workspace_id=$1 AND t.workspace_id=$2 AND s.user_id=$3`, source, target, actor).Scan(&sourceRole, &targetRole)
	if err == pgx.ErrNoRows || (err == nil && sourceRole != "owner" && sourceRole != "admin") {
		return nil, transferFailure(403, "transfer_forbidden", "Source workspace owner/admin and target workspace membership are required.")
	}
	if err != nil {
		return nil, err
	}
	q := h.Queries.WithTx(tx)
	i, err := q.GetIssue(ctx, id)
	if err != nil {
		return nil, err
	}
	if i.WorkspaceID != source {
		return nil, transferFailure(409, "transfer_changed", "The issue has already moved. Refresh before continuing.")
	}
	p := &issueTransferPlan{IssueID: uuidToString(id), SourceWorkspaceID: uuidToString(source), TargetWorkspaceID: uuidToString(target), Revision: i.Revision, Statuses: []issueTransferStatus{}, Blockers: []string{}}
	var prefix, category string
	err = tx.QueryRow(ctx, `SELECT s.issue_prefix,t.name,t.slug,t.issue_prefix,st.category FROM workspace s CROSS JOIN workspace t JOIN issue_status st ON st.workspace_id=s.id AND st.key=$3 WHERE s.id=$1 AND t.id=$2`, source, target, i.Status).Scan(&prefix, &p.TargetName, &p.TargetSlug, &p.TargetPrefix, &category)
	if err != nil {
		return nil, err
	}
	p.SourceIdentifier = fmt.Sprintf("%s-%d", prefix, i.Number)
	rows, err := tx.Query(ctx, `SELECT key,name FROM issue_status WHERE workspace_id=$1 AND category=$2 AND archived_at IS NULL ORDER BY position,key`, target, category)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var s issueTransferStatus
		if err = rows.Scan(&s.Key, &s.Name); err != nil {
			rows.Close()
			return nil, err
		}
		p.Statuses = append(p.Statuses, s)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	p.TargetStatus = req.TargetStatus
	if p.TargetStatus == "" {
		p.TargetStatus = i.Status
	}
	validStatus := false
	for _, s := range p.Statuses {
		if s.Key == p.TargetStatus {
			validStatus = true
		}
	}
	if !validStatus {
		p.Blockers = append(p.Blockers, "status_mapping_required")
	}
	if i.ProjectID.Valid || i.ParentIssueID.Valid || i.DuplicateOfIssueID.Valid {
		p.Blockers = append(p.Blockers, "project_or_parent")
	}
	var custom bool
	if err = tx.QueryRow(ctx, `SELECT properties<>'{}'::jsonb OR metadata<>'{}'::jsonb OR context_refs<>'[]'::jsonb OR triage_state IS NOT NULL FROM issue WHERE id=$1`, id).Scan(&custom); err != nil {
		return nil, err
	}
	if custom {
		p.Blockers = append(p.Blockers, "custom_context")
	}
	if i.AssigneeID.Valid {
		var allowed bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM member WHERE workspace_id=$1 AND user_id=$2 AND $3='member')`, target, i.AssigneeID, i.AssigneeType.String).Scan(&allowed)
		if err != nil {
			return nil, err
		}
		p.Unassign = !allowed
	}
	if err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM comment WHERE issue_id=$1),(SELECT count(*) FROM agent_task_queue WHERE issue_id=$1),(SELECT count(*) FROM task_message WHERE task_id IN(SELECT id FROM agent_task_queue WHERE issue_id=$1))`, id).Scan(&p.Comments, &p.Runs, &p.Messages); err != nil {
		return nil, err
	}
	var active bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_task_queue WHERE issue_id=$1 AND (status NOT IN('completed','failed','cancelled') OR chat_session_id IS NOT NULL OR autopilot_run_id IS NOT NULL))`, id).Scan(&active); err != nil {
		return nil, err
	}
	if active {
		p.Blockers = append(p.Blockers, "active_or_linked_runs")
	}
	checks := []struct{ code, sql string }{
		{"children_or_dependencies", `SELECT EXISTS(SELECT 1 FROM issue WHERE parent_issue_id=$1 OR duplicate_of_issue_id=$1) OR EXISTS(SELECT 1 FROM issue_dependency WHERE issue_id=$1 OR depends_on_issue_id=$1) OR EXISTS(SELECT 1 FROM issue_child_event WHERE child_id=$1 OR parent_id=$1)`},
		{"attachments", `SELECT EXISTS(SELECT 1 FROM attachment WHERE issue_id=$1 OR comment_id IN(SELECT id FROM comment WHERE issue_id=$1) OR task_id IN(SELECT id FROM agent_task_queue WHERE issue_id=$1))`},
		{"automation_or_sources", `SELECT EXISTS(SELECT 1 FROM issue_source_context WHERE issue_id=$1 OR source_issue_id=$1) OR EXISTS(SELECT 1 FROM issue_wakeup WHERE issue_id=$1) OR EXISTS(SELECT 1 FROM autopilot_run WHERE issue_id=$1) OR EXISTS(SELECT 1 FROM task_supplement WHERE issue_id=$1) OR EXISTS(SELECT 1 FROM task_supplement_capability WHERE issue_id=$1)`},
		{"labels_pins_or_prs", `SELECT EXISTS(SELECT 1 FROM issue_to_label WHERE issue_id=$1) OR EXISTS(SELECT 1 FROM pinned_item WHERE item_id=$1) OR EXISTS(SELECT 1 FROM issue_pull_request WHERE issue_id=$1) OR EXISTS(SELECT 1 FROM issue_vcs_pull_request WHERE issue_id=$1) OR EXISTS(SELECT 1 FROM issue_pr_automation WHERE issue_id=$1) OR EXISTS(SELECT 1 FROM issue_pull_request_exclusion WHERE issue_id=$1)`},
		{"notification_recipients", `SELECT EXISTS(SELECT 1 FROM inbox_item n WHERE n.issue_id=$1 AND (n.recipient_type<>'member' OR NOT EXISTS(SELECT 1 FROM member m WHERE m.workspace_id=$2 AND m.user_id=n.recipient_id)))`},
	}
	if hasInfoFlowBindings {
		// Check the resolved issue ID across all bindings, including an already
		// stale workspace scope. Unbound source/delivery history is not a route.
		checks = append(checks, struct{ code, sql string }{"infoflow_binding", `SELECT EXISTS(SELECT 1 FROM infoflow_issue_binding WHERE issue_id=$1)`})
	}
	if hasInfoFlowWebRequests {
		checks = append(checks, struct{ code, sql string }{"infoflow_pending_binding", `SELECT EXISTS(SELECT 1 FROM infoflow_web_group_request WHERE issue_id=$1 AND state IN ('queued','pending') AND expires_at>clock_timestamp())`})
	}
	if hasInfoFlowPrivateRequests {
		checks = append(checks, struct{ code, sql string }{"infoflow_pending_private_binding", `SELECT EXISTS(SELECT 1 FROM infoflow_web_private_request WHERE issue_id=$1 AND state IN ('queued','pending') AND expires_at>clock_timestamp())`})
	}
	for _, c := range checks {
		var blocked bool
		args := []any{id}
		if c.code == "notification_recipients" {
			args = append(args, target)
		}
		if err = tx.QueryRow(ctx, c.sql, args...).Scan(&blocked); err != nil {
			return nil, err
		}
		if blocked {
			p.Blockers = append(p.Blockers, c.code)
		}
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM issue_subscriber s WHERE issue_id=$1 AND unsubscribed_at IS NULL AND (user_type<>'member' OR NOT EXISTS(SELECT 1 FROM member m WHERE m.workspace_id=$2 AND m.user_id=s.user_id))`, id, target).Scan(&p.RetiredSubscriptions); err != nil {
		return nil, err
	}
	// Hash the issue and dependent mutable history, not just its revision:
	// membership/notification/subscription edits need a new confirmation too.
	var stamp string
	err = tx.QueryRow(ctx, `SELECT md5(jsonb_build_array(
       (SELECT to_jsonb(x) FROM issue x WHERE id=$1),
       (SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM comment x WHERE issue_id=$1),
       (SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM agent_task_queue x WHERE issue_id=$1),
       (SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM inbox_item x WHERE issue_id=$1),
       (SELECT jsonb_agg(to_jsonb(x) ORDER BY user_type,user_id) FROM issue_subscriber x WHERE issue_id=$1),
       (SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM member x WHERE workspace_id=$2)
       )::text)`, id, target).Scan(&stamp)
	if err != nil {
		return nil, err
	}
	sort.Strings(p.Blockers)
	bytes, _ := json.Marshal(p)
	sum := sha256.Sum256(append(bytes, []byte(stamp)...))
	p.Fingerprint = hex.EncodeToString(sum[:])
	if !apply {
		return p, nil
	}
	if len(p.Blockers) > 0 {
		return nil, transferFailure(409, "transfer_blocked", "This issue has unsupported relationships or an incompatible status. Refresh the preview.")
	}
	if req.Fingerprint == "" || req.Fingerprint != p.Fingerprint {
		return nil, transferFailure(409, "transfer_changed", "The issue or its destination changed. Refresh the preview and confirm again.")
	}
	if p.Unassign && !req.ConfirmUnassign {
		return nil, transferFailure(409, "transfer_unassign_required", "Confirm removing the current assignee before transferring.")
	}
	var number int32
	err = tx.QueryRow(ctx, `UPDATE workspace SET issue_counter=greatest(issue_counter,(SELECT coalesce(max(number),0) FROM issue WHERE workspace_id=$1))+1 WHERE id=$1 RETURNING issue_counter`, target).Scan(&number)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `UPDATE issue SET workspace_id=$2,number=$3,status=$4,assignee_id=CASE WHEN $5 THEN NULL ELSE assignee_id END,assignee_type=CASE WHEN $5 THEN NULL ELSE assignee_type END,revision=revision+1,updated_at=now(),last_activity_at=now() WHERE id=$1`, id, target, number, p.TargetStatus, p.Unassign)
	if err != nil {
		return nil, err
	}
	for _, table := range []string{"comment", "activity_log", "inbox_item", "issue_reaction"} {
		if _, err = tx.Exec(ctx, "UPDATE "+table+" SET workspace_id=$2 WHERE issue_id=$1", id, target); err != nil {
			return nil, err
		}
	}
	// Finished-run credentials are operational leases, not history. Never let
	// an old host retain a credential to data that has changed workspace.
	if _, err = tx.Exec(ctx, `DELETE FROM task_token WHERE task_id IN(SELECT id FROM agent_task_queue WHERE issue_id=$1)`, id); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE comment_reaction SET workspace_id=$2 WHERE comment_id IN(SELECT id FROM comment WHERE issue_id=$1)`, id, target); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE issue_subscriber s SET unsubscribed_at=coalesce(unsubscribed_at,now()) WHERE issue_id=$1 AND (user_type<>'member' OR NOT EXISTS(SELECT 1 FROM member m WHERE m.workspace_id=$2 AND m.user_id=s.user_id))`, id, target); err != nil {
		return nil, err
	}
	p.Identifier = fmt.Sprintf("%s-%d", p.TargetPrefix, number)
	p.Path = "/" + p.TargetSlug + "/issues/" + p.Identifier
	details, _ := json.Marshal(map[string]any{"source_workspace_id": p.SourceWorkspaceID, "target_workspace_id": p.TargetWorkspaceID, "old_identifier": p.SourceIdentifier, "new_identifier": p.Identifier, "assignee_removed": p.Unassign})
	if _, err = tx.Exec(ctx, `INSERT INTO activity_log(workspace_id,issue_id,actor_type,actor_id,action,details) VALUES($1,$2,'member',$3,'workspace_changed',$4)`, target, id, actor, details); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return p, nil
}
