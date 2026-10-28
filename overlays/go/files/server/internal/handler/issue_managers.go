package handler

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Kept for compatibility with older clients, but the old list no longer
// confers authority and can no longer be edited.
type issueManagerResponse struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
}

func isHumanIssueActor(r *http.Request) bool {
	return !isMachineCredentialActor(r) && r.Header.Get("X-Agent-ID") == "" && r.Header.Get("X-Daemon-ID") == ""
}

func (h *Handler) requireIssueDeletionManager(w http.ResponseWriter, r *http.Request, issue db.Issue) bool {
	if !isHumanIssueActor(r) {
		writeError(w, http.StatusForbidden, "Only a human can delete an issue")
		return false
	}
	allowed, err := h.Queries.CanManageIssueByCreator(r.Context(), issue.ID, issue.WorkspaceID, parseUUID(requestUserID(r)))
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Unable to validate issue deletion permission")
		return false
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "Only the issue creator, creator agent owner, or workspace owner/admin can delete this issue")
		return false
	}
	return true
}

// IssuePermissions exposes only the caller's current permission, never the old
// administrator data. The server repeats the check when applying a deletion.
func (h *Handler) IssuePermissions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	allowed := false
	if isHumanIssueActor(r) {
		var err error
		allowed, err = h.Queries.CanManageIssueByCreator(r.Context(), issue.ID, issue.WorkspaceID, parseUUID(requestUserID(r)))
		if err != nil {
			writeError(w, 503, "Unable to validate issue permissions")
			return
		}
	}
	writeJSON(w, 200, map[string]any{"can_delete": allowed})
}

func (h *Handler) IssueManagers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusGone, "Task administrators have been retired")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	allowed := false
	if isHumanIssueActor(r) {
		var err error
		allowed, err = h.Queries.CanManageIssueByCreator(r.Context(), issue.ID, issue.WorkspaceID, parseUUID(requestUserID(r)))
		if err != nil {
			writeError(w, 503, "Unable to validate issue permissions")
			return
		}
	}
	writeJSON(w, 200, map[string]any{"administrators": []issueManagerResponse{}, "can_manage": false, "can_delete": allowed})
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
	return db.New(q).LockManagedIssueForHumanDeletion(ctx, issue.ID, issue.WorkspaceID, parseUUID(user))
}

func (h *Handler) cancelIssueForHumanDeletion(w http.ResponseWriter, r *http.Request, issue db.Issue) bool {
	err := h.TaskService.CancelIssueForHumanDeletion(r.Context(), issue.ID, issue.WorkspaceID, parseUUID(requestUserID(r)))
	if err != nil {
		writeError(w, 409, "Task workspace or deletion permission changed; refresh before deleting")
		return false
	}
	return true
}
