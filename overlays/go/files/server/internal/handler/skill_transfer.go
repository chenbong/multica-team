package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Transfer is human-only. Agent/task/daemon credentials cannot surrender an
// owner's skill even if they can read it. Normal PAT/session authentication
// supplies the user identity; membership or workspace-admin status is no override.
func (h *Handler) transferSkillSource(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	if !isHumanIssueActor(r) {
		writeError(w, 403, "Only a human can transfer a skill")
		return pgtype.UUID{}, false
	}
	skill, ok := h.loadSkillForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return pgtype.UUID{}, false
	}
	if !h.canManageSkill(w, r, skill) {
		return pgtype.UUID{}, false
	}
	if skill.Scope != "team" && skill.Scope != "workspace" {
		writeError(w, 400, "Personal skills cannot be transferred")
		return pgtype.UUID{}, false
	}
	return skill.ID, true
}

type skillTransferRecipient struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
}

// A bounded, explicit search across registered human accounts. Email is shown
// only in this creator-only picker to disambiguate identical display names.
func (h *Handler) SkillTransferRecipients(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.transferSkillSource(w, r); !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if utf8.RuneCountInString(q) < 2 || len(q) > 200 {
		writeError(w, 400, "Search must contain 2 to 200 characters")
		return
	}
	rows, err := h.DB.Query(r.Context(), `SELECT id::text,coalesce(nullif(name,''),split_part(email,'@',1)),email FROM "user" WHERE id<>$1 AND (strpos(lower(name),lower($2))>0 OR strpos(lower(email),lower($2))>0) ORDER BY lower(email),id LIMIT 20`, parseUUID(requestUserID(r)), q)
	if err != nil {
		writeError(w, 503, "Unable to search users")
		return
	}
	defer rows.Close()
	users := []skillTransferRecipient{}
	for rows.Next() {
		var u skillTransferRecipient
		if err := rows.Scan(&u.UserID, &u.Name, &u.Email); err != nil {
			writeError(w, 503, "Unable to search users")
			return
		}
		users = append(users, u)
	}
	if rows.Err() != nil {
		writeError(w, 503, "Unable to search users")
		return
	}
	writeJSON(w, 200, users)
}

func (h *Handler) TransferSkill(w http.ResponseWriter, r *http.Request) {
	id, ok := h.transferSkillSource(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	var req struct {
		RecipientUserID string `json:"recipient_user_id"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, 400, "Invalid transfer request")
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeError(w, 400, "Invalid transfer request")
		return
	}
	target, ok := parseUUIDOrBadRequest(w, req.RecipientUserID, "recipient_user_id")
	if !ok {
		return
	}
	actor := parseUUID(requestUserID(r))
	if actor == target {
		writeError(w, 400, "Choose another user")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, 503, "Unable to begin transfer")
		return
	}
	defer tx.Rollback(r.Context())
	if err = h.lockSkillCreator(r.Context(), tx, id, requestUserID(r)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 404, "Skill not found")
		} else {
			writeError(w, 403, "Only the current creator can transfer this skill")
		}
		return
	}
	// Recheck scope after acquiring the same lock as edits, imports and refreshes.
	qtx := h.Queries.WithTx(tx)
	current, err := qtx.GetSkill(r.Context(), id)
	if err != nil {
		writeError(w, 503, "Unable to validate skill")
		return
	}
	if current.Scope != "workspace" && current.Scope != "team" {
		writeError(w, 400, "Personal skills cannot be transferred")
		return
	}
	var existing pgtype.UUID
	err = tx.QueryRow(r.Context(), `SELECT id FROM "user" WHERE id=$1 FOR KEY SHARE`, target).Scan(&existing)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "Recipient account not found")
		return
	}
	if err != nil {
		writeError(w, 503, "Unable to validate recipient")
		return
	}
	// Immutable audit and identity switch commit together. There is deliberately
	// no reclaim endpoint; only the new creator may initiate another transfer.
	_, err = tx.Exec(r.Context(), `INSERT INTO team_mesh_skill_transfer_audit(skill_id,skill_name,from_user_id,to_user_id) VALUES($1,$2,$3,$4)`, id, current.Name, actor, target)
	if err != nil {
		writeError(w, 503, "Unable to record transfer")
		return
	}
	_, err = tx.Exec(r.Context(), `UPDATE skill SET created_by=$2,updated_at=now() WHERE id=$1`, id, target)
	if err != nil {
		writeError(w, 503, "Unable to transfer skill")
		return
	}
	updated, err := qtx.GetSkill(r.Context(), id)
	if err != nil {
		writeError(w, 503, "Unable to read transferred skill")
		return
	}
	files, err := qtx.ListSkillFiles(r.Context(), id)
	if err != nil {
		writeError(w, 503, "Unable to read skill files")
		return
	}
	fileResponses := make([]SkillFileResponse, len(files))
	for i, file := range files {
		fileResponses[i] = skillFileToResponse(file)
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 503, "Unable to commit transfer")
		return
	}
	response := SkillWithFilesResponse{SkillResponse: h.skillResponse(r.Context(), updated), Files: fileResponses}
	h.publish(protocol.EventSkillUpdated, h.resolveWorkspaceID(r), "member", requestUserID(r), map[string]any{"skill": response})
	writeJSON(w, 200, response)
}

func (h *Handler) beginSkillMutation(w http.ResponseWriter, r *http.Request, id pgtype.UUID) (pgx.Tx, bool) {
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, 503, "Unable to begin skill update")
		return nil, false
	}
	if err = h.lockSkillCreator(r.Context(), tx, id, requestUserID(r)); err != nil {
		tx.Rollback(r.Context())
		writeError(w, 403, "Only the current creator can manage this skill")
		return nil, false
	}
	return tx, true
}
