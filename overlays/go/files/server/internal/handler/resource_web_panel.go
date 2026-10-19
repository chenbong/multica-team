package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

const resourceWebPanelLimit = 50

type resourceWebPanel struct {
	ID               string    `json:"id"`
	Scope            string    `json:"scope"`
	ResourceID       string    `json:"resource_id"`
	WorkspaceID      string    `json:"workspace_id"`
	CreatedBy        string    `json:"created_by"`
	Name             string    `json:"name"`
	URL              string    `json:"url"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	CanManage        bool      `json:"can_manage"`
	CreatedByAgentID *string   `json:"created_by_agent_id"`
	SourceTaskID     *string   `json:"source_task_id"`
}

type webPanelAccess struct {
	scope       string
	resourceID  pgtype.UUID
	workspaceID pgtype.UUID
	userID      pgtype.UUID
	role        string
}

// The only interpolated SQL identifier comes from this closed enum, never input.
func (a webPanelAccess) column() string {
	if a.scope == "issue" {
		return "issue_id"
	}
	return "workspace_id"
}

func (a webPanelAccess) canManage(createdBy string) bool {
	return createdBy == uuidToString(a.userID) || roleAllowed(a.role, "owner", "admin")
}

func validResourceWebPanel(name, rawURL string) (string, string, bool) {
	name, rawURL = strings.TrimSpace(name), strings.TrimSpace(rawURL)
	if name == "" {
		name = rawURL
	}
	// URL-derived names retain the complete navigation URL, even beyond the
	// custom-name character limit. The URL's byte and safety limits still apply.
	if (name != rawURL && utf8.RuneCountInString(name) > 120) || len(rawURL) > 4096 || strings.ContainsAny(name, "\x00\r\n") || strings.IndexFunc(rawURL, func(ch rune) bool { return ch <= 0x20 || ch == 0x7f }) >= 0 {
		return "", "", false
	}
	u, err := url.Parse(rawURL)
	if err != nil || u == nil || (!strings.EqualFold(u.Scheme, "https") && !strings.EqualFold(u.Scheme, "http")) || u.Hostname() == "" || u.User != nil || u.Opaque != "" {
		return "", "", false
	}
	// The original query and fragment may be application routing state. Never
	// strip them or append platform/user identifiers or authentication material.
	return name, rawURL, true
}

func decodeResourceWebPanel(w http.ResponseWriter, r *http.Request, body any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 12*1024))
	d.DisallowUnknownFields()
	if err := d.Decode(body); err != nil {
		writeErrorCode(w, http.StatusBadRequest, "invalid_web_panel", "Invalid panel request")
		return false
	}
	if err := d.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeErrorCode(w, http.StatusBadRequest, "invalid_web_panel", "Expected one panel request")
		return false
	}
	return true
}

// Lock the resource and recheck membership inside the same transaction as the
// list/write. A transferred issue must immediately inherit the destination's
// access rules; stale source-workspace requests cannot create or edit a panel.
func (h *Handler) lockWebPanelAccess(w http.ResponseWriter, r *http.Request, scope string) (pgx.Tx, webPanelAccess, bool) {
	var a webPanelAccess
	if isMachineCredentialActor(r) {
		writeError(w, http.StatusForbidden, "Web panels are managed by human members")
		return nil, a, false
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return nil, a, false
	}
	a.userID, ok = parseUUIDOrBadRequest(w, userID, "user id")
	if !ok {
		return nil, a, false
	}
	a.scope = scope
	if scope == "issue" {
		issue, found := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
		if !found {
			return nil, a, false
		}
		a.resourceID, a.workspaceID = issue.ID, issue.WorkspaceID
	} else {
		a.resourceID, ok = parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
		if !ok {
			return nil, a, false
		}
		a.workspaceID = a.resourceID
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Unable to load web panels")
		return nil, a, false
	}
	fail := func(err error) (pgx.Tx, webPanelAccess, bool) {
		_ = tx.Rollback(r.Context())
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "resource not found")
		} else {
			writeError(w, http.StatusInternalServerError, "Unable to load web panels")
		}
		return nil, a, false
	}
	var locked pgtype.UUID
	// Workspace before issue follows the issue-transfer/delete lock order.
	lock := "FOR KEY SHARE"
	if scope == "workspace" {
		lock = "FOR UPDATE"
	}
	err = tx.QueryRow(r.Context(), `SELECT id FROM workspace WHERE id=$1 `+lock, a.workspaceID).Scan(&locked)
	if err != nil {
		return fail(err)
	}
	if scope == "issue" {
		err = tx.QueryRow(r.Context(), `SELECT id FROM issue WHERE id=$1 AND workspace_id=$2 FOR UPDATE`, a.resourceID, a.workspaceID).Scan(&locked)
		if err != nil {
			return fail(err)
		}
	}
	err = tx.QueryRow(r.Context(), `SELECT role FROM member WHERE workspace_id=$1 AND user_id=$2 FOR SHARE`, a.workspaceID, a.userID).Scan(&a.role)
	if err != nil {
		return fail(err)
	}
	return tx, a, true
}

func scanResourceWebPanel(row pgx.Row, a webPanelAccess) (resourceWebPanel, error) {
	p := resourceWebPanel{Scope: a.scope, ResourceID: uuidToString(a.resourceID), WorkspaceID: uuidToString(a.workspaceID)}
	err := row.Scan(&p.ID, &p.CreatedBy, &p.Name, &p.URL, &p.CreatedAt, &p.UpdatedAt, &p.CreatedByAgentID, &p.SourceTaskID)
	p.CanManage = a.canManage(p.CreatedBy)
	return p, err
}

const webPanelColumns = `id::text,created_by::text,name,url,created_at,updated_at,created_by_agent_id::text,source_task_id::text`

func (h *Handler) listResourceWebPanels(w http.ResponseWriter, r *http.Request, scope string) {
	tx, a, ok := h.lockWebPanelAccess(w, r, scope)
	if !ok {
		return
	}
	defer tx.Rollback(r.Context())
	rows, err := tx.Query(r.Context(), `SELECT `+webPanelColumns+` FROM resource_web_panel WHERE `+a.column()+`=$1 ORDER BY created_at,id LIMIT $2`, a.resourceID, resourceWebPanelLimit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Unable to load web panels")
		return
	}
	panels := make([]resourceWebPanel, 0)
	for rows.Next() {
		panel, e := scanResourceWebPanel(rows, a)
		if e != nil {
			rows.Close()
			writeError(w, http.StatusInternalServerError, "Unable to load web panels")
			return
		}
		panels = append(panels, panel)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || tx.Commit(r.Context()) != nil {
		writeError(w, http.StatusInternalServerError, "Unable to load web panels")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"panels": panels, "can_add": true, "limit": resourceWebPanelLimit})
}

func (h *Handler) mutateResourceWebPanel(w http.ResponseWriter, r *http.Request, scope string) {
	var body struct {
		Name *string `json:"name"`
		URL  *string `json:"url"`
	}
	if r.Method != http.MethodDelete && !decodeResourceWebPanel(w, r, &body) {
		return
	}
	tx, a, ok := h.lockWebPanelAccess(w, r, scope)
	if !ok {
		return
	}
	defer tx.Rollback(r.Context())
	var panel resourceWebPanel
	var err error
	if r.Method == http.MethodPost {
		if body.URL == nil {
			writeErrorCode(w, http.StatusBadRequest, "invalid_web_panel", "Panel URL is required")
			return
		}
		var count int
		if err = tx.QueryRow(r.Context(), `SELECT count(*) FROM resource_web_panel WHERE `+a.column()+`=$1`, a.resourceID).Scan(&count); err != nil {
			writeError(w, http.StatusInternalServerError, "Unable to add web panel")
			return
		}
		if count >= resourceWebPanelLimit {
			writeErrorCode(w, http.StatusConflict, "web_panel_limit", "This resource has reached its web panel limit")
			return
		}
		panel = resourceWebPanel{ID: uuidToString(dbid.NewV7()), CreatedBy: uuidToString(a.userID)}
	} else {
		panelID, valid := parseUUIDOrBadRequest(w, chi.URLParam(r, "panelId"), "panel id")
		if !valid {
			return
		}
		panel, err = scanResourceWebPanel(tx.QueryRow(r.Context(), `SELECT `+webPanelColumns+` FROM resource_web_panel WHERE id=$1 AND `+a.column()+`=$2 FOR UPDATE`, panelID, a.resourceID), a)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "panel not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Unable to load web panel")
			return
		}
		if !panel.CanManage {
			writeError(w, http.StatusForbidden, "Only the panel creator or workspace administrators can manage this panel")
			return
		}
	}
	if r.Method == http.MethodDelete {
		_, err = tx.Exec(r.Context(), `DELETE FROM resource_web_panel WHERE id=$1 AND `+a.column()+`=$2`, panel.ID, a.resourceID)
	} else {
		if body.Name == nil && body.URL != nil && panel.Name == panel.URL {
			panel.Name = ""
		}
		if body.Name != nil {
			panel.Name = *body.Name
		}
		if body.URL != nil {
			panel.URL = *body.URL
		}
		name, href, valid := validResourceWebPanel(panel.Name, panel.URL)
		if !valid {
			writeErrorCode(w, http.StatusBadRequest, "invalid_web_panel", "Use an HTTP(S) URL without embedded credentials; a custom page name is optional and limited to 120 characters")
			return
		}
		if r.Method == http.MethodPost {
			panel, err = scanResourceWebPanel(tx.QueryRow(r.Context(), `INSERT INTO resource_web_panel(id,`+a.column()+`,created_by,name,url) VALUES($1,$2,$3,$4,$5) RETURNING `+webPanelColumns, panel.ID, a.resourceID, a.userID, name, href), a)
		} else {
			panel, err = scanResourceWebPanel(tx.QueryRow(r.Context(), `UPDATE resource_web_panel SET name=$3,url=$4,updated_at=now() WHERE id=$1 AND `+a.column()+`=$2 RETURNING `+webPanelColumns, panel.ID, a.resourceID, name, href), a)
		}
	}
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeErrorCode(w, http.StatusConflict, "web_panel_exists", "This URL is already linked to the resource")
		} else {
			writeError(w, http.StatusInternalServerError, "Unable to save web panel")
		}
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "Unable to save web panel")
		return
	}
	if h.Bus != nil {
		h.Bus.Publish(events.Event{Type: "resource_web_panel:changed", WorkspaceID: uuidToString(a.workspaceID), ActorType: "member", ActorID: uuidToString(a.userID), Payload: map[string]string{"scope": scope, "resource_id": uuidToString(a.resourceID), "workspace_id": uuidToString(a.workspaceID)}})
	}
	if r.Method == http.MethodDelete {
		w.WriteHeader(http.StatusNoContent)
	} else if r.Method == http.MethodPost {
		writeJSON(w, http.StatusCreated, panel)
	} else {
		writeJSON(w, http.StatusOK, panel)
	}
}

func (h *Handler) ListIssueWebPanels(w http.ResponseWriter, r *http.Request) {
	h.listResourceWebPanels(w, r, "issue")
}
func (h *Handler) CreateIssueWebPanel(w http.ResponseWriter, r *http.Request) {
	h.mutateResourceWebPanel(w, r, "issue")
}
func (h *Handler) UpdateIssueWebPanel(w http.ResponseWriter, r *http.Request) {
	h.mutateResourceWebPanel(w, r, "issue")
}
func (h *Handler) DeleteIssueWebPanel(w http.ResponseWriter, r *http.Request) {
	h.mutateResourceWebPanel(w, r, "issue")
}
func (h *Handler) ListWorkspaceWebPanels(w http.ResponseWriter, r *http.Request) {
	h.listResourceWebPanels(w, r, "workspace")
}
func (h *Handler) CreateWorkspaceWebPanel(w http.ResponseWriter, r *http.Request) {
	h.mutateResourceWebPanel(w, r, "workspace")
}
func (h *Handler) UpdateWorkspaceWebPanel(w http.ResponseWriter, r *http.Request) {
	h.mutateResourceWebPanel(w, r, "workspace")
}
func (h *Handler) DeleteWorkspaceWebPanel(w http.ResponseWriter, r *http.Request) {
	h.mutateResourceWebPanel(w, r, "workspace")
}
