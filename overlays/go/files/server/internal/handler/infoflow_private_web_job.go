package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

type infoFlowPrivateJob struct {
	ID, IssueID, RequesterID, AgentID, Generation, LeaseToken, LeaseAgent pgtype.UUID
	Workspace, Sender, State, LeaseRobot                                  string
	AssignmentVersion                                                     int64
	Snapshot                                                              []byte
	Expires                                                               time.Time
	LeaseUntil                                                            pgtype.Timestamptz
}

const infoFlowPrivateJobColumns = `id,issue_id,requester_id,agent_id,generation,lease_token,lease_agent_id,workspace_id::text,sender,state,COALESCE(lease_robot_id,''),assignment_version,binding_snapshot,expires_at,lease_until`

func infoFlowScanPrivateJob(row pgx.Row) (infoFlowPrivateJob, error) {
	var j infoFlowPrivateJob
	err := row.Scan(&j.ID, &j.IssueID, &j.RequesterID, &j.AgentID, &j.Generation, &j.LeaseToken, &j.LeaseAgent, &j.Workspace, &j.Sender, &j.State, &j.LeaseRobot, &j.AssignmentVersion, &j.Snapshot, &j.Expires, &j.LeaseUntil)
	return j, err
}

func (h *Handler) infoFlowPrivateJobAuthority(ctx context.Context, tx pgx.Tx, j infoFlowPrivateJob) (infoFlowGroupTopology, []infoFlowOwnBinding, string, error) {
	sh, _ := h.infoFlowScoped(tx)
	t, reason, err := sh.infoFlowTopology(ctx, j.IssueID, j.Workspace)
	if err != nil {
		return t, nil, "", err
	}
	if reason != "" || t.AssigneeType != "agent" || t.LeaderAgentID != uuidToString(j.AgentID) || t.AssignmentVersion != j.AssignmentVersion {
		return t, nil, "permission_changed", nil
	}
	sender, err := sh.infoFlowWebSender(ctx, j.Workspace, j.RequesterID)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, nil, "permission_changed", nil
	}
	if err != nil {
		return t, nil, "", err
	}
	if sender == "" || sender != j.Sender {
		return t, nil, "permission_changed", nil
	}
	rows, err := infoFlowOwnPrivateBindings(ctx, tx, j.Workspace, j.IssueID, j.RequesterID, sender)
	if err != nil {
		return t, nil, "", err
	}
	manager := infoFlowBinding{}
	if len(rows) > 0 {
		manager = rows[0].Binding
	}
	_, _, manage, err := sh.infoFlowWebAuthority(ctx, j.Workspace, j.IssueID, j.RequesterID, manager)
	if err != nil {
		return t, nil, "", err
	}
	if !manage {
		return t, rows, "permission_changed", nil
	}
	var saved []map[string]string
	if json.Unmarshal(j.Snapshot, &saved) != nil {
		return t, rows, "binding_changed", nil
	}
	canonical, _ := json.Marshal(saved)
	if !bytes.Equal(infoFlowPrivateSnapshot(rows), canonical) {
		return t, rows, "binding_changed", nil
	}
	return t, rows, "", nil
}

func (h *Handler) InfoFlowWebPrivateJobs(w http.ResponseWriter, r *http.Request) {
	if !infoFlowAuthenticate(w, r) {
		return
	}
	var scope infoFlowScope
	if !infoFlowDecode(w, r, &scope) {
		return
	}
	if !scope.valid() || !h.infoFlowWebScope(r.Context(), scope) {
		infoFlowError(w, 400, "scope")
		return
	}
	out := []map[string]any{}
	j, err := infoFlowScanPrivateJob(h.DB.QueryRow(r.Context(), `SELECT `+infoFlowPrivateJobColumns+` FROM infoflow_web_private_request WHERE workspace_id=$1 AND state IN('queued','pending') AND next_at<=clock_timestamp() AND (lease_until IS NULL OR lease_until<clock_timestamp()) ORDER BY next_at,id LIMIT 1`, scope.WorkspaceID))
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, 200, map[string]any{"jobs": out})
		return
	}
	if err != nil {
		infoFlowError(w, 500, "job")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		infoFlowError(w, 500, "begin")
		return
	}
	defer tx.Rollback(r.Context())
	if err = infoFlowPrivateWebLock(r.Context(), tx, j.IssueID, j.RequesterID); err != nil {
		infoFlowError(w, 500, "lock")
		return
	}
	t, _, reason, err := h.infoFlowPrivateJobAuthority(r.Context(), tx, j)
	if err != nil {
		infoFlowError(w, 500, "authority")
		return
	}
	oldGeneration, oldWorkspace := j.Generation, j.Workspace
	j, err = infoFlowScanPrivateJob(tx.QueryRow(r.Context(), `SELECT `+infoFlowPrivateJobColumns+` FROM infoflow_web_private_request WHERE id=$1 FOR UPDATE`, j.ID))
	if err != nil || j.Generation != oldGeneration || j.Workspace != oldWorkspace || j.Workspace != strings.ToLower(scope.WorkspaceID) {
		infoFlowError(w, 409, "job changed")
		return
	}
	if j.State != "queued" && j.State != "pending" || j.LeaseUntil.Valid && j.LeaseUntil.Time.After(time.Now()) {
		writeJSON(w, 200, map[string]any{"jobs": out})
		return
	}
	if !j.Expires.After(time.Now()) {
		reason = "request_expired"
	}
	if reason != "" {
		_, err = tx.Exec(r.Context(), `UPDATE infoflow_web_private_request SET state='failed',reason=$2,lease_token=NULL,lease_until=NULL,updated_at=clock_timestamp() WHERE id=$1`, j.ID, reason)
	} else {
		lease := dbid.NewV7()
		_, err = tx.Exec(r.Context(), `UPDATE infoflow_web_private_request SET state='pending',lease_token=$2,lease_robot_id=$3,lease_agent_id=$4,lease_until=clock_timestamp()+interval '90 seconds',updated_at=clock_timestamp() WHERE id=$1`, j.ID, lease, scope.RobotID, scope.AgentID)
		out = append(out, map[string]any{"request_id": uuidToString(j.ID), "generation": uuidToString(j.Generation), "lease_token": uuidToString(lease), "sender": j.Sender, "agent_id": uuidToString(j.AgentID), "agent_name": t.AgentNames[uuidToString(j.AgentID)], "assignment_version": infoFlowAssignmentText(j.AssignmentVersion), "issue_id": uuidToString(j.IssueID)})
	}
	if err != nil {
		infoFlowError(w, 500, "lease")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		infoFlowError(w, 500, "commit")
		return
	}
	writeJSON(w, 200, map[string]any{"jobs": out})
}

type infoFlowPrivateResult struct {
	infoFlowScope
	RequestID     string `json:"request_id"`
	Generation    string `json:"generation"`
	LeaseToken    string `json:"lease_token"`
	TargetAgentID string `json:"target_agent_id"`
	TargetRobotID string `json:"target_robot_id"`
	RobotChatID   string `json:"robot_chat_id"`
	RobotName     string `json:"robot_name"`
	Reason        string `json:"reason"`
}

func (h *Handler) InfoFlowWebPrivateResult(w http.ResponseWriter, r *http.Request) {
	if !infoFlowAuthenticate(w, r) {
		return
	}
	var req infoFlowPrivateResult
	if !infoFlowDecode(w, r, &req) {
		return
	}
	if !req.infoFlowScope.valid() || !h.infoFlowWebScope(r.Context(), req.infoFlowScope) || len(req.TargetRobotID) > 256 || strings.ContainsRune(req.TargetRobotID, 0) || len(req.RobotName) > 256 || strings.ContainsAny(req.RobotName, "\x00\r\n") || infoFlowPrivateWebReason(req.Reason) == "" || req.Reason == "active" || req.Reason == "binding_changed" || req.Reason == "permission_changed" || req.Reason == "request_expired" || req.Reason == infoFlowRequestCancelled {
		infoFlowError(w, 400, "result")
		return
	}
	id, ok := parseUUIDOrBadRequest(w, req.RequestID, "request_id")
	if !ok {
		return
	}
	gen, ok := parseUUIDOrBadRequest(w, req.Generation, "generation")
	if !ok {
		return
	}
	lease, ok := parseUUIDOrBadRequest(w, req.LeaseToken, "lease_token")
	if !ok {
		return
	}
	targetAgent, ok := parseUUIDOrBadRequest(w, req.TargetAgentID, "target_agent_id")
	if !ok {
		return
	}
	if req.Reason == "" && (req.TargetRobotID == "" || !infoFlowValidPrivateRobotMetadata(req.RobotChatID, req.RobotName)) {
		infoFlowError(w, 400, "metadata")
		return
	}
	j, err := infoFlowScanPrivateJob(h.DB.QueryRow(r.Context(), `SELECT `+infoFlowPrivateJobColumns+` FROM infoflow_web_private_request WHERE id=$1 AND workspace_id=$2`, id, req.WorkspaceID))
	if err != nil {
		infoFlowError(w, 409, "job")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		infoFlowError(w, 500, "begin")
		return
	}
	defer tx.Rollback(r.Context())
	targetScope := infoFlowScope{RobotID: req.TargetRobotID, AgentID: req.TargetAgentID, WorkspaceID: j.Workspace}
	if req.TargetRobotID != "" {
		if err = infoFlowLock(r.Context(), tx, targetScope, infoFlowConversation{Kind: "private", ID: j.Sender}); err != nil {
			infoFlowError(w, 500, "lock")
			return
		}
	}
	if err = infoFlowPrivateWebLock(r.Context(), tx, j.IssueID, j.RequesterID); err != nil {
		infoFlowError(w, 500, "lock")
		return
	}
	t, rows, reason, err := h.infoFlowPrivateJobAuthority(r.Context(), tx, j)
	if err != nil {
		infoFlowError(w, 500, "authority")
		return
	}
	j, err = infoFlowScanPrivateJob(tx.QueryRow(r.Context(), `SELECT `+infoFlowPrivateJobColumns+` FROM infoflow_web_private_request WHERE id=$1 FOR UPDATE`, id))
	if err != nil || !infoFlowWebLeaseCurrent(j.State, j.Generation, j.LeaseToken, j.LeaseUntil, j.Expires, gen, lease) || j.LeaseRobot != req.RobotID || uuidToString(j.LeaseAgent) != strings.ToLower(req.AgentID) || j.AgentID != targetAgent {
		infoFlowError(w, 409, "stale result")
		return
	}
	state := "pending"
	if reason != "" {
		state = "failed"
	} else if req.Reason != "" {
		reason = req.Reason
	} else {
		var current infoFlowBinding
		for _, row := range rows {
			if row.Robot == req.TargetRobotID && row.Agent == req.TargetAgentID {
				if current.ID.Valid {
					infoFlowError(w, 409, "ambiguous private binding")
					return
				}
				current = row.Binding
			}
		}
		if current.ID.Valid {
			generation := current.Generation
			if current.AssignmentVersion != t.AssignmentVersion {
				generation = dbid.NewV7()
			}
			_, err = tx.Exec(r.Context(), `UPDATE infoflow_issue_binding SET assignment_version=$2,generation=$3,bound_at=CASE WHEN generation<>$3 THEN clock_timestamp() ELSE bound_at END,robot_chat_id=$4,robot_name=$5 WHERE id=$1`, current.ID, t.AssignmentVersion, generation, req.RobotChatID, req.RobotName)
		} else {
			_, err = tx.Exec(r.Context(), `INSERT INTO infoflow_issue_binding(id,robot_id,workspace_id,agent_id,conversation_kind,conversation_id,issue_id,binder_id,generation,assignment_version,robot_chat_id,robot_name) VALUES($1,$2,$3,$4,'private',$5,$6,$7,$8,$9,$10,$11)`, dbid.NewV7(), req.TargetRobotID, j.Workspace, j.AgentID, j.Sender, j.IssueID, j.RequesterID, dbid.NewV7(), t.AssignmentVersion, req.RobotChatID, req.RobotName)
		}
		if err != nil {
			infoFlowError(w, 409, "binding")
			return
		}
		state, reason = "active", "active"
		var bindingID string
		err = tx.QueryRow(r.Context(), `SELECT id::text FROM infoflow_issue_binding WHERE workspace_id=$1 AND issue_id=$2 AND conversation_kind='private' AND robot_id=$3 AND agent_id=$4 AND conversation_id=$5`, j.Workspace, j.IssueID, req.TargetRobotID, j.AgentID, j.Sender).Scan(&bindingID)
		if err != nil {
			infoFlowError(w, 500, "binding notice")
			return
		}
		current, e := infoFlowLoadBindingID(r.Context(), tx, targetScope, infoFlowConversation{Kind: "private", ID: j.Sender}, bindingID)
		if e != nil {
			infoFlowError(w, 500, "binding notice")
			return
		}
		if e = infoFlowEnqueueNotice(r.Context(), tx, targetScope, infoFlowConversation{Kind: "private", ID: j.Sender}, current, "bound"); e != nil {
			infoFlowError(w, 500, "binding notice")
			return
		}
	}
	_, err = tx.Exec(r.Context(), `UPDATE infoflow_web_private_request SET state=$2,reason=$3,lease_token=NULL,lease_robot_id=NULL,lease_agent_id=NULL,lease_until=NULL,next_at=clock_timestamp()+interval '5 seconds',updated_at=clock_timestamp() WHERE id=$1`, id, state, reason)
	if err != nil {
		infoFlowError(w, 500, "result")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		infoFlowError(w, 500, "commit")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "state": state})
}
