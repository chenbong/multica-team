package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
)

type issueTransferRequest struct {
	TargetWorkspaceID string            `json:"target_workspace_id"`
	TargetStatus      string            `json:"target_status"`
	Fingerprint       string            `json:"fingerprint"`
	ConfirmUnassign   bool              `json:"confirm_unassign"`
	IncludeSubissues  bool              `json:"include_subissues"`
	ConfirmCleanup    bool              `json:"confirm_cleanup"`
	StatusMappings    map[string]string `json:"status_mappings"`
}

type issueTransferStatus struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Category string `json:"category,omitempty"`
}

type issueTransferStatusMapping struct {
	Source string `json:"source"`
	Name   string `json:"name"`
	Target string `json:"target"`
}
type issueTransferPlan struct {
	AffectedIssues       int                          `json:"affected_issues"`
	IssueIDs             []string                     `json:"issue_ids"`
	IncludeSubissues     bool                         `json:"include_subissues"`
	RequiresCleanup      bool                         `json:"requires_cleanup"`
	StatusMappings       []issueTransferStatusMapping `json:"status_mappings"`
	Warnings             []string                     `json:"warnings"`
	IssueID              string                       `json:"issue_id"`
	SourceWorkspaceID    string                       `json:"source_workspace_id"`
	TargetWorkspaceID    string                       `json:"target_workspace_id"`
	SourceIdentifier     string                       `json:"source_identifier"`
	TargetName           string                       `json:"target_name"`
	TargetSlug           string                       `json:"target_slug"`
	TargetPrefix         string                       `json:"target_prefix"`
	TargetStatus         string                       `json:"target_status"`
	Statuses             []issueTransferStatus        `json:"statuses"`
	Revision             int64                        `json:"revision"`
	Comments             int                          `json:"comments"`
	Runs                 int                          `json:"runs"`
	Messages             int                          `json:"messages"`
	Unassign             bool                         `json:"unassign"`
	RetiredSubscriptions int                          `json:"retired_subscriptions"`
	Blockers             []string                     `json:"blockers"`
	Fingerprint          string                       `json:"fingerprint"`
	Identifier           string                       `json:"identifier,omitempty"`
	Path                 string                       `json:"path,omitempty"`
}

type issueTransferFailure struct {
	status        int
	code, message string
}

func (e *issueTransferFailure) Error() string { return e.message }
func transferFailure(status int, code, message string) error {
	return &issueTransferFailure{status, code, message}
}

// Deliberately separate from board /move: cross-workspace transfer has a
// different permission boundary and must never dispatch an agent or plugin.
func (h *Handler) PreviewIssueTransfer(w http.ResponseWriter, r *http.Request) {
	h.issueTransfer(w, r, false)
}
func (h *Handler) TransferIssue(w http.ResponseWriter, r *http.Request) { h.issueTransfer(w, r, true) }

func (h *Handler) issueTransfer(w http.ResponseWriter, r *http.Request, apply bool) {
	if isMachineCredentialActor(r) || r.Header.Get("X-Agent-ID") != "" || r.Header.Get("X-Daemon-ID") != "" {
		writeError(w, 403, "only a human can move issues between workspaces")
		return
	}
	user, ok := requireUserID(w, r)
	if !ok {
		return
	}
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	var req issueTransferRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, 400, "invalid transfer request")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, 400, "invalid transfer request")
		return
	}
	target, ok := parseUUIDOrBadRequest(w, req.TargetWorkspaceID, "target_workspace_id")
	if !ok {
		return
	}
	actor, ok := parseUUIDOrBadRequest(w, user, "user_id")
	if !ok {
		return
	}
	plan, err := h.runIssueTransfer(r.Context(), issue.ID, issue.WorkspaceID, target, actor, req, apply)
	if err != nil {
		var failure *issueTransferFailure
		if errors.As(err, &failure) {
			writeJSON(w, failure.status, map[string]string{"code": failure.code, "error": failure.message})
			return
		}
		writeJSON(w, 409, map[string]string{"code": "transfer_retry", "error": "Transfer could not finish safely. Refresh the preview and retry."})
		return
	}
	if apply {
		// A dedicated event avoids the issue-created/deleted plugin and dispatch
		// hooks. Each audience receives only the ID it was already allowed to see.
		for _, ws := range []string{plan.SourceWorkspaceID, plan.TargetWorkspaceID} {
			h.publish("issue:workspace_changed", ws, "member", user, map[string]string{"issue_id": plan.IssueID})
		}
	}
	writeJSON(w, 200, plan)
}
