package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

type infoFlowDeliveryRequest struct {
	infoFlowScope
	Limit      int      `json:"limit,omitempty"`
	MessageID  string   `json:"message_id,omitempty"`
	MessageIDs []string `json:"message_ids,omitempty"`
}

// One Markdown body plus up to 100 native AT batches for 5,000 group members.
const infoFlowMaxDeliveryMessageIDs = 101

type infoFlowDelivery struct {
	AuthorAgentID    string               `json:"author_agent_id"`
	AuthorName       string               `json:"author_name"`
	AuthorType       string               `json:"author_type,omitempty"`
	ID               string               `json:"id"`
	Conversation     infoFlowConversation `json:"conversation"`
	Content          string               `json:"content"`
	IssueID          string               `json:"issue_id"`
	CommentID        string               `json:"comment_id"`
	ReplyToMessageID string               `json:"reply_to_message_id"`
	Binding          *infoFlowBindingView `json:"binding"`
	NotifyUsers      []string             `json:"notify_users"`
	RequiresCheck    bool                 `json:"requires_check"`
	CommentCreatedAt string               `json:"comment_created_at,omitempty"`
	ImageAttachments []infoFlowReplyImage `json:"image_attachments,omitempty"`
	ImageCount       int                  `json:"image_count,omitempty"`
	ImageNotice      string               `json:"image_notice,omitempty"`
}

// Bindings subscribe to new task content, independent of the original input
// channel. Ancestry is retained for provenance, never as a broadcast gate.
const infoFlowDeliveryOrigin = `
CROSS JOIN LATERAL (
 WITH RECURSIVE ancestry(kind,id) AS (
   SELECT 'comment'::text,c.id
   UNION
   SELECT next.kind,next.id FROM ancestry a
   LEFT JOIN comment ac ON a.kind='comment' AND ac.id=a.id AND ac.issue_id=c.issue_id AND ac.workspace_id=c.workspace_id
   LEFT JOIN agent_task_queue t ON a.kind='task' AND t.id=a.id AND t.issue_id=c.issue_id
   CROSS JOIN LATERAL (
     SELECT 'comment'::text AS kind,ac.parent_id AS id
     UNION ALL SELECT 'task',ac.source_task_id
     UNION ALL SELECT 'comment',unnest(ARRAY[t.trigger_comment_id] || COALESCE(t.coalesced_comment_ids,'{}'))
     UNION ALL SELECT 'task',unnest(ARRAY[t.parent_task_id,t.delegated_from_task_id,t.retry_of_task_id])
   ) next WHERE next.id IS NOT NULL
 ), foreign_nodes AS (
   SELECT a.id FROM ancestry a
   LEFT JOIN comment ac ON a.kind='comment' AND ac.id=a.id
   LEFT JOIN agent_task_queue t ON a.kind='task' AND t.id=a.id
   LEFT JOIN agent ta ON ta.id=t.agent_id
   WHERE (a.kind='comment' AND (ac.id IS NULL OR ac.issue_id<>c.issue_id OR ac.workspace_id<>c.workspace_id))
      OR (a.kind='task' AND (t.id IS NULL OR ta.id IS NULL OR t.issue_id IS DISTINCT FROM c.issue_id OR ta.workspace_id<>c.workspace_id))
 ), origins AS (
   SELECT DISTINCT s.id,s.created_at FROM ancestry a JOIN infoflow_issue_source s
   ON ((a.kind='comment' AND s.comment_id=a.id) OR (a.kind='task' AND s.task_id=a.id))
   WHERE s.issue_id=c.issue_id
 )
 SELECT (array_agg(id ORDER BY created_at DESC,id DESC))[1] AS source_id
 FROM origins HAVING NOT EXISTS(SELECT 1 FROM foreign_nodes)
) origin
`

const infoFlowDeliveryCandidates = `
SELECT c.id,c.content,origin.source_id,c.author_type FROM comment c
` + infoFlowDeliveryOrigin + `
LEFT JOIN "user" u ON c.author_type='member' AND u.id=c.author_id
WHERE c.issue_id=$3 AND c.workspace_id=$4 AND (
 (c.author_type='agent' AND c.author_id=ANY($5::uuid[])) OR
 (c.author_type='member' AND c.type='comment'
  AND (split_part(lower(u.email),'@',2)=$8 OR ($8='baidu.com' AND split_part(lower(u.email),'@',2) ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.baidu\.com$'))
  AND split_part(lower(u.email),'@',1) ~ '^[a-z0-9][a-z0-9._-]{0,127}$'))
AND c.deleted_at IS NULL AND c.created_at >= $6 AND c.type IN ('comment','progress_update')
AND NOT EXISTS (
 SELECT 1 FROM infoflow_issue_source direct
 JOIN infoflow_issue_binding source ON source.id=direct.binding_id
 JOIN infoflow_issue_binding target ON target.id=$1
 WHERE direct.comment_id=c.id AND source.conversation_kind=target.conversation_kind
 AND source.conversation_id=target.conversation_id
 AND (target.conversation_kind='group' OR source.robot_id=target.robot_id)
)
AND NOT EXISTS(SELECT 1 FROM infoflow_issue_delivery d WHERE d.binding_id=$1 AND d.generation=$2 AND d.comment_id=c.id)
ORDER BY c.created_at,c.id LIMIT $7`

func (h *Handler) InfoFlowIssueDeliveries(w http.ResponseWriter, r *http.Request) {
	if !infoFlowAuthenticate(w, r) {
		return
	}
	var req infoFlowDeliveryRequest
	if !infoFlowDecode(w, r, &req) {
		return
	}
	if !req.infoFlowScope.valid() || req.Limit < 0 || req.Limit > 50 {
		infoFlowError(w, 400, "invalid scope")
		return
	}
	if req.Limit == 0 {
		req.Limit = 20
	}
	// Discover keys, then close rows before taking transaction-scoped locks.
	rows, err := h.DB.Query(r.Context(), `SELECT conversation_kind,conversation_id,id::text FROM infoflow_issue_binding
		WHERE workspace_id=$2 AND issue_id IS NOT NULL AND ((conversation_kind='private' AND robot_id=$1 AND agent_id=$3) OR (conversation_kind='group' AND leader_robot_id=$1)) ORDER BY last_polled_at NULLS FIRST,conversation_kind,conversation_id`, req.RobotID, req.WorkspaceID, req.AgentID)
	if err != nil {
		infoFlowError(w, 500, "bindings")
		return
	}
	type destination struct {
		conversation infoFlowConversation
		bindingID    string
	}
	var conversations []destination
	for rows.Next() {
		var c destination
		if err = rows.Scan(&c.conversation.Kind, &c.conversation.ID, &c.bindingID); err != nil {
			break
		}
		conversations = append(conversations, c)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		infoFlowError(w, 500, "bindings")
		return
	}
	out := make([]infoFlowDelivery, 0)
	for _, conversation := range conversations {
		if len(out) >= req.Limit {
			break
		}
		// One per conversation per poll, rotating persistently even when a
		// destination never acknowledges. A failed group cannot starve others.
		deliveries, err := h.infoFlowPollBinding(r.Context(), req.infoFlowScope, conversation.conversation, conversation.bindingID, 1)
		if err != nil {
			infoFlowError(w, 500, "deliveries")
			return
		}
		out = append(out, deliveries...)
	}
	writeJSON(w, 200, map[string]any{"deliveries": out})
}

func (h *Handler) infoFlowPollBinding(ctx context.Context, s infoFlowScope, c infoFlowConversation, bindingID string, limit int) ([]infoFlowDelivery, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = infoFlowLock(ctx, tx, s, c); err != nil {
		return nil, err
	}
	b, err := infoFlowLoadBindingID(ctx, tx, s, c, bindingID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sh, _ := h.infoFlowScoped(tx)
	view := sh.infoFlowBindingView(ctx, b, s.WorkspaceID, true)
	if view == nil {
		return nil, nil
	}
	if !b.SyncEnabled {
		return nil, nil
	}
	authors := []string{s.AgentID}
	if c.Kind == "group" {
		if view.Group == nil || view.Group.State != "active" || view.Group.LeaderRobotID != s.RobotID || view.Group.LeaderAgentID != strings.ToLower(s.AgentID) {
			return nil, nil
		}
		authors = view.Group.AgentIDs
	}
	agent, err := sh.Queries.GetAgent(ctx, parseUUID(s.AgentID))
	if err != nil || uuidToString(agent.WorkspaceID) != strings.ToLower(s.WorkspaceID) || agent.ArchivedAt.Valid {
		return nil, nil
	}
	if c.Kind == "private" {
		if view.Private == nil || view.Private.State != "active" {
			return nil, nil
		}
		if _, _, err = sh.infoFlowIdentity(ctx, c.ID, s.WorkspaceID); errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
	}
	var cutoff pgtype.Timestamptz
	if err = tx.QueryRow(ctx, "SELECT GREATEST(bound_at,broadcast_started_at) FROM infoflow_issue_binding WHERE id=$1", b.ID).Scan(&cutoff); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, infoFlowDeliveryCandidates, b.ID, b.Generation, b.IssueID, s.WorkspaceID, authors, cutoff, limit, strings.ToLower(os.Getenv("INFOFLOW_ISSUE_IDENTITY_DOMAIN")))
	if err != nil {
		return nil, err
	}
	type candidate struct {
		comment, source pgtype.UUID
		content         string
		authorType      string
	}
	var candidates []candidate
	for rows.Next() {
		var v candidate
		if err = rows.Scan(&v.comment, &v.content, &v.source, &v.authorType); err != nil {
			break
		}
		candidates = append(candidates, v)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, v := range candidates {
		var imageCount int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM attachment WHERE comment_id=$1 AND issue_id=$2 AND workspace_id=$3 AND content_type LIKE 'image/%'`, v.comment, b.IssueID, s.WorkspaceID).Scan(&imageCount); err != nil {
			return nil, err
		}
		if imageCount > 0 {
			v.content += fmt.Sprintf("\n\n图片：%d 张，请打开任务链接查看。", imageCount)
		}
		notifyUsers := []string{}
		if c.Kind == "group" && v.authorType == "agent" {
			notifyUsers, err = infoFlowReplyAudience(ctx, tx, v.comment, parseUUID(s.WorkspaceID), b.IssueID)
			if err != nil {
				return nil, err
			}
			notifyUsers = infoFlowNormalizeNotifyUsers("", notifyUsers, s.RobotID)
		}
		_, err = tx.Exec(ctx, `INSERT INTO infoflow_issue_delivery(id,binding_id,generation,source_id,comment_id,content,notify_users) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(binding_id,generation,comment_id) DO NOTHING`, dbid.NewV7(), b.ID, b.Generation, v.source, v.comment, v.content, notifyUsers)
		if err != nil {
			return nil, err
		}
	}
	// Audience and content snapshots are immutable across independent retries.
	// The binding generation and deployment cutoff still fence stale work.
	rows, err = tx.Query(ctx, `SELECT d.id::text,d.comment_id::text,d.content,COALESCE(s.message_id,''),CASE WHEN c.author_type='member' THEN $8 ELSE c.author_id::text END,CASE WHEN c.author_type='member' THEN CASE WHEN split_part(lower(u.email),'@',2) ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.baidu\.com$' THEN split_part(lower(u.email),'@',1)||'_'||split_part(split_part(lower(u.email),'@',2),'.',1) ELSE split_part(lower(u.email),'@',1) END ELSE COALESCE(a.name,'智能体') END,CASE WHEN c.author_type='member' THEN 'user' ELSE c.author_type END,COALESCE(d.notify_users,'{}'::text[]),d.notify_users IS NULL FROM infoflow_issue_delivery d LEFT JOIN infoflow_issue_source s ON s.id=d.source_id JOIN comment c ON c.id=d.comment_id LEFT JOIN agent a ON c.author_type='agent' AND a.id=c.author_id AND a.workspace_id=c.workspace_id LEFT JOIN "user" u ON c.author_type='member' AND u.id=c.author_id
		`+infoFlowDeliveryOrigin+`
		WHERE d.binding_id=$1 AND d.generation=$2 AND d.acked_at IS NULL AND d.cancelled_at IS NULL AND c.issue_id=$3 AND c.workspace_id=$4 AND c.deleted_at IS NULL AND c.created_at >= $6
		AND ((c.author_type='agent' AND c.author_id=ANY($5::uuid[])) OR (c.author_type='member' AND u.id IS NOT NULL))
		ORDER BY d.created_at,d.id LIMIT $7`, b.ID, b.Generation, b.IssueID, s.WorkspaceID, authors, cutoff, limit, s.AgentID)
	if err != nil {
		return nil, err
	}
	var out []infoFlowDelivery
	var legacy []infoFlowDelivery
	for rows.Next() {
		v := infoFlowDelivery{Conversation: c, IssueID: view.IssueID, Binding: view, NotifyUsers: []string{}, RequiresCheck: true}
		var notifyUsers []string
		var needsSnapshot bool
		if err = rows.Scan(&v.ID, &v.CommentID, &v.Content, &v.ReplyToMessageID, &v.AuthorAgentID, &v.AuthorName, &v.AuthorType, &notifyUsers, &needsSnapshot); err != nil {
			break
		}
		if c.Kind == "group" && v.AuthorType != "user" {
			v.NotifyUsers = notifyUsers
			if needsSnapshot {
				v.NotifyUsers = infoFlowNormalizeNotifyUsers("", notifyUsers, s.RobotID)
			}
		}
		if needsSnapshot {
			legacy = append(legacy, v)
		}
		out = append(out, v)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, v := range legacy {
		if c.Kind == "group" && v.AuthorType != "user" {
			v.NotifyUsers, err = infoFlowReplyAudience(ctx, tx, parseUUID(v.CommentID), parseUUID(s.WorkspaceID), b.IssueID)
			if err != nil {
				return nil, err
			}
			for i := range out {
				if out[i].ID == v.ID {
					out[i].NotifyUsers = v.NotifyUsers
				}
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE infoflow_issue_delivery SET notify_users=$2 WHERE id=$1 AND binding_id=$3 AND generation=$4 AND notify_users IS NULL`, v.ID, v.NotifyUsers, b.ID, b.Generation); err != nil {
			return nil, err
		}
	}
	for index := range out {
		if err := sh.infoFlowReplyImages(ctx, tx, s, &out[index]); err != nil {
			return nil, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE infoflow_issue_binding SET last_polled_at=clock_timestamp() WHERE id=$1`, b.ID); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func (h *Handler) InfoFlowIssueDeliveryAck(w http.ResponseWriter, r *http.Request) {
	if !infoFlowAuthenticate(w, r) {
		return
	}
	var req infoFlowDeliveryRequest
	if !infoFlowDecode(w, r, &req) {
		return
	}
	id, ok := parseUUIDOrBadRequest(&infoFlowCapture{}, chi.URLParam(r, "id"), "id")
	if !ok || !req.infoFlowScope.valid() || len(req.MessageIDs) > infoFlowMaxDeliveryMessageIDs {
		infoFlowError(w, 400, "invalid ack")
		return
	}
	if req.MessageID != "" {
		req.MessageIDs = append(req.MessageIDs, req.MessageID)
	}
	for _, v := range req.MessageIDs {
		if v == "" || len(v) > 256 || strings.ContainsRune(v, 0) {
			infoFlowError(w, 400, "invalid message id")
			return
		}
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		infoFlowError(w, 500, "begin")
		return
	}
	defer tx.Rollback(r.Context())
	var c infoFlowConversation
	var bindingID string
	err = tx.QueryRow(r.Context(), `SELECT b.conversation_kind,b.conversation_id,b.id::text FROM infoflow_issue_delivery d JOIN infoflow_issue_binding b ON b.id=d.binding_id
		WHERE d.id=$1 AND b.workspace_id=$3 AND ((b.conversation_kind='private' AND b.robot_id=$2 AND b.agent_id=$4) OR (b.conversation_kind='group' AND (b.leader_robot_id=$2 OR (b.leader_robot_id IS NULL AND b.robot_id=$2 AND b.agent_id=$4))))`, id, req.RobotID, req.WorkspaceID, req.AgentID).Scan(&c.Kind, &c.ID, &bindingID)
	if errors.Is(err, pgx.ErrNoRows) {
		infoFlowError(w, 404, "delivery")
		return
	}
	if err != nil {
		infoFlowError(w, 500, "delivery")
		return
	}
	if err = infoFlowLock(r.Context(), tx, req.infoFlowScope, c); err != nil {
		infoFlowError(w, 500, "lock")
		return
	}
	if c.Kind == "group" {
		b, e := infoFlowLoadBindingID(r.Context(), tx, req.infoFlowScope, c, bindingID)
		if e != nil {
			infoFlowError(w, 409, "stale binding")
			return
		}
		sh, _ := h.infoFlowScoped(tx)
		g := sh.infoFlowGroupView(r.Context(), b, req.WorkspaceID)
		if g.LeaderAgentID != "" && g.LeaderAgentID != strings.ToLower(req.AgentID) {
			infoFlowError(w, 404, "coordinator")
			return
		}
		if !b.IssueID.Valid || g.State != "active" || g.LeaderRobotID != req.RobotID || g.LeaderAgentID != strings.ToLower(req.AgentID) {
			infoFlowError(w, 409, "stale coordinator")
			return
		}
	} else {
		b, e := infoFlowLoadBindingID(r.Context(), tx, req.infoFlowScope, c, bindingID)
		if e != nil {
			infoFlowError(w, 409, "stale private binding")
			return
		}
		sh, _ := h.infoFlowScoped(tx)
		if v := sh.infoFlowPrivateView(r.Context(), b, req.WorkspaceID, req.AgentID); !b.IssueID.Valid || v.State != "active" {
			infoFlowError(w, 409, "stale private assignment")
			return
		}
	}
	if req.MessageIDs == nil {
		req.MessageIDs = []string{}
	}
	res, err := tx.Exec(r.Context(), `UPDATE infoflow_issue_delivery d SET acked_at=COALESCE(d.acked_at,clock_timestamp()),
		external_message_id=COALESCE(d.external_message_id,NULLIF($5,'')),
		external_message_ids=ARRAY(SELECT DISTINCT unnest(d.external_message_ids || $6::text[]))
		FROM infoflow_issue_binding b,comment c,issue i WHERE d.id=$1 AND b.id=d.binding_id AND b.workspace_id=$3 AND ((b.conversation_kind='private' AND b.robot_id=$2 AND b.agent_id=$4) OR (b.conversation_kind='group' AND b.leader_robot_id=$2))
		AND d.generation=b.generation AND c.id=d.comment_id AND c.issue_id=b.issue_id AND c.deleted_at IS NULL
		AND i.id=b.issue_id AND i.workspace_id=b.workspace_id AND i.infoflow_assignment_version=b.assignment_version`, id, req.RobotID, req.WorkspaceID, req.AgentID, req.MessageID, req.MessageIDs)
	if err != nil {
		infoFlowError(w, 500, "ack")
		return
	}
	if res.RowsAffected() != 1 {
		infoFlowError(w, 409, "stale delivery")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		infoFlowError(w, 500, "commit")
		return
	}
	writeJSON(w, 200, infoFlowRelayResponse{Handled: true, Silent: true})
}
