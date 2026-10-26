package handler

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/service"
)

func (h *Handler) PublicLiveStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]bool{"enabled": h.PublicLive != nil, "collection_enabled": h.PublicLive != nil && h.PublicLive.CanRecord()})
}
func (h *Handler) PublicLiveStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if h.PublicLive == nil {
		http.NotFound(w, r)
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, 400, "Query parameters are not supported")
		return
	}
	value, ok := h.PublicLive.Get(chi.URLParam(r, "section"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, 200, value)
}
func (h *Handler) RecordLiveActivity(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if isMachineCredentialActor(r) {
		writeError(w, 403, "Human activity requires a human account")
		return
	}
	user, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if _, ok = parseUUIDOrBadRequest(w, user, "user id"); !ok {
		return
	}
	if h.PublicLive == nil {
		http.NotFound(w, r)
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		writeError(w, 415, "Expected application/json")
		return
	}
	var body struct {
		EventID  string `json:"event_id"`
		PageView bool   `json:"page_view"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	d.DisallowUnknownFields()
	if r.URL.RawQuery != "" || d.Decode(&body) != nil || d.Decode(&struct{}{}) != io.EOF {
		writeError(w, 400, "Invalid activity event")
		return
	}
	if _, ok = parseUUIDOrBadRequest(w, body.EventID, "event id"); !ok {
		return
	}
	if err = h.PublicLive.Record(r.Context(), user, body.EventID, body.PageView); err != nil {
		if errors.Is(err, service.ErrLiveRateLimited) {
			w.Header().Set("Retry-After", "60")
			writeError(w, 429, "Activity reporting rate limited")
		} else {
			writeError(w, 503, "Activity collection unavailable")
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Ordinary InfoFlow chats use the robot owner's CLI identity. Never count that
// owner as the sender. The trusted bridge reports a verified incoming account;
// resolve it with the same membership/ambiguity policy as task-mode inputs.
func (h *Handler) InfoFlowLiveActivity(w http.ResponseWriter, r *http.Request) {
	if !infoFlowAuthenticate(w, r) {
		return
	}
	if h.PublicLive == nil {
		w.WriteHeader(204)
		return
	}
	var body struct {
		Sender    string `json:"sender"`
		Workspace string `json:"workspace_id"`
		MessageID string `json:"message_id"`
		RobotID   string `json:"robot_id"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	if !infoFlowDecode(w, r, &body) {
		return
	}
	if len(body.MessageID) == 0 || len(body.MessageID) > 256 || len(body.RobotID) == 0 || len(body.RobotID) > 128 || !machineUsername.MatchString(strings.ToLower(body.Sender)) {
		infoFlowError(w, 400, "invalid activity")
		return
	}
	if _, ok := parseUUIDOrBadRequest(w, body.Workspace, "workspace id"); !ok {
		return
	}
	user, _, err := h.infoFlowIdentity(r.Context(), strings.ToLower(body.Sender), body.Workspace)
	if err != nil {
		infoFlowError(w, 403, "unverified sender")
		return
	}
	event := uuid.NewSHA1(uuid.NameSpaceURL, []byte("team-mesh-live:"+body.RobotID+":"+body.Workspace+":"+uuidToString(user.ID)+":"+body.MessageID)).String()
	if err = h.PublicLive.Record(r.Context(), uuidToString(user.ID), event, false); err != nil {
		infoFlowError(w, 503, "activity unavailable")
		return
	}
	w.WriteHeader(204)
}
