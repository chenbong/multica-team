package handler

import (
	"context"
	"net/http"
	"os"
	"path"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type RuntimeModelCatalog interface {
	Models(context.Context, string, bool) (agent.OneAPIModelResult, error)
}

func configuredOneAPIModelCatalog() RuntimeModelCatalog {
	endpoint := os.Getenv("MULTICA_ONEAPI_CATALOG_URL")
	if endpoint == "" {
		return nil
	}
	return agent.NewOneAPIModelCatalog(endpoint, os.Getenv("MULTICA_ONEAPI_CATALOG_TOKEN_FILE"), os.Getenv("MULTICA_ONEAPI_CATALOG_PROTOCOLS_FILE"), os.Getenv("MULTICA_ONEAPI_CATALOG_CA_FILE"))
}

func oneAPIRuntimeProtocol(provider, command string) string {
	switch {
	case provider == "claude" && path.Base(command) == "ducc":
		return "messages"
	case provider == "codex" && path.Base(command) == "ducx":
		return "responses"
	default:
		return ""
	}
}

// Called only after the normal runtime/workspace access and online checks.
// Only the actual profile command selects this catalog, never a user-editable
// display name. Built-in Claude/Codex and all other custom CLIs stay unchanged.
func (h *Handler) serveOneAPIModelCatalog(w http.ResponseWriter, r *http.Request, rt db.AgentRuntime) bool {
	if h.OneAPIModelCatalog == nil || !rt.ProfileID.Valid {
		return false
	}
	profile, exists, err := h.runtimeLiveProfile(r.Context(), rt)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "runtime profile unavailable")
		return true
	}
	if !exists || profile.ProtocolFamily != rt.Provider {
		return false
	}
	protocol := oneAPIRuntimeProtocol(rt.Provider, profile.CommandName)
	if protocol == "" {
		return false
	}
	now := time.Now()
	reply := &ModelListRequest{ID: randomID(), RuntimeID: uuidToString(rt.ID), Status: ModelListCompleted, Supported: true, CreatedAt: now, UpdatedAt: now}
	result, err := h.OneAPIModelCatalog.Models(r.Context(), protocol, r.URL.Query().Get("force") == "true")
	if err != nil {
		reply.Status, reply.Error = ModelListFailed, err.Error()
		writeJSON(w, http.StatusOK, reply)
		return true
	}
	reply.Models = make([]ModelEntry, 0, len(result.Models))
	for _, model := range result.Models {
		reply.Models = append(reply.Models, ModelEntry{ID: model.ID, Label: model.Label, Provider: model.Provider})
	}
	reply.Cached, reply.CachedAt = result.Cached, &result.StoredAt
	// Keep gateway discovery separate from the daemon's capability cache:
	// a list of model names cannot attest CLI-specific effort or service tiers.
	writeJSON(w, http.StatusOK, reply)
	return true
}
