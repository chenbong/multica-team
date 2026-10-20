package handler

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const personalMachineNameEvent = "daemon:personal-name"

func runtimeMachineNameKey(rt db.AgentRuntime) string {
	if rt.DaemonID.Valid && strings.TrimSpace(rt.DaemonID.String) != "" {
		return "daemon:" + rt.RuntimeMode + ":" + rt.DaemonID.String
	}
	return "runtime:" + uuidToString(rt.ID)
}

// Metadata visibility is deliberately separate from execution permission.
// Workspace admins may label private machines they can list without gaining
// access to the machine's files, credentials, local skills or execution.
func canLabelRuntime(member db.Member, rt db.AgentRuntime) bool {
	return roleAllowed(member.Role, "owner", "admin") || rt.Visibility == "public" ||
		(rt.OwnerID.Valid && rt.OwnerID == member.UserID)
}

func (h *Handler) personalRuntimeResponses(ctx context.Context, user pgtype.UUID, runtimes []db.AgentRuntime) ([]AgentRuntimeResponse, error) {
	result := make([]AgentRuntimeResponse, len(runtimes))
	if len(runtimes) == 0 {
		return result, nil
	}
	keys := make([]string, 0, len(runtimes))
	for _, rt := range runtimes {
		keys = append(keys, runtimeMachineNameKey(rt))
	}
	rows, err := h.DB.Query(ctx, `SELECT machine_key,name FROM user_machine_name WHERE user_id=$1 AND machine_key=ANY($2::text[])`, user, keys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := map[string]string{}
	for rows.Next() {
		var key, name string
		if err := rows.Scan(&key, &name); err != nil {
			return nil, err
		}
		names[key] = name
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, rt := range runtimes {
		result[i] = runtimeToResponse(rt)
		if name, ok := names[runtimeMachineNameKey(rt)]; ok {
			alias := name
			result[i].PersonalName = &alias
			result[i].CustomName = &alias
		}
	}
	return result, nil
}

// A personal name never mutates agent_runtime. Older clients using custom_name
// receive the same private semantics; no route can still edit shared defaults.
func (h *Handler) updatePersonalRuntime(ctx context.Context, expected db.AgentRuntime, actor pgtype.UUID, req UpdateAgentRuntimeRequest) (db.AgentRuntime, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return expected, err
	}
	defer tx.Rollback(ctx)
	qtx := h.Queries.WithTx(tx)
	rt, err := qtx.LockAgentRuntime(ctx, expected.ID)
	if err != nil {
		return expected, err
	}
	if rt.WorkspaceID != expected.WorkspaceID || runtimeMachineNameKey(rt) != runtimeMachineNameKey(expected) {
		return expected, errPersonalRuntimeChanged
	}
	member, err := qtx.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{WorkspaceID: rt.WorkspaceID, UserID: actor})
	if err != nil || !canLabelRuntime(member, rt) {
		return expected, errPersonalRuntimeChanged
	}
	if req.Visibility != nil && *req.Visibility != rt.Visibility {
		if !canSetRuntimeVisibility(member, rt) {
			return expected, errPersonalRuntimeChanged
		}
		rt, err = qtx.UpdateAgentRuntimeVisibility(ctx, db.UpdateAgentRuntimeVisibilityParams{ID: rt.ID, Visibility: *req.Visibility})
		if err != nil {
			return expected, err
		}
	}
	if req.CustomName != nil {
		key := runtimeMachineNameKey(rt)
		// Serializes clear/upsert even when no note row exists yet.
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "personal-machine-name:"+uuidToString(actor)+":"+key); err != nil {
			return expected, err
		}
		name := strings.TrimSpace(*req.CustomName)
		if name == "" {
			_, err = tx.Exec(ctx, `DELETE FROM user_machine_name WHERE user_id=$1 AND machine_key=$2`, actor, key)
		} else {
			_, err = tx.Exec(ctx, `INSERT INTO user_machine_name(user_id,machine_key,name) VALUES($1,$2,$3)
    ON CONFLICT(user_id,machine_key) DO UPDATE SET name=EXCLUDED.name,updated_at=now()`, actor, key, name)
		}
		if err != nil {
			return expected, err
		}
	}
	return rt, tx.Commit(ctx)
}

var errPersonalRuntimeChanged = errors.New("runtime access or registration changed")
