package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"sort"
	"time"
)

// Preview is a read-only repeatable snapshot. Apply fences structural writers,
// rechecks every moved issue, revokes old leases and commits the whole tree.
func (h *Handler) runIssueTransfer(ctx context.Context, id, source, target, actor pgtype.UUID, req issueTransferRequest, apply bool) (*issueTransferPlan, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if source == target {
		return nil, transferFailure(400, "same_workspace", "Choose a different workspace.")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if !apply {
		if _, err = tx.Exec(ctx, `SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY`); err != nil {
			return nil, err
		}
	}
	if _, err = tx.Exec(ctx, `SET LOCAL lock_timeout='2s'; SET LOCAL statement_timeout='15s'`); err != nil {
		return nil, err
	}
	if apply {
		if _, err = tx.Exec(ctx, `SELECT id FROM workspace WHERE id IN($1,$2) ORDER BY id FOR UPDATE`, source, target); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `LOCK TABLE issue,member,issue_status,issue_manager,issue_manager_state,agent,comment,comment_reaction,agent_task_queue,task_token,activity_log,inbox_item,issue_subscriber,attachment,issue_dependency,issue_to_label,issue_source_context,issue_source_context_object_intent,issue_wakeup,issue_wakeup_receipt,issue_child_event,task_supplement,task_supplement_capability,issue_reaction,pinned_item,issue_pull_request,issue_vcs_pull_request,issue_pr_automation,issue_pull_request_exclusion,autopilot_run,resource_web_panel IN SHARE ROW EXCLUSIVE MODE`); err != nil {
			return nil, err
		}
	}
	q := h.Queries.WithTx(tx)
	root, err := q.GetIssue(ctx, id)
	if err != nil {
		return nil, err
	}
	if root.WorkspaceID != source {
		return nil, transferFailure(409, "transfer_changed", "The issue has already moved. Refresh before continuing.")
	}
	var role string
	if err = tx.QueryRow(ctx, `SELECT s.role FROM member s JOIN member t ON t.user_id=s.user_id WHERE s.workspace_id=$1 AND t.workspace_id=$2 AND s.user_id=$3`, source, target, actor).Scan(&role); err != nil {
		if err == pgx.ErrNoRows {
			return nil, transferFailure(403, "transfer_forbidden", "Source access and target workspace membership are required.")
		}
		return nil, err
	}
	rows, err := tx.Query(ctx, `WITH RECURSIVE tree(id) AS(SELECT id FROM issue WHERE id=$1 AND workspace_id=$2 UNION SELECT child.id FROM issue child JOIN tree p ON child.parent_issue_id=p.id WHERE child.workspace_id=$2 AND $3) SELECT id FROM tree ORDER BY id`, id, source, req.IncludeSubissues)
	if err != nil {
		return nil, err
	}
	ids := []pgtype.UUID{}
	for rows.Next() {
		var current pgtype.UUID
		if err = rows.Scan(&current); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, current)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	issues := make([]db.Issue, 0, len(ids))
	p := &issueTransferPlan{IssueID: uuidToString(id), SourceWorkspaceID: uuidToString(source), TargetWorkspaceID: uuidToString(target), Revision: root.Revision, Statuses: []issueTransferStatus{}, Blockers: []string{}, IssueIDs: []string{}, AffectedIssues: len(ids), IncludeSubissues: req.IncludeSubissues, RequiresCleanup: true, StatusMappings: []issueTransferStatusMapping{}, Warnings: []string{"workspace_configuration_cleared", "active_runs_cancelled", "external_relations_detached", "conversation_bindings_removed", "private_chats_keep_original_permissions"}}
	for _, current := range ids {
		issue, e := q.GetIssue(ctx, current)
		if e != nil {
			return nil, e
		}
		allowed, e := q.CanManageIssueByCreator(ctx, current, source, actor)
		if e != nil {
			return nil, e
		}
		if !allowed {
			return nil, transferFailure(403, "transfer_forbidden", "Every issue must be created by you or your agent, or you must be a source workspace owner/admin.")
		}
		issues = append(issues, issue)
		p.IssueIDs = append(p.IssueIDs, uuidToString(current))
	}
	var prefix string
	if err = tx.QueryRow(ctx, `SELECT s.issue_prefix,t.name,t.slug,t.issue_prefix FROM workspace s CROSS JOIN workspace t WHERE s.id=$1 AND t.id=$2`, source, target).Scan(&prefix, &p.TargetName, &p.TargetSlug, &p.TargetPrefix); err != nil {
		return nil, err
	}
	p.SourceIdentifier = fmt.Sprintf("%s-%d", prefix, root.Number)
	rows, err = tx.Query(ctx, `SELECT key,name,category FROM issue_status WHERE workspace_id=$1 AND archived_at IS NULL ORDER BY position,key`, target)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var status issueTransferStatus
		if err = rows.Scan(&status.Key, &status.Name, &status.Category); err != nil {
			rows.Close()
			return nil, err
		}
		p.Statuses = append(p.Statuses, status)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	catalog := map[string]issueTransferStatus{}
	for _, status := range p.Statuses {
		catalog[status.Key] = status
	}
	mappings := map[string]string{}
	statusKeys := map[string]bool{}
	for _, issue := range issues {
		statusKeys[issue.Status] = true
	}
	keys := []string{}
	for key := range statusKeys {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		var category, name string
		if err = tx.QueryRow(ctx, `SELECT category,name FROM issue_status WHERE workspace_id=$1 AND key=$2`, source, key).Scan(&category, &name); err == pgx.ErrNoRows {
			category = ""
			name = key
		} else if err != nil {
			return nil, err
		}
		selected := req.StatusMappings[key]
		if key == root.Status && req.TargetStatus != "" {
			selected = req.TargetStatus
		}
		if selected == "" {
			if match, ok := catalog[key]; ok && match.Category == category {
				selected = key
			} else {
				for _, status := range p.Statuses {
					if status.Category == category {
						selected = status.Key
						break
					}
				}
			}
		}
		if _, ok := catalog[selected]; !ok {
			selected = ""
			p.Blockers = append(p.Blockers, "status_mapping_required")
		}
		mappings[key] = selected
		p.StatusMappings = append(p.StatusMappings, issueTransferStatusMapping{Source: key, Name: name, Target: selected})
	}
	p.TargetStatus = mappings[root.Status]
	if err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM comment WHERE issue_id=ANY($1::uuid[])),(SELECT count(*) FROM agent_task_queue WHERE issue_id=ANY($1::uuid[])),(SELECT count(*) FROM task_message WHERE task_id IN(SELECT id FROM agent_task_queue WHERE issue_id=ANY($1::uuid[]))),(SELECT count(*) FROM issue_subscriber s WHERE issue_id=ANY($1::uuid[]) AND unsubscribed_at IS NULL AND (user_type<>'member' OR NOT EXISTS(SELECT 1 FROM member m WHERE m.workspace_id=$2 AND m.user_id=s.user_id)))`, ids, target).Scan(&p.Comments, &p.Runs, &p.Messages, &p.RetiredSubscriptions); err != nil {
		return nil, err
	}
	var corrupt bool
	if err = tx.QueryRow(ctx, `SELECT
 EXISTS(SELECT 1 FROM task_supplement_capability cap LEFT JOIN agent_task_queue t ON t.id=cap.task_id WHERE (cap.issue_id=ANY($1::uuid[]) OR t.issue_id=ANY($1::uuid[])) AND (t.id IS NULL OR cap.issue_id IS DISTINCT FROM t.issue_id OR cap.workspace_id<>$2))
 OR EXISTS(SELECT 1 FROM task_supplement s LEFT JOIN agent_task_queue t ON t.id=s.task_id LEFT JOIN comment c ON c.id=s.comment_id WHERE (s.issue_id=ANY($1::uuid[]) OR t.issue_id=ANY($1::uuid[]) OR c.issue_id=ANY($1::uuid[])) AND (t.id IS NULL OR c.id IS NULL OR s.issue_id IS DISTINCT FROM t.issue_id OR s.issue_id IS DISTINCT FROM c.issue_id OR s.workspace_id<>$2 OR c.workspace_id<>$2))
 OR EXISTS(SELECT 1 FROM comment WHERE issue_id=ANY($1::uuid[]) AND workspace_id<>$2)
 OR EXISTS(SELECT 1 FROM attachment WHERE (issue_id=ANY($1::uuid[]) OR comment_id IN(SELECT id FROM comment WHERE issue_id=ANY($1::uuid[]))) AND workspace_id<>$2)
 OR EXISTS(SELECT 1 FROM issue_source_context WHERE issue_id=ANY($1::uuid[]) AND workspace_id<>$2)
 `, ids, source).Scan(&corrupt); err != nil {
		return nil, err
	}
	if corrupt {
		p.Blockers = append(p.Blockers, "automation_or_sources")
	}
	for _, issue := range issues {
		if issue.AssigneeID.Valid {
			var keep bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM member WHERE workspace_id=$1 AND user_id=$2 AND $3='member')`, target, issue.AssigneeID, issue.AssigneeType.String).Scan(&keep); err != nil {
				return nil, err
			}
			if !keep {
				p.Unassign = true
			}
		}
	}
	// Stream chunks and comment bodies are retained verbatim and do not require
	// a moving agent to become quiet merely to obtain a stable confirmation.
	var stamp string
	err = tx.QueryRow(ctx, `SELECT md5(jsonb_build_array(
 (SELECT jsonb_agg(jsonb_build_array(x.id,x.workspace_id,x.creator_type,x.creator_id,x.status,x.assignee_type,x.assignee_id,x.parent_issue_id,x.duplicate_of_issue_id,x.project_id,x.properties,x.metadata,x.context_refs,x.triage_state) ORDER BY x.id) FROM issue x WHERE x.id=ANY($1::uuid[]) OR x.parent_issue_id=ANY($1::uuid[]) OR x.duplicate_of_issue_id=ANY($1::uuid[])),
 (SELECT jsonb_agg(jsonb_build_array(a.id,a.owner_id) ORDER BY a.id) FROM agent a WHERE a.id IN (SELECT creator_id FROM issue WHERE id=ANY($1::uuid[]) AND creator_type='agent')),
 (SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM member x WHERE workspace_id IN($2,$3)),
 (SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM issue_status x WHERE workspace_id=$3),
 (SELECT jsonb_agg(jsonb_build_array(x.id,x.status,x.agent_id) ORDER BY id) FROM agent_task_queue x WHERE issue_id=ANY($1::uuid[]) AND status NOT IN('completed','failed','cancelled')),
 (SELECT jsonb_agg(to_jsonb(x) ORDER BY issue_id,user_type,user_id) FROM issue_subscriber x WHERE issue_id=ANY($1::uuid[])),
 (SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM issue_wakeup x WHERE issue_id=ANY($1::uuid[])),
 (SELECT jsonb_agg(to_jsonb(x) ORDER BY issue_id,depends_on_issue_id) FROM issue_dependency x WHERE issue_id=ANY($1::uuid[]) OR depends_on_issue_id=ANY($1::uuid[])),
 (SELECT jsonb_agg(to_jsonb(x) ORDER BY issue_id,label_id) FROM issue_to_label x WHERE issue_id=ANY($1::uuid[])),
 (SELECT jsonb_agg(to_jsonb(x) ORDER BY task_id) FROM task_supplement_capability x WHERE issue_id=ANY($1::uuid[]) OR task_id IN(SELECT id FROM agent_task_queue WHERE issue_id=ANY($1::uuid[]))),
 (SELECT jsonb_agg(to_jsonb(x) ORDER BY task_id,comment_id) FROM task_supplement x WHERE issue_id=ANY($1::uuid[])),
 (SELECT jsonb_agg(jsonb_build_array(x.id,x.name,x.slug,x.issue_prefix) ORDER BY x.id) FROM workspace x WHERE id IN($2,$3)),
 (SELECT jsonb_agg(to_jsonb(x) ORDER BY issue_id,pull_request_id) FROM issue_pull_request x WHERE issue_id=ANY($1::uuid[])),
 (SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM resource_web_panel x WHERE issue_id=ANY($1::uuid[]))
 )::text)`, ids, source, target).Scan(&stamp)
	if err != nil {
		return nil, err
	}
	relayStamps := []string{}
	for _, table := range []string{"infoflow_issue_binding", "infoflow_web_group_request", "infoflow_web_private_request"} {
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		var value string
		if err = tx.QueryRow(ctx, "SELECT md5(coalesce(jsonb_agg((to_jsonb(x) - ARRAY['lease_token','lease_robot_id','lease_agent_id','lease_until','next_at','updated_at']) ORDER BY id)::text,'[]')) FROM "+table+" x WHERE issue_id=ANY($1::uuid[])", ids).Scan(&value); err != nil {
			return nil, err
		}
		relayStamps = append(relayStamps, value)
	}
	encoded, _ := json.Marshal(struct {
		Issue    string
		Target   string
		Tree     bool
		Mappings map[string]string
		Stamp    string
		Relay    []string
	}{p.IssueID, p.TargetWorkspaceID, req.IncludeSubissues, mappings, stamp, relayStamps})
	digest := sha256.Sum256(encoded)
	p.Fingerprint = hex.EncodeToString(digest[:])
	if !apply {
		return p, nil
	}
	if len(p.Blockers) > 0 {
		return nil, transferFailure(409, "transfer_blocked", "Choose a valid target status for every source status.")
	}
	if req.Fingerprint == "" || req.Fingerprint != p.Fingerprint {
		return nil, transferFailure(409, "transfer_changed", "The issue configuration or permissions changed. Refresh the preview and confirm again.")
	}
	if !req.ConfirmCleanup {
		return nil, transferFailure(409, "transfer_cleanup_required", "Confirm cancelling active runs and removing incompatible workspace links.")
	}
	if p.Unassign && !req.ConfirmUnassign {
		return nil, transferFailure(409, "transfer_unassign_required", "Confirm removing unavailable assignees.")
	}
	cancelled := []db.AgentTaskQueue{}
	for _, current := range ids {
		stopped, e := q.CancelAgentTasksByIssue(ctx, current)
		if e != nil {
			return nil, e
		}
		cancelled = append(cancelled, stopped...)
		if _, e = q.FailAutopilotRunsByIssue(ctx, current); e != nil {
			return nil, e
		}
	}
	if err = service.SettleTerminalTaskState(ctx, q, cancelled...); err != nil {
		return nil, err
	}
	if err = clearTransferConfiguration(ctx, tx, ids, source, target); err != nil {
		return nil, err
	}
	for _, issue := range issues {
		var number int32
		if err = tx.QueryRow(ctx, `UPDATE workspace SET issue_counter=greatest(issue_counter,(SELECT coalesce(max(number),0) FROM issue WHERE workspace_id=$1))+1 WHERE id=$1 RETURNING issue_counter`, target).Scan(&number); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `UPDATE issue SET workspace_id=$2,number=$3,status=$4,assignee_id=CASE WHEN assignee_type='member' AND EXISTS(SELECT 1 FROM member WHERE workspace_id=$2 AND user_id=assignee_id) THEN assignee_id ELSE NULL END,assignee_type=CASE WHEN assignee_type='member' AND EXISTS(SELECT 1 FROM member WHERE workspace_id=$2 AND user_id=assignee_id) THEN assignee_type ELSE NULL END,project_id=NULL,properties='{}',metadata='{}',context_refs='[]',triage_state=NULL,parent_issue_id=CASE WHEN parent_issue_id=ANY($5::uuid[]) THEN parent_issue_id ELSE NULL END,duplicate_of_issue_id=CASE WHEN duplicate_of_issue_id=ANY($5::uuid[]) AND $4='cancelled' THEN duplicate_of_issue_id ELSE NULL END,revision=revision+1,updated_at=now(),last_activity_at=now() WHERE id=$1`, issue.ID, target, number, mappings[issue.Status], ids); err != nil {
			return nil, err
		}
		identifier := fmt.Sprintf("%s-%d", p.TargetPrefix, number)
		if issue.ID == id {
			p.Identifier = identifier
			p.Path = "/" + p.TargetSlug + "/issues/" + identifier
		}
		details, _ := json.Marshal(map[string]any{"source_workspace_id": p.SourceWorkspaceID, "target_workspace_id": p.TargetWorkspaceID, "old_identifier": fmt.Sprintf("%s-%d", prefix, issue.Number), "new_identifier": identifier, "configuration_cleared": true, "include_subissues": req.IncludeSubissues})
		if _, err = tx.Exec(ctx, `INSERT INTO activity_log(workspace_id,issue_id,actor_type,actor_id,action,details) VALUES($1,$2,'member',$3,'workspace_changed',$4)`, target, issue.ID, actor, details); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	if h.TaskService != nil {
		h.TaskService.FinalizeTransferredTaskCancellations(ctx, cancelled)
	}
	return p, nil
}

func clearTransferConfiguration(ctx context.Context, tx pgx.Tx, ids []pgtype.UUID, source, target pgtype.UUID) error {
	// Revoke writeback before the issue starts belonging to another workspace.
	statements := []string{
		`DELETE FROM agent_token_read_scope WHERE task_id IN(SELECT id FROM agent_task_queue WHERE issue_id=ANY($1::uuid[]))`,
		`DELETE FROM task_token WHERE task_id IN(SELECT id FROM agent_task_queue WHERE issue_id=ANY($1::uuid[]))`,
		`DELETE FROM issue_wakeup_receipt WHERE wakeup_id IN(SELECT id FROM issue_wakeup WHERE issue_id=ANY($1::uuid[]))`,
		`DELETE FROM issue_wakeup WHERE issue_id=ANY($1::uuid[])`,
		`DELETE FROM issue_child_event WHERE parent_id=ANY($1::uuid[]) OR child_id=ANY($1::uuid[])`,
		`UPDATE autopilot_run SET issue_id=NULL WHERE issue_id=ANY($1::uuid[])`,
		`DELETE FROM issue_dependency WHERE (issue_id=ANY($1::uuid[]) OR depends_on_issue_id=ANY($1::uuid[])) AND NOT(issue_id=ANY($1::uuid[]) AND depends_on_issue_id=ANY($1::uuid[]))`,
		`UPDATE issue SET parent_issue_id=NULL,revision=revision+1,updated_at=now() WHERE parent_issue_id=ANY($1::uuid[]) AND NOT(id=ANY($1::uuid[]))`,
		`UPDATE issue SET duplicate_of_issue_id=NULL,revision=revision+1,updated_at=now() WHERE duplicate_of_issue_id=ANY($1::uuid[]) AND NOT(id=ANY($1::uuid[]))`,
		`DELETE FROM issue_to_label WHERE issue_id=ANY($1::uuid[])`,
		`DELETE FROM pinned_item WHERE item_id=ANY($1::uuid[]) AND item_type='issue'`,
		`DELETE FROM issue_pull_request WHERE issue_id=ANY($1::uuid[])`,
		`DELETE FROM issue_vcs_pull_request WHERE issue_id=ANY($1::uuid[])`,
		`DELETE FROM issue_pr_automation WHERE issue_id=ANY($1::uuid[])`,
		`DELETE FROM issue_pull_request_exclusion WHERE issue_id=ANY($1::uuid[])`,
	}
	for _, sql := range statements {
		if _, err := tx.Exec(ctx, sql, ids); err != nil {
			return err
		}
	}
	for _, table := range []string{"comment", "activity_log", "issue_reaction", "issue_source_context", "resource_web_panel"} {
		if _, err := tx.Exec(ctx, "UPDATE "+table+" SET workspace_id=$2 WHERE issue_id=ANY($1::uuid[])", ids, target); err != nil {
			return err
		}
	}
	for _, sql := range []string{
		`UPDATE comment_reaction SET workspace_id=$2 WHERE comment_id IN(SELECT id FROM comment WHERE issue_id=ANY($1::uuid[]))`,
		`UPDATE attachment SET workspace_id=$2 WHERE issue_id=ANY($1::uuid[]) OR comment_id IN(SELECT id FROM comment WHERE issue_id=ANY($1::uuid[])) OR source_context_id IN(SELECT id FROM issue_source_context WHERE issue_id=ANY($1::uuid[])) OR (task_id IN(SELECT id FROM agent_task_queue WHERE issue_id=ANY($1::uuid[])) AND chat_session_id IS NULL)`,
		`UPDATE issue_source_context_object_intent SET workspace_id=$2 WHERE source_context_id IN(SELECT id FROM issue_source_context WHERE issue_id=ANY($1::uuid[]))`,
		`UPDATE task_supplement_capability SET workspace_id=$2 WHERE issue_id=ANY($1::uuid[]) AND task_id IN(SELECT id FROM agent_task_queue WHERE issue_id=ANY($1::uuid[]))`,
		`UPDATE task_supplement SET workspace_id=$2,status=CASE WHEN status IN('pending','delivering') THEN 'failed' ELSE status END,failure_reason=CASE WHEN status IN('pending','delivering') THEN 'workspace_changed' ELSE failure_reason END WHERE issue_id=ANY($1::uuid[])`,
		`DELETE FROM inbox_item n WHERE issue_id=ANY($1::uuid[]) AND (recipient_type<>'member' OR NOT EXISTS(SELECT 1 FROM member m WHERE m.workspace_id=$2 AND m.user_id=n.recipient_id))`,
		`UPDATE inbox_item SET workspace_id=$2 WHERE issue_id=ANY($1::uuid[])`,
		`UPDATE issue_subscriber s SET unsubscribed_at=coalesce(unsubscribed_at,now()) WHERE issue_id=ANY($1::uuid[]) AND (user_type<>'member' OR NOT EXISTS(SELECT 1 FROM member m WHERE m.workspace_id=$2 AND m.user_id=s.user_id))`,
	} {
		if _, err := tx.Exec(ctx, sql, ids, target); err != nil {
			return err
		}
	}
	for _, table := range []string{"infoflow_issue_binding", "infoflow_web_group_request", "infoflow_web_private_request"} {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			continue
		}
		if _, err := tx.Exec(ctx, "LOCK TABLE "+table+" IN SHARE ROW EXCLUSIVE MODE"); err != nil {
			return err
		}
		if table == "infoflow_issue_binding" {
			if _, err := tx.Exec(ctx, `UPDATE infoflow_issue_delivery SET cancelled_at=clock_timestamp() WHERE binding_id IN(SELECT id FROM infoflow_issue_binding WHERE issue_id=ANY($1::uuid[])) AND acked_at IS NULL AND cancelled_at IS NULL`, ids); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE infoflow_conversation_notice SET cancelled_at=clock_timestamp() WHERE binding_id IN(SELECT id FROM infoflow_issue_binding WHERE issue_id=ANY($1::uuid[])) AND acked_at IS NULL AND cancelled_at IS NULL`, ids); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE infoflow_issue_binding SET issue_id=NULL,binder_id=NULL,sync_enabled=false,generation=gen_random_uuid(),bound_at=clock_timestamp(),group_state='pending',leader_robot_id=NULL,leader_verified_at=NULL WHERE issue_id=ANY($1::uuid[])`, ids); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec(ctx, "UPDATE "+table+" SET state='failed',reason='workspace_changed',generation=gen_random_uuid(),lease_token=NULL,lease_robot_id=NULL,lease_agent_id=NULL,lease_until=NULL,updated_at=clock_timestamp() WHERE issue_id=ANY($1::uuid[])", ids); err != nil {
				return err
			}
		}
	}
	return nil
}
