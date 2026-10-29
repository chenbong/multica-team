package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type SkillCreatorResponse struct {
	UserID    string  `json:"user_id"`
	Name      string  `json:"name"`
	AvatarURL *string `json:"avatar_url"`
}

func normalizedSkillName(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

func (h *Handler) globalSkillByName(ctx context.Context, name string) (db.Skill, error) {
	var id pgtype.UUID
	if err := h.DB.QueryRow(ctx, `SELECT id FROM skill WHERE team_mesh_skill_name_key(name)=team_mesh_skill_name_key($1)`, name).Scan(&id); err != nil {
		return db.Skill{}, err
	}
	return h.Queries.GetSkill(ctx, id)
}

func (h *Handler) skillCreators(ctx context.Context, ids []string) map[string]*SkillCreatorResponse {
	result := map[string]*SkillCreatorResponse{}
	if len(ids) == 0 {
		return result
	}
	rows, err := h.DB.Query(ctx, `SELECT id::text,coalesce(nullif(name,''),split_part(email,'@',1)),avatar_url FROM "user" WHERE id=ANY($1::uuid[])`, ids)
	if err != nil {
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var c SkillCreatorResponse
		if rows.Scan(&c.UserID, &c.Name, &c.AvatarURL) == nil {
			result[c.UserID] = &c
		}
	}
	return result
}

func (h *Handler) skillCreator(ctx context.Context, id pgtype.UUID) *SkillCreatorResponse {
	if !id.Valid {
		return nil
	}
	key := uuidToString(id)
	return h.skillCreators(ctx, []string{key})[key]
}

func (h *Handler) skillResponse(ctx context.Context, skill db.Skill) SkillResponse {
	result := skillToResponse(skill)
	result.Creator = h.skillCreator(ctx, skill.CreatedBy)
	return result
}

func (h *Handler) skillConflictIdentity(ctx context.Context, skill db.Skill, userID string) ExistingSkillIdentity {
	result := existingSkillIdentity(skill, userID)
	result.Scope = skill.Scope
	result.WorkspaceID = uuidToString(skill.WorkspaceID)
	result.Creator = h.skillCreator(ctx, skill.CreatedBy)
	if skill.WorkspaceID.Valid {
		if ws, err := h.Queries.GetWorkspace(ctx, skill.WorkspaceID); err == nil {
			result.WorkspaceName = ws.Name
		}
	}
	if skill.Scope == "personal" && uuidToString(skill.OwnerUserID) != userID {
		result.ID = ""
		result.CreatedBy = ""
		result.CanOverwrite = false
	}
	return result
}

func skillConflictMessage(info ExistingSkillIdentity) string {
	if info.Scope == "" && info.Creator == nil {
		return "a skill with this name already exists"
	}
	owner := "unknown creator"
	if info.Creator != nil {
		owner = info.Creator.Name
	}
	location := info.Scope
	if info.WorkspaceName != "" {
		location += " / " + info.WorkspaceName
	}
	return fmt.Sprintf("Skill %q already exists (%s; creator: %s)", info.Name, location, owner)
}

func (h *Handler) writeGlobalSkillNameConflict(w http.ResponseWriter, r *http.Request, name string) {
	skill, err := h.globalSkillByName(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusConflict, "a skill with this name already exists")
		return
	}
	info := h.skillConflictIdentity(r.Context(), skill, requestUserID(r))
	writeJSON(w, http.StatusConflict, map[string]any{"error": skillConflictMessage(info), "code": "skill_name_conflict", "existing_skill": info})
}

func sameSkillDestination(skill db.Skill, scope string, workspace, owner pgtype.UUID) bool {
	if skill.Scope != scope {
		return false
	}
	if scope == "workspace" {
		return skill.WorkspaceID == workspace
	}
	if scope == "personal" {
		return skill.OwnerUserID == owner
	}
	return true
}

func (h *Handler) lockSkillCreator(ctx context.Context, tx pgx.Tx, id pgtype.UUID, user string) error {
	var creator pgtype.UUID
	if err := tx.QueryRow(ctx, `SELECT created_by FROM skill WHERE id=$1 FOR UPDATE`, id).Scan(&creator); err != nil {
		return err
	}
	if !creator.Valid || uuidToString(creator) != user {
		return errors.New("only the skill creator can manage this skill")
	}
	return nil
}

func (h *Handler) canManagePluginSkills(w http.ResponseWriter, r *http.Request, id pgtype.UUID) bool {
	skills, err := h.Queries.ListPluginSkills(r.Context(), id)
	if err != nil {
		writeError(w, 503, "unable to validate skill creators")
		return false
	}
	for _, skill := range skills {
		if !canOverwriteSkillByLocalImport(requestUserID(r), skill) {
			writeError(w, 403, "only the skill creator can delete these plugin skills")
			return false
		}
	}
	return true
}
