package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

type personalSecret struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// PersonalSecrets deliberately has no workspace-admin override. Agent access
// resolves the owner from the live run, never a caller-supplied owner ID.
func (h *Handler) PersonalSecrets(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	user, ok := requireUserID(w, r)
	if !ok {
		return
	}
	owner, ok := parseUUIDOrBadRequest(w, user, "user id")
	if !ok {
		return
	}
	agentMode := strings.HasPrefix(r.URL.Path, "/api/agent/")
	if isMachineCredentialActor(r) && !agentMode {
		writeError(w, 403, "Personal secret management requires a human")
		return
	}
	if agentMode && (r.Header.Get("X-Actor-Source") != "task_token" || r.Method != http.MethodGet) {
		writeError(w, 403, "A live agent run is required")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "Secret storage unavailable")
		return
	}
	defer tx.Rollback(r.Context())
	if agentMode {
		task, valid := parseUUIDOrBadRequest(w, r.Header.Get("X-Task-ID"), "run id")
		if !valid {
			return
		}
		agent, valid := parseUUIDOrBadRequest(w, r.Header.Get("X-Agent-ID"), "agent id")
		if !valid {
			return
		}
		ws, valid := parseUUIDOrBadRequest(w, r.Header.Get("X-Workspace-ID"), "workspace id")
		if !valid {
			return
		}
		var authoritative pgtype.UUID
		err = tx.QueryRow(r.Context(), `SELECT a.owner_id FROM agent a JOIN agent_task_queue t ON t.agent_id=a.id JOIN member m ON m.workspace_id=a.workspace_id AND m.user_id=a.owner_id WHERE t.id=$1 AND a.id=$2 AND a.workspace_id=$3 AND a.owner_id=$4 AND t.status='running' AND a.archived_at IS NULL FOR SHARE OF a,t,m NOWAIT`, task, agent, ws, owner).Scan(&authoritative)
		if err != nil {
			writeError(w, 403, "The agent run is no longer authorized")
			return
		}
		owner = authoritative
	}
	idRaw := chi.URLParam(r, "secretId")
	var id pgtype.UUID
	if idRaw != "" {
		id, ok = parseUUIDOrBadRequest(w, idRaw, "secret id")
		if !ok {
			return
		}
	}
	fail := func(err error) {
		var pgErr *pgconn.PgError
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 404, "Secret not found")
		} else if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeError(w, 409, "A secret with this name already exists")
		} else {
			writeError(w, 500, "Unable to access personal secrets")
		}
	}
	if r.Method == http.MethodGet && idRaw == "" {
		rows, err := tx.Query(r.Context(), `SELECT id::text,name,description,updated_at FROM personal_secret WHERE owner_id=$1 ORDER BY name,id`, owner)
		if err != nil {
			fail(err)
			return
		}
		items := []personalSecret{}
		for rows.Next() {
			var item personalSecret
			if err = rows.Scan(&item.ID, &item.Name, &item.Description, &item.UpdatedAt); err != nil {
				rows.Close()
				fail(err)
				return
			}
			items = append(items, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			fail(err)
			return
		}
		if err = tx.Commit(r.Context()); err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, map[string]any{"secrets": items})
		return
	}
	if r.Method == http.MethodDelete && idRaw != "" && !agentMode {
		result, err := tx.Exec(r.Context(), `DELETE FROM personal_secret WHERE id=$1 AND owner_id=$2`, id, owner)
		if err != nil {
			fail(err)
			return
		}
		if result.RowsAffected() == 0 {
			fail(pgx.ErrNoRows)
			return
		}
		if err = tx.Commit(r.Context()); err != nil {
			fail(err)
			return
		}
		w.WriteHeader(204)
		return
	}
	key, err := secretbox.LoadKey("MULTICA_PLUGIN_SECRET_KEY")
	if err != nil {
		writeError(w, 503, "Secret encryption unavailable")
		return
	}
	box, err := secretbox.New(key)
	if err != nil {
		writeError(w, 503, "Secret encryption unavailable")
		return
	}
	if r.Method == http.MethodGet && idRaw != "" {
		var ciphertext []byte
		var item personalSecret
		err = tx.QueryRow(r.Context(), `SELECT id::text,name,description,updated_at,ciphertext FROM personal_secret WHERE id=$1 AND owner_id=$2 FOR SHARE`, id, owner).Scan(&item.ID, &item.Name, &item.Description, &item.UpdatedAt, &ciphertext)
		if err != nil {
			fail(err)
			return
		}
		value, err := openPersonalSecret(box, ciphertext, uuidToString(owner), item.ID)
		if err != nil {
			fail(err)
			return
		}
		if err = tx.Commit(r.Context()); err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, struct {
			personalSecret
			Value string `json:"value"`
		}{item, value})
		return
	}
	if agentMode || (r.Method != http.MethodPost && r.Method != http.MethodPut) || (r.Method == http.MethodPost && idRaw != "") || (r.Method == http.MethodPut && idRaw == "") {
		writeError(w, 405, "Unsupported secret operation")
		return
	}
	var body struct {
		Name        string  `json:"name"`
		Description string  `json:"description"`
		Value       *string `json:"value"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128*1024))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&body); err != nil {
		writeError(w, 400, "Invalid secret request")
		return
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		writeError(w, 400, "Expected one secret request")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || utf8.RuneCountInString(body.Name) > 120 || strings.ContainsAny(body.Name, "\x00\r\n") || len(body.Description) > 4000 || (body.Value != nil && (len(*body.Value) == 0 || len(*body.Value) > 64*1024)) {
		writeError(w, 400, "Invalid secret name, description or value")
		return
	}
	if r.Method == http.MethodPost && body.Value == nil {
		writeError(w, 400, "Secret value is required")
		return
	}
	if r.Method == http.MethodPost {
		id = dbid.NewV7()
	}
	var encrypted []byte
	if body.Value != nil {
		payload, _ := json.Marshal(struct {
			Owner string
			ID    string
			Value string
		}{uuidToString(owner), uuidToString(id), *body.Value})
		encrypted, err = box.Seal(payload)
		if err != nil {
			fail(err)
			return
		}
	}
	var item personalSecret
	if r.Method == http.MethodPost {
		err = tx.QueryRow(r.Context(), `INSERT INTO personal_secret(id,owner_id,name,description,ciphertext) VALUES($1,$2,$3,$4,$5) RETURNING id::text,name,description,updated_at`, id, owner, body.Name, body.Description, encrypted).Scan(&item.ID, &item.Name, &item.Description, &item.UpdatedAt)
	} else {
		err = tx.QueryRow(r.Context(), `UPDATE personal_secret SET name=$3,description=$4,ciphertext=COALESCE($5,ciphertext),updated_at=now() WHERE id=$1 AND owner_id=$2 RETURNING id::text,name,description,updated_at`, id, owner, body.Name, body.Description, encrypted).Scan(&item.ID, &item.Name, &item.Description, &item.UpdatedAt)
	}
	if err != nil {
		fail(err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		fail(err)
		return
	}
	status := 200
	if r.Method == http.MethodPost {
		status = 201
	}
	writeJSON(w, status, item)
}

func openPersonalSecret(box *secretbox.Box, ciphertext []byte, owner, id string) (string, error) {
	payload, err := box.Open(ciphertext)
	if err != nil {
		return "", err
	}
	var value struct {
		Owner string
		ID    string
		Value string
	}
	if err = json.Unmarshal(payload, &value); err != nil {
		return "", err
	}
	if value.Owner != owner || value.ID != id {
		return "", errors.New("secret identity mismatch")
	}
	return value.Value, nil
}
