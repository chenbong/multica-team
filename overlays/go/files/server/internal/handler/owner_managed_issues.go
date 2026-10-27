package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Only this read-only route can select a target workspace after the server
// verifies the live executing agent and its owner's current issue authority.
// General task-token workspace resolution remains unchanged.
func (h *Handler) OwnerManagedIssues(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, private")
	if r.Method != http.MethodGet || r.Header.Get("X-Actor-Source") != "task_token" {
		writeError(w, 403, "A live agent task token is required")
		return
	}
	tokenUser, ok := parseUUIDOrBadRequest(w, requestUserID(r), "user id")
	if !ok {
		return
	}
	task, ok := parseUUIDOrBadRequest(w, r.Header.Get("X-Task-ID"), "run id")
	if !ok {
		return
	}
	agent, ok := parseUUIDOrBadRequest(w, r.Header.Get("X-Agent-ID"), "agent id")
	if !ok {
		return
	}
	source, ok := parseUUIDOrBadRequest(w, r.Header.Get("X-Workspace-ID"), "workspace id")
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		writeError(w, 503, "Read authorization unavailable")
		return
	}
	defer tx.Rollback(ctx)
	var owner pgtype.UUID
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !strings.HasPrefix(token, "mat_") {
		writeError(w, 403, "A live task credential is required")
		return
	}
	err = tx.QueryRow(ctx, `SELECT a.owner_id FROM agent_token_read_scope cap JOIN task_token tok ON tok.token_hash=cap.token_hash JOIN agent_task_queue t ON t.id=cap.task_id JOIN agent a ON a.id=cap.agent_id AND a.id=t.agent_id JOIN agent_runtime runtime ON runtime.id=t.runtime_id JOIN member agent_member ON agent_member.workspace_id=a.workspace_id AND agent_member.user_id=a.owner_id JOIN member runtime_member ON runtime_member.workspace_id=runtime.workspace_id AND runtime_member.user_id=runtime.owner_id WHERE cap.token_hash=$5 AND t.id=$1 AND a.id=$2 AND a.workspace_id=$3 AND cap.workspace_id=$3 AND runtime.workspace_id=$3 AND runtime.owner_id=$4 AND tok.user_id=$4 AND tok.task_id=t.id AND tok.agent_id=a.id AND tok.workspace_id=$3 AND tok.expires_at>now() AND a.owner_id=cap.owner_id AND t.status='running' AND a.archived_at IS NULL FOR SHARE OF cap,tok,t,a,runtime,agent_member,runtime_member NOWAIT`, task, agent, source, tokenUser, auth.HashToken(token)).Scan(&owner)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, 403, "A current owner-authorized run is required. Start a new run if this one predates the feature.")
		} else {
			writeError(w, 503, "Run authorization could not be validated")
		}
		return
	}
	rawID := chi.URLParam(r, "managedIssueId")
	if rawID == "" {
		h.listOwnerManagedIssues(w, r.WithContext(ctx), tx, owner)
		return
	}
	// At most two candidates: human identifiers can collide across workspaces.
	rows, err := tx.Query(ctx, `SELECT i.id FROM issue i JOIN workspace w ON w.id=i.workspace_id JOIN issue_manager im ON im.issue_id=i.id AND im.user_id=$1 JOIN member m ON m.workspace_id=i.workspace_id AND m.user_id=$1 WHERE i.id::text=$2 OR lower(w.issue_prefix||'-'||i.number::text)=lower($2) ORDER BY i.id LIMIT 2 FOR SHARE OF i,im,m`, owner, rawID)
	if err != nil {
		writeError(w, 503, "Issue authorization unavailable")
		return
	}
	ids := []pgtype.UUID{}
	for rows.Next() {
		var id pgtype.UUID
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			writeError(w, 503, "Issue authorization unavailable")
			return
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		writeError(w, 503, "Issue authorization unavailable")
		return
	}
	if len(ids) == 0 {
		writeError(w, 404, "Managed issue not found")
		return
	}
	if len(ids) > 1 {
		writeError(w, 409, "Issue identifier is ambiguous; use the UUID returned by the managed issue list")
		return
	}
	issue, err := h.Queries.GetIssue(ctx, ids[0])
	if err != nil {
		writeError(w, 404, "Managed issue not found")
		return
	}
	member, err := h.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{WorkspaceID: issue.WorkspaceID, UserID: owner})
	if err != nil {
		writeError(w, 404, "Managed issue not found")
		return
	}
	inner := r.Clone(middleware.SetMemberContext(ctx, uuidToString(issue.WorkspaceID), member))
	inner.Header = r.Header.Clone()
	inner.Header.Set("X-Workspace-ID", uuidToString(issue.WorkspaceID))
	inner.Header.Set("X-User-ID", uuidToString(owner))
	inner.Header.Del("X-Workspace-Slug")
	route := chi.NewRouteContext()
	route.URLParams.Add("id", uuidToString(issue.ID))
	inner = inner.WithContext(context.WithValue(inner.Context(), chi.RouteCtxKey, route))
	action := strings.Trim(chi.URLParam(r, "*"), "/")
	switch action {
	case "":
		h.GetIssue(w, inner)
	case "comments":
		h.ListComments(w, inner)
	case "timeline":
		h.ListTimeline(w, inner)
	case "runs":
		if inner.URL.Query().Get("scope") == "family" {
			writeError(w, 400, "Managed issue reads cannot expand to an unauthorized issue family")
			return
		}
		h.ListTasksByIssue(w, inner)
	case "attachments":
		h.ListAttachments(w, inner)
	case "properties":
		rows, e := tx.Query(ctx, `SELECT id,name,type,config FROM issue_property WHERE workspace_id=$1 AND id::text IN(SELECT jsonb_object_keys($2::jsonb)) ORDER BY id`, issue.WorkspaceID, issue.Properties)
		if e != nil {
			writeError(w, 503, "Property definitions unavailable")
			return
		}
		type property struct {
			ID     string          `json:"id"`
			Name   string          `json:"name"`
			Type   string          `json:"type"`
			Config json.RawMessage `json:"config"`
		}
		properties := []property{}
		for rows.Next() {
			var p property
			if e = rows.Scan(&p.ID, &p.Name, &p.Type, &p.Config); e != nil {
				rows.Close()
				writeError(w, 503, "Property definitions unavailable")
				return
			}
			properties = append(properties, p)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			writeError(w, 503, "Property definitions unavailable")
			return
		}
		writeJSON(w, 200, map[string]any{"values": json.RawMessage(issue.Properties), "definitions": properties})
	case "administrators":
		rows, e := tx.Query(ctx, `SELECT im.user_id,u.name FROM issue_manager im JOIN member m ON m.user_id=im.user_id AND m.workspace_id=$2 JOIN "user" u ON u.id=im.user_id WHERE im.issue_id=$1 ORDER BY im.created_at,im.user_id`, issue.ID, issue.WorkspaceID)
		if e != nil {
			writeError(w, 503, "Task administrators unavailable")
			return
		}
		administrators := []issueManagerResponse{}
		for rows.Next() {
			var item issueManagerResponse
			if e = rows.Scan(&item.UserID, &item.Name); e != nil {
				rows.Close()
				writeError(w, 503, "Task administrators unavailable")
				return
			}
			administrators = append(administrators, item)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			writeError(w, 503, "Task administrators unavailable")
			return
		}
		writeJSON(w, 200, map[string]any{"administrators": administrators, "can_manage": false, "can_delete": false})
	case "panels":
		rows, e := tx.Query(ctx, `SELECT `+webPanelColumns+` FROM resource_web_panel WHERE issue_id=$1 ORDER BY created_at,id LIMIT $2`, issue.ID, resourceWebPanelLimit)
		if e != nil {
			writeError(w, 503, "Unable to read task panels")
			return
		}
		panels := []resourceWebPanel{}
		access := webPanelAccess{scope: "issue", resourceID: issue.ID, workspaceID: issue.WorkspaceID, userID: owner}
		for rows.Next() {
			p, e := scanResourceWebPanel(rows, access)
			if e != nil {
				rows.Close()
				writeError(w, 503, "Unable to read task panels")
				return
			}
			p.CanManage = false
			panels = append(panels, p)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			writeError(w, 503, "Unable to read task panels")
			return
		}
		writeJSON(w, 200, map[string]any{"panels": panels, "can_add": false})
	default:
		parts := strings.Split(action, "/")
		if len(parts) == 3 && parts[0] == "runs" && parts[2] == "messages" {
			run, valid := parseUUIDOrBadRequest(w, parts[1], "run id")
			if !valid {
				return
			}
			var belongs bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_task_queue WHERE id=$1 AND issue_id=$2)`, run, issue.ID).Scan(&belongs); err != nil || !belongs {
				writeError(w, 404, "Run not found on managed issue")
				return
			}
			route.URLParams.Add("taskId", uuidToString(run))
			h.ListTaskMessagesByUser(w, inner)
			return
		}
		if (len(parts) == 2 || len(parts) == 3) && parts[0] == "attachments" {
			att, valid := parseUUIDOrBadRequest(w, parts[1], "attachment id")
			if !valid {
				return
			}
			var belongs bool
			err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM attachment a LEFT JOIN comment c ON c.id=a.comment_id AND c.workspace_id=a.workspace_id WHERE a.id=$1 AND a.workspace_id=$3 AND (a.issue_id=$2 OR c.issue_id=$2) AND a.chat_session_id IS NULL AND (a.comment_id IS NULL OR (c.id IS NOT NULL AND c.deleted_at IS NULL)))`, att, issue.ID, issue.WorkspaceID).Scan(&belongs)
			if err != nil || !belongs {
				writeError(w, 404, "Attachment not found on managed issue")
				return
			}
			route.URLParams.Add("id", uuidToString(att))
			if len(parts) == 2 {
				h.GetAttachmentByID(w, inner)
			} else if parts[2] == "download" {
				h.DownloadAttachment(w, inner)
			} else if parts[2] == "content" {
				h.GetAttachmentContent(w, inner)
			} else {
				writeError(w, 404, "Read endpoint not found")
			}
			return
		}
		writeError(w, 404, "Read endpoint not found")
	}
}

func (h *Handler) listOwnerManagedIssues(w http.ResponseWriter, r *http.Request, tx pgx.Tx, owner pgtype.UUID) {
	after := r.URL.Query().Get("after")
	var cursor pgtype.UUID
	if after != "" {
		var ok bool
		cursor, ok = parseUUIDOrBadRequest(w, after, "after")
		if !ok {
			return
		}
	}
	search := r.URL.Query().Get("q")
	if len(search) > 256 {
		writeError(w, 400, "Search is too long")
		return
	}
	rows, err := tx.Query(r.Context(), `SELECT i.id,w.id,w.name,w.issue_prefix||'-'||i.number::text,i.title,i.status FROM issue_manager im JOIN issue i ON i.id=im.issue_id JOIN member m ON m.workspace_id=i.workspace_id AND m.user_id=im.user_id JOIN workspace w ON w.id=i.workspace_id WHERE im.user_id=$1 AND ($2::uuid IS NULL OR i.id>$2) AND ($3='' OR i.title ILIKE '%'||$3||'%' OR (w.issue_prefix||'-'||i.number::text) ILIKE '%'||$3||'%') ORDER BY i.id LIMIT 101`, owner, cursor, search)
	if err != nil {
		writeError(w, 503, "Managed issue list unavailable")
		return
	}
	defer rows.Close()
	type item struct {
		ID            string `json:"id"`
		WorkspaceID   string `json:"workspace_id"`
		WorkspaceName string `json:"workspace_name"`
		Identifier    string `json:"identifier"`
		Title         string `json:"title"`
		Status        string `json:"status"`
	}
	items := []item{}
	for rows.Next() {
		var v item
		if err = rows.Scan(&v.ID, &v.WorkspaceID, &v.WorkspaceName, &v.Identifier, &v.Title, &v.Status); err != nil {
			writeError(w, 503, "Managed issue list unavailable")
			return
		}
		items = append(items, v)
	}
	if rows.Err() != nil {
		writeError(w, 503, "Managed issue list unavailable")
		return
	}
	next := ""
	if len(items) > 100 {
		items = items[:100]
		next = items[99].ID
	}
	writeJSON(w, 200, map[string]any{"issues": items, "next_after": next})
}
