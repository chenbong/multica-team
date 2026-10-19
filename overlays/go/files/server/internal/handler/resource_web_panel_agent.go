package handler

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

// AddCurrentIssueWebPanel is add-only. The target comes exclusively from the
// authenticated live run, never from a URL parameter or caller-supplied body.
// All human management and workspace panel endpoints keep their human guards.
func (h *Handler) AddCurrentIssueWebPanel(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Actor-Source") != "task_token" {
		writeError(w, http.StatusForbidden, "A current issue run is required")
		return
	}
	user, ok := requireUserID(w, r)
	if !ok {
		return
	}
	ownerID, ok := parseUUIDOrBadRequest(w, user, "user id")
	if !ok {
		return
	}
	agentID, ok := parseUUIDOrBadRequest(w, r.Header.Get("X-Agent-ID"), "agent id")
	if !ok {
		return
	}
	taskID, ok := parseUUIDOrBadRequest(w, r.Header.Get("X-Task-ID"), "run id")
	if !ok {
		return
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, r.Header.Get("X-Workspace-ID"), "workspace id")
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if !decodeResourceWebPanel(w, r, &body) {
		return
	}
	name, href, valid := validResourceWebPanel(body.Name, body.URL)
	if !valid {
		writeErrorCode(w, http.StatusBadRequest, "invalid_web_panel", "Use an HTTP(S) URL without embedded credentials; a custom page name is optional and limited to 120 characters")
		return
	}
	// This preliminary read only locates the resource. The run identity and
	// state are checked again under locks before any panel data is read/written.
	task, err := h.Queries.GetAgentTask(r.Context(), taskID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "Unable to resolve the current run")
		return
	}
	if errors.Is(err, pgx.ErrNoRows) || !task.IssueID.Valid {
		writeError(w, http.StatusForbidden, "This run has no current issue")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "Unable to add issue panel")
		return
	}
	defer tx.Rollback(r.Context())
	fail := func(err error) {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "55P03" {
			writeErrorCode(w, http.StatusConflict, "web_panel_run_busy", "The current run is changing; retry the request")
		} else if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusForbidden, "The current issue run is no longer authorized")
		} else {
			writeError(w, 500, "Unable to add issue panel")
		}
	}
	var locked pgtype.UUID
	if err = tx.QueryRow(r.Context(), `SELECT id FROM workspace WHERE id=$1 FOR KEY SHARE`, workspaceID).Scan(&locked); err != nil {
		fail(err)
		return
	}
	if err = tx.QueryRow(r.Context(), `SELECT id FROM issue WHERE id=$1 AND workspace_id=$2 FOR UPDATE`, task.IssueID, workspaceID).Scan(&locked); err != nil {
		fail(err)
		return
	}
	var originator pgtype.UUID
	// NOWAIT avoids a lock-order cycle with terminal transitions. Holding the
	// run share lock fences cancellation/completion until this write commits.
	if err = tx.QueryRow(r.Context(), `SELECT originator_user_id FROM agent_task_queue WHERE id=$1 AND agent_id=$2 AND issue_id=$3 AND status='running' FOR SHARE NOWAIT`, taskID, agentID, task.IssueID).Scan(&originator); err != nil {
		fail(err)
		return
	}
	if !originator.Valid {
		writeError(w, 403, "A human originator is required")
		return
	}
	if err = tx.QueryRow(r.Context(), `SELECT id FROM agent WHERE id=$1 AND workspace_id=$2 AND owner_id=$3 AND archived_at IS NULL FOR SHARE NOWAIT`, agentID, workspaceID, ownerID).Scan(&locked); err != nil {
		fail(err)
		return
	}
	var role string
	for _, memberID := range []pgtype.UUID{ownerID, originator} {
		if err = tx.QueryRow(r.Context(), `SELECT role FROM member WHERE workspace_id=$1 AND user_id=$2 FOR SHARE NOWAIT`, workspaceID, memberID).Scan(&role); err != nil {
			fail(err)
			return
		}
	}
	a := webPanelAccess{scope: "issue", resourceID: task.IssueID, workspaceID: workspaceID, userID: originator, role: role}
	panel, err := scanResourceWebPanel(tx.QueryRow(r.Context(), `SELECT `+webPanelColumns+` FROM resource_web_panel WHERE issue_id=$1 AND url=$2`, task.IssueID, href), a)
	created := false
	if errors.Is(err, pgx.ErrNoRows) {
		var count int
		if err = tx.QueryRow(r.Context(), `SELECT count(*) FROM resource_web_panel WHERE issue_id=$1`, task.IssueID).Scan(&count); err != nil {
			fail(err)
			return
		}
		if count >= resourceWebPanelLimit {
			writeErrorCode(w, 409, "web_panel_limit", "This issue has reached its web panel limit")
			return
		}
		panel, err = scanResourceWebPanel(tx.QueryRow(r.Context(), `INSERT INTO resource_web_panel(id,issue_id,created_by,name,url,created_by_agent_id,source_task_id) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING `+webPanelColumns, dbid.NewV7(), task.IssueID, originator, name, href, agentID, taskID), a)
		created = true
	}
	if err != nil {
		fail(err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		fail(err)
		return
	}
	if created && h.Bus != nil {
		h.Bus.Publish(events.Event{Type: "resource_web_panel:changed", WorkspaceID: uuidToString(workspaceID), ActorType: "agent", ActorID: uuidToString(agentID), Payload: map[string]string{"scope": "issue", "resource_id": uuidToString(task.IssueID), "workspace_id": uuidToString(workspaceID)}})
	}
	// The machine may add only; do not advertise human management rights in
	// its response. A repeated URL returns the existing item without mutation.
	panel.CanManage = false
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, struct {
		resourceWebPanel
		AlreadyExists bool `json:"already_exists"`
	}{panel, !created})
}
