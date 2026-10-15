package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

type infoFlowWebJob struct {
	ID, IssueID, RequesterID, Generation, OriginalID, OriginalGeneration, LeaseToken, LeaseAgent pgtype.UUID
	WorkspaceID, GroupID, State, LeaseRobot                                                      string
	Topology                                                                                     []byte
	Expires                                                                                      time.Time
	LeaseUntil                                                                                   pgtype.Timestamptz
}

const infoFlowWebJobColumns = `id,issue_id,requester_id,generation,original_binding_id,original_generation,lease_token,lease_agent_id,workspace_id::text,group_id,state,COALESCE(lease_robot_id,''),topology,expires_at,lease_until`

func infoFlowScanWebJob(row pgx.Row) (infoFlowWebJob, error) {
	var j infoFlowWebJob
	err := row.Scan(&j.ID, &j.IssueID, &j.RequesterID, &j.Generation, &j.OriginalID, &j.OriginalGeneration, &j.LeaseToken, &j.LeaseAgent, &j.WorkspaceID, &j.GroupID, &j.State, &j.LeaseRobot, &j.Topology, &j.Expires, &j.LeaseUntil)
	return j, err
}

func infoFlowWebOriginal(j infoFlowWebJob, b infoFlowBinding) bool {
	return j.OriginalID == b.ID && j.OriginalGeneration == b.Generation
}

func infoFlowWebFail(ctx context.Context, tx pgx.Tx, id pgtype.UUID, reason string) error {
	_, err := tx.Exec(ctx, `UPDATE infoflow_web_group_request SET state='failed',reason=$2,lease_token=NULL,lease_robot_id=NULL,lease_agent_id=NULL,lease_until=NULL,updated_at=clock_timestamp() WHERE id=$1`, id, reason)
	return err
}

func (h *Handler) InfoFlowWebGroupJobs(w http.ResponseWriter, r *http.Request) {
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
	// Candidate selection acquires no row lock. All mutation paths then use the
	// same group/issue/entity/job lock order to avoid lease-vs-submit deadlocks.
	j, err := infoFlowScanWebJob(h.DB.QueryRow(r.Context(), `SELECT `+infoFlowWebJobColumns+` FROM infoflow_web_group_request WHERE workspace_id=$1 AND state IN ('queued','pending') AND next_at<=clock_timestamp() AND (lease_until IS NULL OR lease_until<clock_timestamp()) ORDER BY next_at,id LIMIT 1`, scope.WorkspaceID))
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, 200, map[string]any{"jobs": out})
		return
	}
	if err != nil {
		infoFlowError(w, 500, "jobs")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		infoFlowError(w, 500, "begin")
		return
	}
	defer tx.Rollback(r.Context())
	candidateGroup, candidateWorkspace := j.GroupID, j.WorkspaceID
	if err = infoFlowWebLock(r.Context(), tx, j.WorkspaceID, j.GroupID, j.IssueID); err != nil {
		infoFlowError(w, 500, "lock")
		return
	}
	j, err = infoFlowScanWebJob(tx.QueryRow(r.Context(), `SELECT `+infoFlowWebJobColumns+` FROM infoflow_web_group_request WHERE id=$1 FOR UPDATE`, j.ID))
	if err != nil {
		infoFlowError(w, 409, "request changed")
		return
	}
	if j.GroupID != candidateGroup || j.WorkspaceID != candidateWorkspace || j.WorkspaceID != strings.ToLower(scope.WorkspaceID) {
		writeJSON(w, 200, map[string]any{"jobs": out})
		return
	}
	if j.State != "queued" && j.State != "pending" || j.LeaseUntil.Valid && j.LeaseUntil.Time.After(time.Now()) {
		writeJSON(w, 200, map[string]any{"jobs": out})
		return
	}
	b, group, err := infoFlowWebBinding(r.Context(), tx, j.WorkspaceID, j.IssueID)
	if err != nil {
		infoFlowError(w, 500, "binding")
		return
	}
	sh, _ := h.infoFlowScoped(tx)
	t, sender, manage, err := sh.infoFlowWebAuthority(r.Context(), j.WorkspaceID, j.IssueID, j.RequesterID, b)
	if err != nil {
		infoFlowError(w, 500, "authority")
		return
	}
	reason := ""
	if time.Now().After(j.Expires) {
		reason = "request_expired"
	} else if !manage || !infoFlowSameTopology(j.Topology, t) {
		reason = "permission_changed"
	} else if !infoFlowWebOriginal(j, b) || group != "" && group != j.GroupID {
		reason = "binding_changed"
	}
	if reason != "" {
		if err = infoFlowWebFail(r.Context(), tx, j.ID, reason); err != nil {
			infoFlowError(w, 500, "job")
			return
		}
	} else {
		lease := dbid.NewV7()
		_, err = tx.Exec(r.Context(), `UPDATE infoflow_web_group_request SET state='pending',lease_token=$2,lease_robot_id=$3,lease_agent_id=$4,lease_until=clock_timestamp()+interval '90 seconds',updated_at=clock_timestamp() WHERE id=$1`, j.ID, lease, scope.RobotID, scope.AgentID)
		if err != nil {
			infoFlowError(w, 500, "lease")
			return
		}
		rows, e := tx.Query(r.Context(), `SELECT robot_id FROM infoflow_group_observation WHERE workspace_id=$1 AND group_id=$2 AND observed_at>clock_timestamp()-interval '24 hours' ORDER BY robot_id`, j.WorkspaceID, j.GroupID)
		if e != nil {
			infoFlowError(w, 500, "inviter")
			return
		}
		inviter := []string{}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				break
			}
			inviter = append(inviter, id)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			infoFlowError(w, 500, "inviter")
			return
		}
		view := sh.infoFlowBindingView(r.Context(), infoFlowBinding{ID: j.ID, Generation: j.Generation, IssueID: j.IssueID}, j.WorkspaceID, false)
		if view == nil {
			infoFlowError(w, 409, "issue")
			return
		}
		view.Group = &infoFlowGroupView{State: "pending", LeaderAgentID: t.LeaderAgentID, AgentIDs: t.AgentIDs, AgentNames: t.AgentNames}
		out = append(out, map[string]any{"request_id": uuidToString(j.ID), "generation": uuidToString(j.Generation), "lease_token": uuidToString(lease), "group_id": j.GroupID, "sender": sender, "inviter_robot_ids": inviter, "binding": view})
	}
	if err = tx.Commit(r.Context()); err != nil {
		infoFlowError(w, 500, "commit")
		return
	}
	writeJSON(w, 200, map[string]any{"jobs": out})
}

type infoFlowWebResult struct {
	infoFlowScope
	RequestID     string `json:"request_id"`
	Generation    string `json:"generation"`
	LeaseToken    string `json:"lease_token"`
	GroupID       string `json:"group_id"`
	ActorPresent  bool   `json:"actor_present"`
	LeaderPresent bool   `json:"leader_present"`
	LeaderAgentID string `json:"leader_agent_id"`
	LeaderRobotID string `json:"leader_robot_id"`
	Reason        string `json:"reason"`
}

func (h *Handler) InfoFlowWebGroupResult(w http.ResponseWriter, r *http.Request) {
	if !infoFlowAuthenticate(w, r) {
		return
	}
	var req infoFlowWebResult
	if !infoFlowDecode(w, r, &req) {
		return
	}
	if !req.infoFlowScope.valid() || !infoFlowValidGroupID(req.GroupID) || len(req.LeaderRobotID) > 256 || strings.ContainsRune(req.LeaderRobotID, 0) || infoFlowWebReason(req.Reason) == "" || req.Reason == "active" || req.Reason == "permission_changed" || req.Reason == "binding_changed" || req.Reason == "request_expired" || req.Reason == infoFlowRequestCancelled || !h.infoFlowWebScope(r.Context(), req.infoFlowScope) {
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
	if req.LeaderAgentID != "" {
		if _, ok = parseUUIDOrBadRequest(w, req.LeaderAgentID, "leader_agent_id"); !ok {
			return
		}
	}
	if req.LeaderPresent && (!req.ActorPresent || req.LeaderRobotID == "" || req.LeaderAgentID == "") {
		infoFlowError(w, 400, "unverified result")
		return
	}
	j, err := infoFlowScanWebJob(h.DB.QueryRow(r.Context(), `SELECT `+infoFlowWebJobColumns+` FROM infoflow_web_group_request WHERE id=$1 AND workspace_id=$2`, id, req.WorkspaceID))
	if err != nil || j.GroupID != req.GroupID {
		infoFlowError(w, 409, "job changed")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		infoFlowError(w, 500, "begin")
		return
	}
	defer tx.Rollback(r.Context())
	if err = infoFlowWebLock(r.Context(), tx, j.WorkspaceID, j.GroupID, j.IssueID); err != nil {
		infoFlowError(w, 500, "lock")
		return
	}
	j, err = infoFlowScanWebJob(tx.QueryRow(r.Context(), `SELECT `+infoFlowWebJobColumns+` FROM infoflow_web_group_request WHERE id=$1 FOR UPDATE`, id))
	if err != nil || !infoFlowWebLeaseCurrent(j.State, j.Generation, j.LeaseToken, j.LeaseUntil, j.Expires, gen, lease) || j.LeaseRobot != req.RobotID || uuidToString(j.LeaseAgent) != strings.ToLower(req.AgentID) || j.GroupID != req.GroupID {
		infoFlowError(w, 409, "stale result")
		return
	}
	b, group, err := infoFlowWebBinding(r.Context(), tx, j.WorkspaceID, j.IssueID)
	if err != nil {
		infoFlowError(w, 500, "binding")
		return
	}
	sh, _ := h.infoFlowScoped(tx)
	t, _, manage, err := sh.infoFlowWebAuthority(r.Context(), j.WorkspaceID, j.IssueID, j.RequesterID, b)
	if err != nil {
		infoFlowError(w, 500, "authority")
		return
	}
	reason, state := req.Reason, "pending"
	// Entity/grant row locks above can wait longer than the original lease.
	// Never turn stale IM membership evidence into a newly timestamped binding.
	if !infoFlowWebLeaseCurrent(j.State, j.Generation, j.LeaseToken, j.LeaseUntil, j.Expires, gen, lease) {
		infoFlowError(w, 409, "expired membership proof")
		return
	}
	if !manage || !infoFlowSameTopology(j.Topology, t) {
		reason, state = "permission_changed", "failed"
	} else if !infoFlowWebOriginal(j, b) || group != "" && group != j.GroupID {
		reason, state = "binding_changed", "failed"
	} else if reason == "actor_not_in_group" {
		state = "failed"
	} else if req.LeaderPresent && req.ActorPresent {
		if t.LeaderAgentID != strings.ToLower(req.LeaderAgentID) {
			infoFlowError(w, 409, "leader changed")
			return
		}
		if !b.ID.Valid {
			b.ID, b.Generation = dbid.NewV7(), dbid.NewV7()
			_, err = tx.Exec(r.Context(), `INSERT INTO infoflow_issue_binding(id,robot_id,workspace_id,agent_id,conversation_kind,conversation_id,issue_id,binder_id,generation,group_topology,group_state,leader_robot_id,leader_verified_at,assignment_version) VALUES($1,$2,$3,$4,'group',$5,$6,$7,$8,$9,'active',$2,clock_timestamp(),$10)`, b.ID, req.LeaderRobotID, j.WorkspaceID, t.LeaderAgentID, j.GroupID, j.IssueID, j.RequesterID, b.Generation, j.Topology, t.AssignmentVersion)
		} else {
			generation := b.Generation
			if b.AssignmentVersion != t.AssignmentVersion {
				generation = dbid.NewV7()
			}
			_, err = tx.Exec(r.Context(), `UPDATE infoflow_issue_binding SET group_topology=$2,group_state='active',leader_robot_id=$3,leader_verified_at=clock_timestamp(),assignment_version=$4,generation=$5,bound_at=CASE WHEN generation<>$5 THEN clock_timestamp() ELSE bound_at END WHERE id=$1`, b.ID, j.Topology, req.LeaderRobotID, t.AssignmentVersion, generation)
		}
		if err != nil {
			infoFlowError(w, 409, "group admission failed")
			return
		}
		reason, state = "active", "active"
	}
	_, err = tx.Exec(r.Context(), `UPDATE infoflow_web_group_request SET state=$2,reason=$3,lease_token=NULL,lease_robot_id=NULL,lease_agent_id=NULL,lease_until=NULL,next_at=clock_timestamp()+interval '5 seconds',updated_at=clock_timestamp() WHERE id=$1`, j.ID, state, reason)
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

func infoFlowWebReserved(ctx context.Context, tx pgx.Tx, issue pgtype.UUID) (bool, error) {
	var pending bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM infoflow_web_group_request WHERE issue_id=$1 AND state IN ('queued','pending') AND expires_at>clock_timestamp())`, issue).Scan(&pending)
	return pending, err
}
