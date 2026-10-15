package handler

import (
	"context"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// Only public configuration metadata is accepted, never robot credentials.
type infoFlowRobotObservation struct {
	infoFlowScope
	RobotName      string `json:"robot_name"`
	RobotChatID    string `json:"robot_chat_id"`
	PrivateEnabled *bool  `json:"private_enabled"`
	Available      *bool  `json:"available"`
}

type infoFlowPrivateRobotPreview struct {
	State       string `json:"state"`
	RobotID     string `json:"robot_id,omitempty"`
	Name        string `json:"name,omitempty"`
	RobotChatID string `json:"robot_chat_id,omitempty"`
}

// InfoFlowRobotDirectory records a five-minute heartbeat for a configured robot.
// The relay token is the same trusted boundary used by binding reconciliation.
func (h *Handler) InfoFlowRobotDirectory(w http.ResponseWriter, r *http.Request) {
	if !infoFlowAuthenticate(w, r) {
		return
	}
	var req infoFlowRobotObservation
	if !infoFlowDecode(w, r, &req) {
		return
	}
	if !req.infoFlowScope.valid() || strings.TrimSpace(req.RobotID) != req.RobotID ||
		!utf8.ValidString(req.RobotName) || len(req.RobotName) > 256 || strings.ContainsAny(req.RobotName, "\x00\r\n") ||
		(req.RobotChatID != "" && !infoFlowValidGroupID(req.RobotChatID)) || req.PrivateEnabled == nil || req.Available == nil {
		infoFlowError(w, http.StatusBadRequest, "robot metadata")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		infoFlowError(w, 500, "robot directory")
		return
	}
	defer tx.Rollback(r.Context())
	// Fence archival against publication of an obsolete active-agent observation.
	var agent string
	err = tx.QueryRow(r.Context(), `SELECT id::text FROM agent WHERE id=$1 AND workspace_id=$2 AND archived_at IS NULL FOR SHARE`, req.AgentID, req.WorkspaceID).Scan(&agent)
	if err == pgx.ErrNoRows {
		infoFlowError(w, 400, "robot scope")
		return
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO infoflow_robot_directory(robot_id,agent_id,workspace_id,robot_name,robot_chat_id,private_enabled,available)
			VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(robot_id) DO UPDATE SET agent_id=EXCLUDED.agent_id,workspace_id=EXCLUDED.workspace_id,
			robot_name=EXCLUDED.robot_name,robot_chat_id=EXCLUDED.robot_chat_id,private_enabled=EXCLUDED.private_enabled,
			available=EXCLUDED.available,observed_at=clock_timestamp()`, req.RobotID, req.AgentID, req.WorkspaceID, strings.TrimSpace(req.RobotName), req.RobotChatID, *req.PrivateEnabled, *req.Available)
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		infoFlowError(w, 500, "robot directory")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func infoFlowPrivateRobot(ctx context.Context, tx pgx.Tx, ws, agent string) (infoFlowPrivateRobotPreview, error) {
	rows, err := tx.Query(ctx, `SELECT robot_id,robot_name,robot_chat_id,private_enabled,available,
		observed_at > clock_timestamp()-interval '5 minutes' FROM infoflow_robot_directory
		WHERE workspace_id=$1 AND agent_id=$2 ORDER BY robot_id`, ws, agent)
	if err != nil {
		return infoFlowPrivateRobotPreview{}, err
	}
	defer rows.Close()
	out := infoFlowPrivateRobotPreview{State: "missing"}
	current, expired := 0, false
	for rows.Next() {
		var candidate infoFlowPrivateRobotPreview
		var enabled, available, fresh bool
		if err = rows.Scan(&candidate.RobotID, &candidate.Name, &candidate.RobotChatID, &enabled, &available, &fresh); err != nil {
			return out, err
		}
		if !fresh {
			expired = true
			continue
		}
		current++
		candidate.State = "unavailable"
		if enabled && available && candidate.Name != "" && infoFlowValidGroupID(candidate.RobotChatID) {
			candidate.State = "ready"
		}
		out = candidate
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if current > 1 {
		return infoFlowPrivateRobotPreview{State: "ambiguous"}, nil
	}
	if current == 0 && expired {
		return infoFlowPrivateRobotPreview{State: "unavailable"}, nil
	}
	return out, nil
}
