package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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
	Username    string `json:"username"`
	MachineName string `json:"machine_name,omitempty"`
}

var errMachineWorkspaceAmbiguous = errors.New("multiple accessible workspaces have this name")
var errMachineWorkspaceDisabled = errors.New("workspace creation disabled")

func (input *machineProvisionInput) validate() error {
	if !utf8.ValidString(input.MachineName) || utf8.RuneCountInString(input.MachineName) > 128 || strings.IndexFunc(input.MachineName, unicode.IsControl) >= 0 {
		return errors.New("invalid machine name")
	}
	input.MachineName = strings.TrimSpace(input.MachineName)
	return nil
}

// The caller serializes ensures for this user. Slug contention between
// different users uses ON CONFLICT without aborting the transaction.
func (h *Handler) machinePersonalWorkspace(ctx context.Context, tx pgx.Tx, userID pgtype.UUID, username string) (db.Workspace, bool, error) {
	q := h.Queries.WithTx(tx)
	rows, err := tx.Query(ctx, `SELECT w.id FROM workspace w JOIN member m ON m.workspace_id=w.id
		WHERE m.user_id=$1 AND w.name=$2 ORDER BY w.id LIMIT 2 FOR SHARE OF w,m`, userID, username)
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
	base := machineWorkspaceSlug(username)
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

// EnsureMachineWorkspace is the user-authenticated operation used by the CLI.
// It never selects a workspace outside the caller's memberships.
func (h *Handler) EnsureMachineWorkspace(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	uid, ok := parseUUIDOrBadRequest(w, userID, "user_id")
	if !ok {
		return
	}
	var input struct {
		Name string `json:"name"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace request")
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid workspace request")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || !utf8.ValidString(input.Name) || utf8.RuneCountInString(input.Name) > 128 || strings.IndexFunc(input.Name, unicode.IsControl) >= 0 {
		writeError(w, http.StatusBadRequest, "invalid workspace name")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace preparation failed")
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "tmesh-workspace:"+userID); err != nil {
		writeError(w, http.StatusInternalServerError, "workspace preparation failed")
		return
	}
	workspace, created, err := h.machinePersonalWorkspace(r.Context(), tx, uid, input.Name)
	if errors.Is(err, errMachineWorkspaceAmbiguous) {
		workspaces, listErr := h.Queries.WithTx(tx).ListWorkspaces(r.Context(), uid)
		candidates := make([]string, 0, 2)
		if listErr == nil {
			for _, candidate := range workspaces {
				if candidate.Name == input.Name {
					candidates = append(candidates, fmt.Sprintf("%s (%s)", candidate.Slug, uuidToString(candidate.ID)))
				}
			}
		}
		writeError(w, http.StatusConflict, "multiple accessible workspaces have this name: "+strings.Join(candidates, ", "))
		return
	}
	if errors.Is(err, errMachineWorkspaceDisabled) {
		writeError(w, http.StatusForbidden, "workspace creation is disabled")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace preparation failed")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "workspace preparation failed")
		return
	}
	if created {
		h.notifyDaemonWorkspacesChanged(userID)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"workspace_id": uuidToString(workspace.ID), "workspace_name": workspace.Name,
		"workspace_slug": workspace.Slug, "workspace_created": created,
	})
}

// machineWorkspaceSlug accepts display names in any language while producing a
// valid ASCII slug. Slugs, unlike workspace display names, are globally unique.
func machineWorkspaceSlug(name string) string {
	var b strings.Builder
	separator := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if separator && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			separator = false
		} else {
			separator = true
		}
		if b.Len() >= 48 {
			break
		}
	}
	base := strings.Trim(b.String(), "-")
	if base == "" {
		return "workspace"
	}
	return base
}

func machineOnboardingCommand(cfg machineCommandConfig, email, token, username string, input machineProvisionInput) string {
	cli := "multica --profile " + daemonProfileSlug(email)
	defaults := ""
	if cfg.Runtimes != "" {
		defaults += cli + " config set daemon_runtimes " + machineShellQuote(cfg.Runtimes) + "\n"
	}
	if cfg.Shims != "" {
		defaults += cli + " config set runtime_shims " + machineShellQuote(cfg.Shims) + "\n"
	}
	machineSteps := ""
	if input.MachineName != "" {
		machineSteps = `tmesh_step=machine_rename
__CLI__ tmesh-machine rename --workspace-name __USERNAME__ --machine-name __MACHINE__ > "$tmesh_result_dir/machine.json"
tmesh_complete machine_rename
tmesh_step=agent
__CLI__ tmesh-machine agent --workspace-name __USERNAME__ --agent-name __MACHINE__ --runtime ducc > "$tmesh_result_dir/agent.json"
tmesh_complete agent
`
	}
	usernameJSON, _ := json.Marshal(username)
	template := `#!/usr/bin/env bash
set +x
set -e
set -o pipefail
umask 077

# Preserve stdout for exactly one final JSON document; all ordinary logs use stderr.
exec 3>&1
exec 1>&2
tmesh_step=network
tmesh_steps=''
tmesh_result_dir=''
tmesh_complete() {
  if [ -n "$tmesh_steps" ]; then tmesh_steps="$tmesh_steps,"; fi
  tmesh_steps="$tmesh_steps\"$1\""
}
tmesh_finish() {
  tmesh_status=$?
  trap - EXIT
  set +e
  unset MULTICA_TOKEN
  tmesh_workspace='{}'; tmesh_machine='{}'; tmesh_agent='{}'
  for tmesh_part in workspace machine agent; do
    tmesh_value='{}'
    if [ -n "$tmesh_result_dir" ] && [ -s "$tmesh_result_dir/$tmesh_part.json" ]; then
      tmesh_value=$(cat "$tmesh_result_dir/$tmesh_part.json")
      case "$tmesh_value" in \{*\}) ;; *) tmesh_value='{}'; tmesh_status=1 ;; esac
    fi
    case "$tmesh_part" in
      workspace) tmesh_workspace="$tmesh_value" ;;
      machine) tmesh_machine="$tmesh_value" ;;
      agent) tmesh_agent="$tmesh_value" ;;
    esac
  done
  if [ "$tmesh_status" -eq 0 ]; then
    printf '{"success":true,"exit_code":0,"username":__USERJSON__,"completed_steps":[%s],"workspace":%s,"machine":%s,"agent":%s}\n' "$tmesh_steps" "$tmesh_workspace" "$tmesh_machine" "$tmesh_agent" >&3
  else
    printf '{"success":false,"exit_code":%s,"username":__USERJSON__,"failed_step":"%s","error":"The step failed; completed changes were retained. See stderr for details.","completed_steps":[%s],"workspace":%s,"machine":%s,"agent":%s}\n' "$tmesh_status" "$tmesh_step" "$tmesh_steps" "$tmesh_workspace" "$tmesh_machine" "$tmesh_agent" >&3
  fi
  if [ -n "$tmesh_result_dir" ]; then
    command rm -f "$tmesh_result_dir/workspace.json" "$tmesh_result_dir/machine.json" "$tmesh_result_dir/agent.json"
    rmdir "$tmesh_result_dir" 2>/dev/null
  fi
  exit "$tmesh_status"
}
trap tmesh_finish EXIT
tmesh_result_dir=$(mktemp -d "${TMPDIR:-/tmp}/tmesh-enroll.XXXXXXXX")
export no_proxy=".bcebos.com,.duckdns.org,.ai-chat.host,.baidu-int.com,10.0.0.0/8${no_proxy:+,$no_proxy}"
export NO_PROXY="$no_proxy"
export https_proxy="http://agent.baidu.com:8188"
export http_proxy="$https_proxy"
tmesh_complete network
tmesh_step=release
tmesh_release=$(curl -fsSL --connect-timeout 5 --max-time 15 -H __AUTH__ __RELEASE_URL__)
if ! printf '%s\n' "$tmesh_release" | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+-overlay\.[0-9]+$' >/dev/null; then
  printf '%s\n' 'The platform returned an invalid Team Mesh CLI release.'
  exit 1
fi
tmesh_complete release
tmesh_step=install
export MULTICA_BIN_DIR="${MULTICA_BIN_DIR:-$HOME/.local/bin}"
curl -fsSL __INSTALL__ | MULTICA_VERSION="$tmesh_release" bash
export PATH="${MULTICA_BIN_DIR:+$MULTICA_BIN_DIR:}$HOME/.local/bin:/usr/local/bin:$PATH"
if ! multica tmesh-workspace ensure --help | grep -F -- '--set-default-if-unset' >/dev/null; then
  printf '%s\n' 'Installed CLI does not support Team Mesh workspace preparation.'
  exit 1
fi
if ! multica tmesh-machine agent --help | grep -F -- '--workspace-name' >/dev/null; then
  printf '%s\n' 'Installed CLI does not support workspace names for Team Mesh agents.'
  exit 1
fi
if ! multica login --help | grep -F -- '--tmesh-noninteractive' >/dev/null; then
  printf '%s\n' 'Installed CLI does not support non-interactive Team Mesh login.'
  exit 1
fi
tmesh_complete install
tmesh_step=workspace
__CLI__ config set server_url __SERVER__
__CLI__ config set app_url __APP__
export MULTICA_TOKEN=__TOKEN__
__CLI__ tmesh-workspace ensure --name __USERNAME__ --set-default-if-unset > "$tmesh_result_dir/workspace.json"
unset MULTICA_TOKEN
tmesh_complete workspace
tmesh_step=login
__CLI__ login --tmesh-noninteractive --token __TOKEN__
tmesh_complete login
tmesh_step=runtime_setup
__DEFAULTS____CLI__ ducc setup
export PATH="$HOME/.baidu-cc/baidu-cc/bin:$HOME/.comate/baidu-cc/bin:$HOME/.baidu-cx/baidu-cx/bin:$HOME/.comate/baidu-cx/bin:$PATH"
ducc config model 'Opus 4.8'
tmesh_complete runtime_setup
tmesh_step=daemon_restart
__CLI__ daemon restart
tmesh_complete daemon_restart
__MACHINESTEPS__`
	return strings.NewReplacer(
		"__CLI__", cli, "__INSTALL__", machineShellQuote(cfg.InstallURL),
		"__SERVER__", machineShellQuote(cfg.ServerURL), "__APP__", machineShellQuote(cfg.AppURL),
		"__TOKEN__", machineShellQuote(token), "__AUTH__", machineShellQuote("Authorization: Bearer "+token),
		"__RELEASE_URL__", machineShellQuote(strings.TrimRight(cfg.ServerURL, "/")+"/api/tmesh/releases/latest"),
		"__USERNAME__", machineShellQuote(username),
		"__MACHINE__", machineShellQuote(input.MachineName), "__USERJSON__", string(usernameJSON),
		"__DEFAULTS__", defaults, "__MACHINESTEPS__", strings.NewReplacer(
			"__CLI__", cli, "__USERNAME__", machineShellQuote(username), "__MACHINE__", machineShellQuote(input.MachineName),
		).Replace(machineSteps),
	).Replace(template)
}
