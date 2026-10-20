package handler

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
)

var infoFlowCodeMention = regexp.MustCompile("(?s)```.*?```|~~~.*?~~~|`[^`]*`")
var infoFlowPlainMention = regexp.MustCompile(`(?:^|[^A-Za-z0-9._/@%-])@([\p{L}][\p{L}\p{N}._-]{0,127})`)
var infoFlowMentionURL = regexp.MustCompile(`https?://\S+`)

// Resolve explicit human mentions only from input/configuration, never output.
func infoFlowExplicitAudience(ctx context.Context, tx pgx.Tx, workspace pgtype.UUID, text string) ([]string, error) {
	text = infoFlowCodeMention.ReplaceAllString(text, "")
	var out []string
	for _, mention := range util.ParseMentions(text) {
		if mention.Type != "member" {
			continue
		}
		id, err := util.ParseUUID(mention.ID)
		if err != nil {
			continue
		}
		var email string
		err = tx.QueryRow(ctx, `SELECT u.email FROM "user" u JOIN member m ON m.user_id=u.id
			WHERE u.id=$1 AND m.workspace_id=$2`, id, workspace).Scan(&email)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if account := infoFlowEmailAccount(email); account != "" {
			out = append(out, account)
		}
	}
	plain := util.MentionRe.ReplaceAllString(text, "")
	for _, match := range infoFlowPlainMention.FindAllStringSubmatch(infoFlowMentionURL.ReplaceAllString(plain, ""), -1) {
		name := match[1]
		if strings.EqualFold(name, "all") || strings.EqualFold(name, "atall") {
			continue
		}
		if account := infoFlowHumanAccount(name); account != "" {
			// A named account need not be a workspace member. The Bridge
			// verifies actual membership separately in every destination group.
			out = append(out, account)
			continue
		}
		rows, err := tx.Query(ctx, `SELECT u.email FROM "user" u JOIN member m ON m.user_id=u.id
			WHERE m.workspace_id=$1 AND (split_part(lower(u.email),'@',1)=lower($2) OR u.name=$2) LIMIT 2`, workspace, name)
		if err != nil {
			return nil, err
		}
		var matches []string
		for rows.Next() {
			var email string
			if err = rows.Scan(&email); err != nil {
				break
			}
			matches = append(matches, email)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return nil, err
		}
		if len(matches) == 1 {
			if account := infoFlowEmailAccount(matches[0]); account != "" {
				out = append(out, account)
			}
		}
	}
	return infoFlowNormalizeNotifyUsers("", out, ""), nil
}

func infoFlowEmailAccount(email string) string {
	parts := strings.Split(strings.ToLower(email), "@")
	if len(parts) != 2 || parts[1] != strings.ToLower(os.Getenv("INFOFLOW_ISSUE_IDENTITY_DOMAIN")) {
		return ""
	}
	return infoFlowHumanAccount(parts[0])
}

func infoFlowInputAudience(ctx context.Context, tx pgx.Tx, comment, workspace, issue pgtype.UUID) ([]string, bool, error) {
	var authorType, content, email string
	var stored []string
	err := tx.QueryRow(ctx, `SELECT c.author_type,c.content,COALESCE(u.email,''),s.notify_users
		FROM comment c LEFT JOIN "user" u ON c.author_type='member' AND u.id=c.author_id
		LEFT JOIN infoflow_issue_source s ON s.comment_id=c.id
		WHERE c.id=$1 AND c.issue_id=$2 AND c.workspace_id=$3 AND c.deleted_at IS NULL
		ORDER BY s.created_at DESC LIMIT 1`, comment, issue, workspace).Scan(&authorType, &content, &email, &stored)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil || authorType != "member" {
		return nil, false, err
	}
	explicit, err := infoFlowExplicitAudience(ctx, tx, workspace, content)
	if err != nil {
		return nil, false, err
	}
	return infoFlowNormalizeNotifyUsers(infoFlowEmailAccount(email), append(stored, explicit...), ""), true, nil
}

// Walk execution inputs, stopping at the nearest human request or wakeup.
// A scheduled run never inherits its creator or an older private-chat audience.
func infoFlowReplyAudience(ctx context.Context, tx pgx.Tx, comment, workspace, issue pgtype.UUID) ([]string, error) {
	type node struct {
		kind string
		id   pgtype.UUID
	}
	queue := []node{{"comment", comment}}
	seen := map[string]bool{}
	for len(queue) > 0 && len(seen) < 256 {
		n := queue[0]
		queue = queue[1:]
		if !n.id.Valid {
			continue
		}
		key := n.kind + ":" + uuidToString(n.id)
		if seen[key] {
			continue
		}
		seen[key] = true
		if n.kind == "comment" {
			var task, parent pgtype.UUID
			err := tx.QueryRow(ctx, `SELECT source_task_id,parent_id FROM comment
				WHERE id=$1 AND workspace_id=$2 AND issue_id=$3 AND deleted_at IS NULL`, n.id, workspace, issue).Scan(&task, &parent)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if task.Valid {
				queue = append(queue, node{"task", task})
			} else {
				if users, human, err := infoFlowInputAudience(ctx, tx, n.id, workspace, issue); err != nil || human {
					return users, err
				}
				queue = append(queue, node{"comment", parent})
			}
			continue
		}
		var raw []byte
		var trigger, parent, delegated, retry, originator pgtype.UUID
		var coalesced []pgtype.UUID
		err := tx.QueryRow(ctx, `SELECT t.context,t.trigger_comment_id,t.coalesced_comment_ids,
			t.parent_task_id,t.delegated_from_task_id,t.retry_of_task_id,t.originator_user_id
			FROM agent_task_queue t JOIN agent a ON a.id=t.agent_id
			WHERE t.id=$1 AND t.issue_id=$2 AND a.workspace_id=$3`,
			n.id, issue, workspace).Scan(&raw, &trigger, &coalesced, &parent, &delegated, &retry, &originator)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var metadata struct {
			WakeupID          string  `json:"wakeup_id"`
			WakeupInstruction *string `json:"wakeup_instruction"`
		}
		if err = json.Unmarshal(raw, &metadata); err != nil && len(raw) != 0 {
			// Unknown execution metadata must not invent an audience or
			// prevent independent destinations from receiving task content.
			return []string{}, nil
		}
		if metadata.WakeupID != "" {
			// Only new runs contain the immutable instruction snapshot. Older
			// runs default to no AT rather than guessing from edited rules.
			if metadata.WakeupInstruction == nil {
				return []string{}, nil
			}
			return infoFlowExplicitAudience(ctx, tx, workspace, *metadata.WakeupInstruction)
		}
		var requesters, mentioned []string
		found := false
		rows, err := tx.Query(ctx, `SELECT sender,notify_users FROM infoflow_issue_source
			WHERE task_id=$1 AND issue_id=$2 ORDER BY created_at,id`, n.id, issue)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var sender string
			var users []string
			if err = rows.Scan(&sender, &users); err != nil {
				break
			}
			found = true
			requesters = append(requesters, sender)
			mentioned = append(mentioned, users...)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return nil, err
		}
		for _, input := range append([]pgtype.UUID{trigger}, coalesced...) {
			if !input.Valid {
				continue
			}
			values, human, err := infoFlowInputAudience(ctx, tx, input, workspace, issue)
			if err != nil {
				return nil, err
			}
			found = found || human
			if len(values) > 0 {
				requesters = append(requesters, values[0])
				mentioned = append(mentioned, values[1:]...)
			}
		}
		if found {
			return infoFlowNormalizeNotifyUsers("", append(requesters, mentioned...), ""), nil
		}
		if parent.Valid || delegated.Valid || retry.Valid || trigger.Valid {
			queue = append(queue, node{"task", parent}, node{"task", delegated}, node{"task", retry}, node{"comment", trigger})
		} else if originator.Valid {
			var email string
			err := tx.QueryRow(ctx, `SELECT u.email FROM "user" u JOIN member m ON m.user_id=u.id
				WHERE u.id=$1 AND m.workspace_id=$2`, originator, workspace).Scan(&email)
			if err == nil {
				return infoFlowNormalizeNotifyUsers(infoFlowEmailAccount(email), nil, ""), nil
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return nil, err
			}
		}
	}
	return []string{}, nil
}
