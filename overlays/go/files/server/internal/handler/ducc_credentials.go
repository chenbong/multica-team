package handler

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/util/secretbox"
)

var duccAccountName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

func (h *Handler) duccIdentity(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if isMachineCredentialActor(r) {
		writeError(w, 403, "human account credential required")
		return "", "", false
	}
	id, ok := requireUserID(w, r)
	if !ok {
		return "", "", false
	}
	var email string
	if err := h.DB.QueryRow(r.Context(), `SELECT email FROM "user" WHERE id=$1`, id).Scan(&email); err != nil {
		writeError(w, 401, "account unavailable")
		return "", "", false
	}
	name := strings.Split(strings.ToLower(email), "@")[0]
	if !duccAccountName.MatchString(name) {
		writeError(w, 400, "unsupported account name")
		return "", "", false
	}
	if _, err := h.DB.Exec(r.Context(), `INSERT INTO ducc_credential(user_id) VALUES($1) ON CONFLICT(user_id) DO NOTHING`, id); err != nil {
		writeError(w, 503, "ducc credential storage unavailable")
		return "", "", false
	}
	return id, name, true
}

// Metadata only: no browser endpoint returns the provider credential.
func (h *Handler) GetDuccCredential(w http.ResponseWriter, r *http.Request) {
	id, name, ok := h.duccIdentity(w, r)
	if !ok {
		return
	}
	var enabled, imported bool
	var version int64
	var source, sourceName, pending string
	var at *time.Time
	if err := h.DB.QueryRow(r.Context(), `SELECT c.enabled,c.sealed IS NOT NULL,c.version,c.source_daemon,c.pending_daemon,c.imported_at,coalesce(s.name,'') FROM ducc_credential c LEFT JOIN ducc_machine s ON s.user_id=c.user_id AND s.daemon_id=c.source_daemon WHERE c.user_id=$1`, id).Scan(&enabled, &imported, &version, &source, &pending, &at, &sourceName); err != nil {
		writeError(w, 500, "status unavailable")
		return
	}
	rows, err := h.DB.Query(r.Context(), `WITH computers AS (
 SELECT r.daemon_id, max(coalesce(nullif(r.custom_name,''),r.name)) AS name, max(r.last_seen_at) AS seen
 FROM agent_runtime r WHERE r.owner_id=$1 AND r.daemon_id IS NOT NULL AND r.runtime_mode='local'
 AND EXISTS(SELECT 1 FROM member m WHERE m.workspace_id=r.workspace_id AND m.user_id=$1)
 GROUP BY r.daemon_id)
 SELECT c.daemon_id,c.name,s.last_seen_at,s.client_version,s.installed,s.credential_state,s.error_code
 FROM computers c LEFT JOIN ducc_machine s ON s.daemon_id=c.daemon_id AND s.user_id=$1
 ORDER BY c.name`, id)
	if err != nil {
		writeError(w, 500, "computer list unavailable")
		return
	}
	defer rows.Close()
	machines := []map[string]any{}
	for rows.Next() {
		var did, label string
		var seen *time.Time
		var v, state, e *string
		var installed *bool
		if err := rows.Scan(&did, &label, &seen, &v, &installed, &state, &e); err != nil {
			writeError(w, 500, "computer status unavailable")
			return
		}
		machines = append(machines, map[string]any{"daemon_id": did, "name": label, "last_seen_at": seen, "supported": v != nil, "online": seen != nil && time.Since(*seen) < 2*time.Minute, "installed": installed, "state": state, "error_code": e})
	}
	if rows.Err() != nil {
		writeError(w, 500, "computer status unavailable")
		return
	}
	writeJSON(w, 200, map[string]any{"username": name, "enabled": enabled, "imported": imported, "version": version, "source_daemon": source, "source_name": sourceName, "pending_daemon": pending, "imported_at": at, "machines": machines})
}

func (h *Handler) UpdateDuccCredential(w http.ResponseWriter, r *http.Request) {
	id, _, ok := h.duccIdentity(w, r)
	if !ok {
		return
	}
	var req struct {
		Action  string `json:"action"`
		Daemon  string `json:"daemon_id"`
		Enabled bool   `json:"enabled"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&req) != nil {
		writeError(w, 400, "invalid request")
		return
	}
	var err error
	switch req.Action {
	case "enable":
		_, err = h.DB.Exec(r.Context(), `UPDATE ducc_credential SET enabled=$2,request_id='',pending_daemon='',updated_at=now() WHERE user_id=$1`, id, req.Enabled)
	case "import":
		var allowed bool
		err = h.DB.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM ducc_machine s WHERE s.user_id=$1 AND s.daemon_id=$2 AND s.installed AND s.last_seen_at>now()-interval '2 minutes' AND EXISTS(SELECT 1 FROM agent_runtime r JOIN member m ON m.workspace_id=r.workspace_id AND m.user_id=$1 WHERE r.owner_id=$1 AND r.daemon_id=s.daemon_id AND r.runtime_mode='local'))`, id, req.Daemon).Scan(&allowed)
		if err != nil || !allowed {
			writeError(w, 409, "computer offline, unsupported or not owned by you")
			return
		}
		_, err = h.DB.Exec(r.Context(), `UPDATE ducc_credential SET pending_daemon=$2,request_id=$3,updated_at=now() WHERE user_id=$1`, id, req.Daemon, randomID())
	case "remove":
		_, err = h.DB.Exec(r.Context(), `UPDATE ducc_credential SET sealed=NULL,enabled=false,source_daemon='',pending_daemon='',request_id='',imported_at=NULL,version=version+1,updated_at=now() WHERE user_id=$1`, id)
	default:
		writeError(w, 400, "unknown action")
		return
	}
	if err != nil {
		writeError(w, 500, "update failed")
		return
	}
	w.WriteHeader(204)
}

// Agent task tokens are refused. The user's daemon PAT owns all requests and
// namespaces daemon IDs; no request may supply another user ID or a file path.
func (h *Handler) SyncDuccCredential(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer mul_") {
		writeError(w, 403, "daemon personal access token required")
		return
	}
	id, name, ok := h.duccIdentity(w, r)
	if !ok {
		return
	}
	var req struct {
		Daemon          string `json:"daemon_id"`
		Name            string `json:"name"`
		Version         string `json:"client_version"`
		Phase           string `json:"phase"`
		Installed       bool   `json:"installed"`
		State           string `json:"state"`
		Error           string `json:"error_code"`
		Credential      string `json:"credential"`
		RequestID       string `json:"request_id"`
		ExpectedVersion int64  `json:"expected_version"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 24*1024)).Decode(&req) != nil || !duccAccountName.MatchString(req.Daemon) || len(req.Name) > 256 || len(req.Version) > 64 {
		writeError(w, 400, "invalid sync request")
		return
	}
	switch req.State {
	case "unknown", "missing", "present", "ready", "invalid", "installing", "install_failed":
	default:
		writeError(w, 400, "invalid state")
		return
	}
	switch req.Error {
	case "", "install_failed", "credential_invalid", "credential_conflict", "validation_failed", "unsupported_os", "sync_failed":
	default:
		writeError(w, 400, "invalid error code")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "sync unavailable")
		return
	}
	defer tx.Rollback(r.Context())
	var enabled bool
	var sealed []byte
	var version int64
	var pending, request string
	err = tx.QueryRow(r.Context(), `SELECT enabled,sealed,version,pending_daemon,request_id FROM ducc_credential WHERE user_id=$1 FOR UPDATE`, id).Scan(&enabled, &sealed, &version, &pending, &request)
	if err != nil {
		writeError(w, 500, "sync unavailable")
		return
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO ducc_machine(user_id,daemon_id,name,client_version,installed,credential_state,error_code) VALUES($1,$2,$3,$4,$5,$6,$7)
 ON CONFLICT(user_id,daemon_id) DO UPDATE SET name=excluded.name,client_version=excluded.client_version,installed=excluded.installed,credential_state=excluded.credential_state,error_code=excluded.error_code,last_seen_at=now()`, id, req.Daemon, req.Name, req.Version, req.Installed, req.State, req.Error)
	if err != nil {
		writeError(w, 500, "sync unavailable")
		return
	}
	action := "none"
	credential := ""
	manual := pending == req.Daemon && request != ""
	if req.Phase == "upload" {
		if (!manual && !(enabled && len(sealed) == 0 && pending == "")) || version != req.ExpectedVersion || request != req.RequestID {
			writeError(w, 409, "import selection changed")
			return
		}
		if len(req.Credential) == 0 || len(req.Credential) > 16384 || req.State != "ready" {
			writeError(w, 400, "credential validation required")
			return
		}
		key, e := secretbox.LoadKey("MULTICA_PLUGIN_SECRET_KEY")
		if e != nil {
			writeError(w, 503, "credential encryption unavailable")
			return
		}
		box, e := secretbox.New(key)
		if e != nil {
			writeError(w, 503, "credential encryption unavailable")
			return
		}
		sealed, e = box.Seal([]byte(req.Credential))
		if e != nil {
			writeError(w, 500, "credential encryption failed")
			return
		}
		_, err = tx.Exec(r.Context(), `UPDATE ducc_credential SET sealed=$2,version=version+1,source_daemon=$3,imported_at=now(),pending_daemon='',request_id='',updated_at=now() WHERE user_id=$1`, id, sealed, req.Daemon)
		version++
	} else if req.Phase == "download" {
		if !enabled || len(sealed) == 0 || req.State != "missing" || version != req.ExpectedVersion {
			writeError(w, 409, "credential policy changed")
			return
		}
		key, e := secretbox.LoadKey("MULTICA_PLUGIN_SECRET_KEY")
		if e != nil {
			writeError(w, 503, "credential encryption unavailable")
			return
		}
		box, e := secretbox.New(key)
		if e != nil {
			writeError(w, 503, "credential encryption unavailable")
			return
		}
		plain, e := box.Open(sealed)
		if e != nil {
			writeError(w, 500, "credential decryption failed")
			return
		}
		credential = string(plain)
	} else if req.Phase == "poll" || req.Phase == "report" {
		if manual && (req.State == "missing" || req.State == "invalid") {
			_, err = tx.Exec(r.Context(), `UPDATE ducc_credential SET pending_daemon='',request_id='' WHERE user_id=$1`, id)
		}
		if manual && req.Installed && req.State == "present" {
			action = "import"
		}
		if enabled {
			if !req.Installed {
				action = "install"
			} else if pending == "" && len(sealed) == 0 && req.State == "present" {
				action = "import"
			} else if len(sealed) > 0 && req.State == "missing" {
				action = "download"
			}
		}
	} else {
		writeError(w, 400, "unknown sync phase")
		return
	}
	if err != nil {
		writeError(w, 500, "credential update failed")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "credential commit failed")
		return
	}
	writeJSON(w, 200, map[string]any{"username": name, "enabled": enabled, "action": action, "request_id": request, "version": version, "credential": credential})
}
