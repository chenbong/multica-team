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
	Username      string `json:"username"`
	MachineName   string `json:"machine_name,omitempty"`
	WorkspaceName string `json:"workspace_name,omitempty"`
}

const defaultMachineProvisionWorkspace = "pdc-loop"

var errMachineWorkspaceAmbiguous = errors.New("multiple accessible workspaces have this name")
var errMachineWorkspaceDisabled = errors.New("workspace creation disabled")
var errMachineProvisionWorkspaceMissing = errors.New("target workspace not found")
var errMachineProvisionWorkspaceAmbiguous = errors.New("multiple workspaces have the target name")

func (input machineProvisionInput) workspaceName() string {
	if name := strings.TrimSpace(input.WorkspaceName); name != "" {
		return name
	}
	return defaultMachineProvisionWorkspace
}

func (input *machineProvisionInput) validate() error {
	if !utf8.ValidString(input.MachineName) || utf8.RuneCountInString(input.MachineName) > 128 || strings.IndexFunc(input.MachineName, unicode.IsControl) >= 0 {
		return errors.New("invalid machine name")
	}
	input.MachineName = strings.TrimSpace(input.MachineName)
	if !utf8.ValidString(input.WorkspaceName) || utf8.RuneCountInString(input.WorkspaceName) > 128 || strings.IndexFunc(input.WorkspaceName, unicode.IsControl) >= 0 {
		return errors.New("invalid workspace name")
	}
	input.WorkspaceName = input.workspaceName()
	return nil
}

// The provisioning credential may join a user to an existing workspace. The
// client ensure command remains membership-scoped and must not guess a target.
func machineProvisionWorkspaceByName(ctx context.Context, tx pgx.Tx, name string) (db.Workspace, error) {
	rows, err := tx.Query(ctx, `SELECT id FROM workspace WHERE name=$1 ORDER BY id LIMIT 2 FOR SHARE`, name)
	if err != nil {
		return db.Workspace{}, err
	}
	var ids []pgtype.UUID
	for rows.Next() {
		var id pgtype.UUID
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return db.Workspace{}, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return db.Workspace{}, err
	}
	if len(ids) == 0 {
		return db.Workspace{}, errMachineProvisionWorkspaceMissing
	}
	if len(ids) > 1 {
		return db.Workspace{}, errMachineProvisionWorkspaceAmbiguous
	}
	return db.New(tx).GetWorkspace(ctx, ids[0])
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

func machineOnboardingCommand(cfg machineCommandConfig, email, token string, input machineProvisionInput) string {
	cli := "multica --profile " + daemonProfileSlug(email)
	workspace := machineShellQuote(input.workspaceName())
	defaults := ""
	if cfg.Runtimes != "" {
		defaults += cli + " config set daemon_runtimes " + machineShellQuote(cfg.Runtimes) + "\n"
	}
	if cfg.Shims != "" {
		defaults += cli + " config set runtime_shims " + machineShellQuote(cfg.Shims) + "\n"
	}
	machineSteps := ""
	if input.MachineName != "" {
		machineSteps = `
# 5. 重命名机器并创建或复用智能体
__CLI__ tmesh-machine rename --workspace-name __WORKSPACE__ --machine-name __MACHINE__
__CLI__ tmesh-machine agent --workspace-name __WORKSPACE__ --agent-name __MACHINE__ --runtime ducc
`
	}
	template := `#!/usr/bin/env bash
set +x
set -e -o pipefail
umask 077

# 1. 配置网络并安装 Multica CLI
export no_proxy=".bcebos.com,.duckdns.org,.ai-chat.host,.baidu-int.com,10.0.0.0/8${no_proxy:+,$no_proxy}"
export NO_PROXY="$no_proxy"
export https_proxy="http://agent.baidu.com:8188"
export http_proxy="$https_proxy"
export MULTICA_BIN_DIR="${MULTICA_BIN_DIR:-$HOME/.local/bin}"
curl -fsSL __INSTALL__ | bash
export PATH="$MULTICA_BIN_DIR:$HOME/.local/bin:/usr/local/bin:$PATH"

# 2. 配置平台地址、登录并准备工作区
__CLI__ config set server_url __SERVER__
__CLI__ config set app_url __APP__
__CLI__ login --tmesh-noninteractive --token __TOKEN__
__CLI__ tmesh-workspace ensure --name __WORKSPACE__

# 3. 配置运行时并准备 ducc、ducx
__DEFAULTS____CLI__ ducc setup
export PATH="$HOME/.baidu-cc/baidu-cc/bin:$HOME/.comate/baidu-cc/bin:$HOME/.baidu-cx/baidu-cx/bin:$HOME/.comate/baidu-cx/bin:$PATH"
ducc config model 'Opus 4.8'

# 4. 启动守护进程，连接平台
__CLI__ daemon restart
__MACHINESTEPS__`
	return strings.NewReplacer(
		"__CLI__", cli, "__INSTALL__", machineShellQuote(cfg.InstallURL),
		"__SERVER__", machineShellQuote(cfg.ServerURL), "__APP__", machineShellQuote(cfg.AppURL),
		"__TOKEN__", machineShellQuote(token), "__WORKSPACE__", workspace,
		"__MACHINE__", machineShellQuote(input.MachineName), "__DEFAULTS__", defaults,
		"__MACHINESTEPS__", strings.NewReplacer(
			"__CLI__", cli, "__WORKSPACE__", workspace, "__MACHINE__", machineShellQuote(input.MachineName),
		).Replace(machineSteps),
	).Replace(template)
}
