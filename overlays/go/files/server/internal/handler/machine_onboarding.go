package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/issuestatus"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type machineProvisionInput struct {
	Username      string  `json:"username"`
	MachineName   string  `json:"machine_name,omitempty"`
	AgentName     string  `json:"agent_name,omitempty"`
	Runtime       string  `json:"runtime,omitempty"`
	Model         *string `json:"model,omitempty"`
	ThinkingLevel *string `json:"thinking_level,omitempty"`
}

var machineRuntimeName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
var machineSlugSeparators = regexp.MustCompile(`[-_.]+`)
var errMachineWorkspaceAmbiguous = errors.New("ambiguous username workspace")
var errMachineWorkspaceDisabled = errors.New("workspace creation disabled")

func (input *machineProvisionInput) validate() error {
	for _, value := range []string{input.MachineName, input.AgentName} {
		if !utf8.ValidString(value) || utf8.RuneCountInString(value) > 128 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return errors.New("invalid machine or agent name")
		}
	}
	input.MachineName = strings.TrimSpace(input.MachineName)
	input.AgentName = strings.TrimSpace(input.AgentName)
	if input.Runtime == "" {
		input.Runtime = "ducc"
	}
	if !machineRuntimeName.MatchString(input.Runtime) {
		return errors.New("invalid runtime")
	}
	for _, value := range []*string{input.Model, input.ThinkingLevel} {
		if value != nil && (!utf8.ValidString(*value) || len(*value) > 256 || strings.IndexFunc(*value, unicode.IsControl) >= 0) {
			return errors.New("invalid model or thinking level")
		}
	}
	if input.AgentName == "" && (input.Model != nil || input.ThinkingLevel != nil) {
		return errors.New("agent_name is required with model or thinking_level")
	}
	return nil
}

// The caller already owns the user-row lock. Slug contention between different
// users uses ON CONFLICT, so an expected collision never aborts the transaction.
func (h *Handler) machinePersonalWorkspace(ctx context.Context, tx pgx.Tx, userID pgtype.UUID, username string) (db.Workspace, bool, error) {
	q := h.Queries.WithTx(tx)
	rows, err := tx.Query(ctx, `SELECT w.id FROM workspace w JOIN member m ON m.workspace_id=w.id
		WHERE m.user_id=$1 AND m.role='owner' AND w.name=$2 ORDER BY w.id LIMIT 2 FOR SHARE OF w,m`, userID, username)
	if err != nil {
		return db.Workspace{}, false, err
	}
	var ids []pgtype.UUID
	for rows.Next() {
		var id pgtype.UUID
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return db.Workspace{}, false, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return db.Workspace{}, false, err
	}
	if len(ids) > 1 {
		return db.Workspace{}, false, errMachineWorkspaceAmbiguous
	}
	if len(ids) == 1 {
		ws, e := q.GetWorkspace(ctx, ids[0])
		return ws, false, e
	}
	if h.cfg.DisableWorkspaceCreation {
		return db.Workspace{}, false, errMachineWorkspaceDisabled
	}
	base := strings.Trim(machineSlugSeparators.ReplaceAllString(username, "-"), "-")
	for attempt := 0; attempt < 5; attempt++ {
		slug := base
		if attempt > 0 || isReservedSlug(slug) {
			slug += "-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
		}
		var id pgtype.UUID
		err = tx.QueryRow(ctx, `INSERT INTO workspace(name,slug,issue_prefix) VALUES($1,$2,$3)
			ON CONFLICT(slug) DO NOTHING RETURNING id`, username, slug, defaultIssuePrefixFromSlug(slug)).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return db.Workspace{}, false, err
		}
		if _, err = q.CreateMember(ctx, db.CreateMemberParams{WorkspaceID: id, UserID: userID, Role: "owner"}); err != nil {
			return db.Workspace{}, false, err
		}
		if err = issuestatus.Ensure(ctx, q, id); err != nil {
			return db.Workspace{}, false, err
		}
		ws, e := q.GetWorkspace(ctx, id)
		return ws, true, e
	}
	return db.Workspace{}, false, errors.New("workspace slug collision")
}

func machineOnboardingCommand(cfg machineCommandConfig, email, token, workspaceID string, input machineProvisionInput) string {
	cli := "multica --profile " + daemonProfileSlug(email)
	command := machineLinuxCommand(cfg, email, token)
	login := cli + " login --token "
	prepare := cli + " tmesh-machine prepare --workspace-id " + machineShellQuote(workspaceID) + "\n"
	command = strings.Replace(command, login, prepare+login, 1)
	// Keep the existing Web command template intact. This API wraps its steps
	// with a machine-readable result contract without requiring Python or jq.
	for _, pair := range [][2]string{
		{"curl -fsSL ", "tmesh_complete network\ntmesh_step=install\nexport MULTICA_BIN_DIR=\"${MULTICA_BIN_DIR:-$HOME/.local/bin}\"\ncurl -fsSL "},
		{"# 2. 配置平台地址并登录", "export PATH=\"${MULTICA_BIN_DIR:+$MULTICA_BIN_DIR:}$HOME/.local/bin:/usr/local/bin:$PATH\"\nmultica tmesh-machine --help >/dev/null\ntmesh_complete install\ntmesh_step=login\n\n# 2. 配置平台地址并登录"},
		{"# 3. 配置运行时并准备 ducc、ducx", "tmesh_complete login\ntmesh_step=runtime_setup\n\n# 3. 配置运行时并准备 ducc、ducx"},
		{"# 4. 启动守护进程，连接平台", "tmesh_complete runtime_setup\ntmesh_step=daemon_restart\n\n# 4. 启动守护进程，连接平台"},
	} {
		command = strings.Replace(command, pair[0], pair[1], 1)
	}
	args := []string{cli, "--workspace-id", machineShellQuote(workspaceID), "tmesh-machine enroll"}
	for _, pair := range [][2]string{{"machine-name", input.MachineName}, {"agent-name", input.AgentName}} {
		if pair[1] != "" {
			args = append(args, "--"+pair[0], machineShellQuote(pair[1]))
		}
	}
	if input.AgentName != "" {
		args = append(args, "--runtime", machineShellQuote(input.Runtime))
		if input.Model != nil {
			args = append(args, "--model", machineShellQuote(*input.Model))
		}
		if input.ThinkingLevel != nil {
			args = append(args, "--thinking-level", machineShellQuote(*input.ThinkingLevel))
		}
	}
	workspaceJSON, _ := json.Marshal(workspaceID)
	preamble := strings.ReplaceAll(`#!/usr/bin/env bash
set -e
set -o pipefail
umask 077

# Preserve stdout for exactly one final JSON document; all ordinary logs use stderr.
exec 3>&1
exec 1>&2
tmesh_step=network
tmesh_steps=''
tmesh_result_file=''
tmesh_complete() {
  if [ -n "$tmesh_steps" ]; then tmesh_steps="$tmesh_steps,"; fi
  tmesh_steps="$tmesh_steps\"$1\""
}
tmesh_finish() {
  tmesh_status=$?
  trap - EXIT
  set +e
  tmesh_result='{}'
  if [ -n "$tmesh_result_file" ] && [ -s "$tmesh_result_file" ]; then
    tmesh_result=$(cat "$tmesh_result_file")
    case "$tmesh_result" in
      \{*\}) ;;
      *) tmesh_result='{}'; tmesh_status=1 ;;
    esac
  fi
  if [ "$tmesh_status" -eq 0 ]; then
    printf '{"success":true,"exit_code":0,"workspace_id":__WORKSPACE_JSON__,"completed_steps":[%s],"result":%s}\n' "$tmesh_steps" "$tmesh_result" >&3
  else
    printf '{"success":false,"exit_code":%s,"workspace_id":__WORKSPACE_JSON__,"failed_step":"%s","error":"The step failed; completed changes were retained. See stderr for details.","completed_steps":[%s],"result":%s}\n' "$tmesh_status" "$tmesh_step" "$tmesh_steps" "$tmesh_result" >&3
  fi
  if [ -n "$tmesh_result_file" ]; then
    command rm -f "$tmesh_result_file"
    rmdir "${tmesh_result_file%/*}" 2>/dev/null
  fi
  exit "$tmesh_status"
}
trap tmesh_finish EXIT
tmesh_result_dir=$(mktemp -d "${TMPDIR:-/tmp}/tmesh-enroll.XXXXXXXX")
tmesh_result_file="$tmesh_result_dir/result.json"

`, "__WORKSPACE_JSON__", string(workspaceJSON))
	return fmt.Sprintf("%s%s\ntmesh_complete daemon_restart\n\n# 5. 等待本机上线，并按需重命名和创建或复用智能体\ntmesh_step=enrollment\n%s > \"$tmesh_result_file\"\ntmesh_complete enrollment\n", preamble, command, strings.Join(args, " "))
}
