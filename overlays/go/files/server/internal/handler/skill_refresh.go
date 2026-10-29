package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/multica-ai/multica/server/pkg/protocol"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// errSkillNotRefreshable marks a skill whose stored provenance cannot be
// re-fetched: manual skills, archive uploads (no origin), runtime-local
// copies, or a hand-edited origin whose source_url no longer matches its type.
var errSkillNotRefreshable = errors.New("this skill was not imported from a refreshable source (GitHub, skills.sh, ClawHub, or OneTool)")

// skillOriginRef is the minimal provenance needed to re-run an import.
type skillOriginRef struct {
	Type      string
	SourceURL string
}

// parseSkillOrigin extracts config.origin.{type,source_url} from a skill's
// JSONB config blob. ok is false when config has no decodable origin object.
func parseSkillOrigin(config []byte) (skillOriginRef, bool) {
	if len(config) == 0 {
		return skillOriginRef{}, false
	}
	var decoded struct {
		Origin struct {
			Type      string `json:"type"`
			SourceURL string `json:"source_url"`
		} `json:"origin"`
	}
	if err := json.Unmarshal(config, &decoded); err != nil {
		return skillOriginRef{}, false
	}
	if decoded.Origin.Type == "" {
		return skillOriginRef{}, false
	}
	return skillOriginRef{
		Type:      decoded.Origin.Type,
		SourceURL: strings.TrimSpace(decoded.Origin.SourceURL),
	}, true
}

// refreshableOriginSource maps a stored origin type onto the importSource its
// source_url must resolve to. ok is false for origins that cannot be
// re-fetched (manual, runtime_local, archive uploads).
func refreshableOriginSource(originType string) (importSource, bool) {
	switch originType {
	case "github":
		return sourceGitHub, true
	case "skills_sh":
		return sourceSkillsSh, true
	case "clawhub":
		return sourceClawHub, true
	case "onetool":
		return sourceOneTool, true
	default:
		return 0, false
	}
}

// fetchImportedSkillFromOrigin re-runs the import fetch for a skill's stored
// provenance. Eligibility is validated before any network call: a
// non-refreshable origin type, an empty source_url, or a source_url that does
// not resolve to the origin's own source (hand-edited config) all return
// errSkillNotRefreshable without contacting the upstream.
func fetchImportedSkillFromOrigin(ctx context.Context, httpClient *http.Client, origin skillOriginRef) (*importedSkill, error) {
	expected, ok := refreshableOriginSource(origin.Type)
	if !ok || origin.SourceURL == "" {
		return nil, errSkillNotRefreshable
	}
	source, normalized, err := detectImportSource(origin.SourceURL)
	if err != nil || source != expected {
		return nil, errSkillNotRefreshable
	}
	switch source {
	case sourceClawHub:
		return fetchFromClawHub(ctx, httpClient, normalized)
	case sourceSkillsSh:
		return fetchFromSkillsSh(ctx, httpClient, normalized)
	case sourceGitHub:
		return fetchFromGitHub(ctx, httpClient, normalized)
	case sourceOneTool:
		return fetchFromOneTool(ctx, httpClient, normalized)
	}
	return nil, errSkillNotRefreshable
}

// mergeSkillConfigOrigin rewrites only the origin key of a skill's config,
// preserving any other keys a user may have set via the API or CLI. This is
// deliberately narrower than the import-overwrite path, which replaces config
// wholesale: refresh touches provenance, not user configuration.
func mergeSkillConfigOrigin(existingConfig []byte, origin map[string]any) map[string]any {
	config := map[string]any{}
	if len(existingConfig) > 0 {
		var decoded map[string]any
		if err := json.Unmarshal(existingConfig, &decoded); err == nil && decoded != nil {
			config = decoded
		}
	}
	if origin != nil {
		config["origin"] = origin
	}
	return config
}

// RefreshSkill re-downloads a skill from its stored config.origin source and
// overwrites the skill in place. Identity (id, created_by, created_at) and
// agent_skill bindings are preserved; description, content, files, and
// config.origin are replaced with the fresh upstream state. Unlike the
// creator-only import-overwrite path, refresh follows the canManageSkill
// policy (workspace owner/admin or creator): the source URL is pinned on the
// skill itself, so a refreshing admin cannot inject arbitrary content.
func (h *Handler) RefreshSkill(w http.ResponseWriter, r *http.Request) {
	skill, ok := h.loadSkillForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	if !h.canManageSkill(w, r, skill) {
		return
	}
	var body struct {
		URL *string `json:"url,omitempty"`
	}
	if r.Body != nil {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		decoder.DisallowUnknownFields()
		decodeErr := decoder.Decode(&body)
		if decodeErr != nil && decodeErr != io.EOF {
			writeError(w, 400, "invalid update request")
			return
		}
		if decodeErr == nil {
			var extra any
			if decoder.Decode(&extra) != io.EOF {
				writeError(w, 400, "invalid update request")
				return
			}
		}
	}
	origin, hasOrigin := parseSkillOrigin(skill.Config)
	if body.URL != nil {
		source, normalized, err := detectImportSource(*body.URL)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		sourceType := ""
		switch source {
		case sourceGitHub:
			sourceType = "github"
		case sourceSkillsSh:
			sourceType = "skills_sh"
		case sourceClawHub:
			sourceType = "clawhub"
		case sourceOneTool:
			sourceType = "onetool"
		}
		origin = skillOriginRef{Type: sourceType, SourceURL: normalized}
		hasOrigin = true
	}
	if !hasOrigin {
		writeError(w, 422, errSkillNotRefreshable.Error())
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	httpClient := &http.Client{Timeout: 30 * time.Second}
	ctx, cancel := context.WithTimeout(r.Context(), importFetchTimeout)
	defer cancel()

	imported, err := fetchImportedSkillFromOrigin(ctx, httpClient, origin)
	if err != nil {
		if errors.Is(err, errSkillNotRefreshable) {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		status, msg := importFetchErrorResponse(ctx, err)
		writeError(w, status, msg)
		return
	}

	resp, err := h.overwriteSkillWithFiles(r.Context(), skillOverwriteInput{
		WorkspaceID:   skill.WorkspaceID,
		TargetSkillID: skill.ID,
		UserID:        userID,
		ExpectedScope: skill.Scope,
		AllowOverwrite: func(uid string, s db.Skill) bool {
			return canOverwriteSkillByLocalImport(uid, s)
		},
		Description: imported.description,
		Content:     imported.content,
		Config:      mergeSkillConfigOrigin(skill.Config, imported.origin),
		Files:       importedSkillFileRequests(imported),
	})
	if err != nil {
		switch {
		case errors.Is(err, errSkillOverwriteNotFound):
			writeError(w, http.StatusNotFound, "skill not found")
		case errors.Is(err, errSkillOverwriteForbidden):
			writeError(w, http.StatusForbidden, "only the skill creator or a workspace admin can update this skill from its source")
		case errors.Is(err, errSkillOverwriteNameConflict):
			writeError(w, http.StatusConflict, "skill name conflict")
		default:
			writeError(w, http.StatusInternalServerError, "failed to update skill from source: "+err.Error())
		}
		return
	}

	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	h.publish(protocol.EventSkillUpdated, workspaceID, actorType, actorID, map[string]any{"skill": resp})
	writeJSON(w, http.StatusOK, resp)
}
