package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type issueManagerResponse struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
}

func (h *Handler) issueManagerPermission(ctx context.Context, issue db.Issue, user string) (bool, bool, error) {
	var manage, remove bool
	err := h.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issue_manager im JOIN member m ON m.user_id=im.user_id AND m.workspace_id=$2 WHERE im.issue_id=$1 AND im.user_id=$3), EXISTS(SELECT 1 FROM member WHERE workspace_id=$2 AND user_id=$3 AND role IN ('owner','admin'))`, issue.ID, issue.WorkspaceID, user).Scan(&remove, &manage)
	return manage || remove, remove, err
}
func (h *Handler) requireIssueDeletionManager(w http.ResponseWriter, r *http.Request, issue db.Issue) bool {
	if isMachineCredentialActor(r) || r.Header.Get("X-Agent-ID") != "" {
		writeError(w, 403, "Only a human task administrator can delete this issue")
		return false
	}
	if _, err := h.DB.Exec(r.Context(), `SELECT team_mesh_ensure_issue_managers($1)`, issue.ID); err != nil {
		writeError(w, 503, "Unable to validate task administrators")
		return false
	}
	_, remove, err := h.issueManagerPermission(r.Context(), issue, requestUserID(r))
	if err != nil {
		writeError(w, 503, "Unable to validate task administrators")
		return false
	}
	if !remove {
		writeError(w, 403, "Only task administrators can delete this issue; ask an administrator to update the task administrator list")
		return false
	}
	return true
}
func (h *Handler) IssueManagers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if r.Method != http.MethodGet && (isMachineCredentialActor(r) || r.Header.Get("X-Agent-ID") != "") {
		writeError(w, 403, "Only human administrators can change task administrators")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, 503, "Administrator storage unavailable")
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `SET LOCAL lock_timeout='2s'`); err != nil {
		writeError(w, 503, "Administrator storage unavailable")
		return
	}
	var currentWorkspace pgtype.UUID
	if err = tx.QueryRow(r.Context(), `SELECT workspace_id FROM issue WHERE id=$1 FOR UPDATE`, issue.ID).Scan(&currentWorkspace); err != nil || currentWorkspace != issue.WorkspaceID {
		writeError(w, 404, "Issue has moved or is unavailable")
		return
	}
	var actorRole string
	if err = tx.QueryRow(r.Context(), `SELECT role FROM member WHERE workspace_id=$1 AND user_id=$2 FOR SHARE`, issue.WorkspaceID, requestUserID(r)).Scan(&actorRole); err != nil {
		writeError(w, 404, "Issue not found")
		return
	}
	if _, err = tx.Exec(r.Context(), `SELECT team_mesh_ensure_issue_managers($1)`, issue.ID); err != nil {
		writeError(w, 503, "Unable to validate task administrators")
		return
	}
	var manager, workspaceAdmin bool
	err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM issue_manager im JOIN member m ON m.user_id=im.user_id AND m.workspace_id=$2 WHERE im.issue_id=$1 AND im.user_id=$3),EXISTS(SELECT 1 FROM member WHERE workspace_id=$2 AND user_id=$3 AND role IN ('owner','admin'))`, issue.ID, issue.WorkspaceID, requestUserID(r)).Scan(&manager, &workspaceAdmin)
	if err != nil {
		writeError(w, 503, "Administrator permission unavailable")
		return
	}
	if r.Method == http.MethodPut {
		if !manager && !workspaceAdmin {
			writeError(w, 403, "Task administrator or workspace administrator required")
			return
		}
		var body struct {
			IDs []string `json:"user_ids"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		dec.DisallowUnknownFields()
		if dec.Decode(&body) != nil || len(body.IDs) == 0 || len(body.IDs) > 100 {
			writeError(w, 400, "Select at least one current workspace member (maximum 100)")
			return
		}
		unique := map[string]bool{}
		ids := []pgtype.UUID{}
		for _, raw := range body.IDs {
			id, valid := parseUUIDOrBadRequest(w, raw, "user_id")
			if !valid {
				return
			}
			key := uuidToString(id)
			if !unique[key] {
				unique[key] = true
				ids = append(ids, id)
			}
		}
		// Share-lock the membership rows until the new list commits.
		rows, e := tx.Query(r.Context(), `SELECT user_id FROM member WHERE workspace_id=$1 AND user_id=ANY($2::uuid[]) ORDER BY id FOR SHARE`, issue.WorkspaceID, ids)
		if e != nil {
			writeError(w, 503, "Unable to validate members")
			return
		}
		count := 0
		for rows.Next() {
			count++
		}
		e = rows.Err()
		rows.Close()
		if e != nil || count != len(ids) {
			writeError(w, 400, "All task administrators must be current workspace members")
			return
		}
		if _, err = tx.Exec(r.Context(), `DELETE FROM issue_manager WHERE issue_id=$1`, issue.ID); err == nil {
			_, err = tx.Exec(r.Context(), `INSERT INTO issue_manager(issue_id,user_id) SELECT $1,unnest($2::uuid[])`, issue.ID, ids)
		}
		if err != nil {
			writeError(w, 503, "Unable to save task administrators")
			return
		}
		audit, _ := json.Marshal(map[string]any{"administrator_ids": body.IDs})
		_, err = tx.Exec(r.Context(), `INSERT INTO activity_log(workspace_id,issue_id,actor_type,actor_id,action,details) VALUES($1,$2,'member',$3,'administrators_changed',$4::jsonb)`, issue.WorkspaceID, issue.ID, requestUserID(r), string(audit))
		if err != nil {
			writeError(w, 503, "Unable to save task administrator audit")
			return
		}
		manager = unique[requestUserID(r)]
	}
	rows, err := tx.Query(r.Context(), `SELECT im.user_id,u.name FROM issue_manager im JOIN member m ON m.user_id=im.user_id AND m.workspace_id=$2 JOIN "user" u ON u.id=im.user_id WHERE im.issue_id=$1 ORDER BY im.created_at,im.user_id`, issue.ID, issue.WorkspaceID)
	if err != nil {
		writeError(w, 503, "Unable to read task administrators")
		return
	}
	result := []issueManagerResponse{}
	for rows.Next() {
		var v issueManagerResponse
		if err = rows.Scan(&v.UserID, &v.Name); err != nil {
			rows.Close()
			writeError(w, 503, "Unable to read task administrators")
			return
		}
		result = append(result, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || tx.Commit(r.Context()) != nil {
		writeError(w, 503, "Unable to read task administrators")
		return
	}
	human := !isMachineCredentialActor(r) && r.Header.Get("X-Agent-ID") == ""
	if r.Method == http.MethodPut {
		h.publish("issue:administrators_changed", uuidToString(issue.WorkspaceID), "member", requestUserID(r), map[string]string{"issue_id": uuidToString(issue.ID)})
	}
	writeJSON(w, 200, map[string]any{"administrators": result, "can_manage": human && (manager || workspaceAdmin), "can_delete": human && manager})
}

type issueDeleteActorKey struct{}

func withIssueDeleteActor(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, issueDeleteActorKey{}, user)
}
func validateIssueDeleteActor(ctx context.Context, q db.DBTX, issue db.Issue) error {
	user, _ := ctx.Value(issueDeleteActorKey{}).(string)
	if user == "" {
		return nil
	}
	var allowed bool
	err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issue_manager im JOIN member m ON m.user_id=im.user_id AND m.workspace_id=$2 WHERE im.issue_id=$1 AND im.user_id=$3)`, issue.ID, issue.WorkspaceID, user).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return errors.New("task administrator authorization changed")
	}
	return nil
}
func repairIssueManagersAfterDeparture(ctx context.Context, q db.DBTX, ws, user pgtype.UUID) error {
	rows, err := q.Query(ctx, `SELECT i.id FROM issue i JOIN issue_manager im ON im.issue_id=i.id WHERE i.workspace_id=$1 AND im.user_id=$2 ORDER BY i.id FOR UPDATE OF i`, ws, user)
	if err != nil {
		return err
	}
	ids := []pgtype.UUID{}
	for rows.Next() {
		var id pgtype.UUID
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = q.Exec(ctx, `DELETE FROM issue_manager WHERE issue_id=$1 AND user_id=$2`, id, user); err != nil {
			return err
		}
		if _, err = q.Exec(ctx, `SELECT team_mesh_ensure_issue_managers($1)`, id); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) cancelIssueForHumanDeletion(w http.ResponseWriter, r *http.Request, issue db.Issue) bool {
	err := h.TaskService.CancelIssueForHumanDeletion(r.Context(), issue.ID, issue.WorkspaceID, parseUUID(requestUserID(r)))
	if err != nil {
		writeError(w, 409, "Task workspace or administrator permissions changed; refresh before deleting")
		return false
	}
	return true
}
