package handler

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var machineUsername = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]{0,62}[a-z0-9])?$`)
var machineDomain = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`)

type machineCommandConfig struct {
	InstallURL string `json:"MULTICA_CLI_INSTALL_URL"`
	Runtimes   string `json:"MULTICA_DAEMON_RUNTIME_DEFAULTS"`
	Shims      string `json:"MULTICA_DAEMON_RUNTIME_SHIM_DEFAULTS"`
	ServerURL  string
	AppURL     string
}

func machineSafeURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil && u.Fragment == "" && !strings.ContainsAny(raw, "\r\n\x00")
}

func loadMachineCommandConfig() (machineCommandConfig, error) {
	var cfg machineCommandConfig
	path := os.Getenv("MULTICA_WEB_BUILD_CONFIG")
	if !filepath.IsAbs(path) {
		return cfg, errors.New("command configuration missing")
	}
	file, err := os.Open(path)
	if err != nil {
		return cfg, errors.New("command configuration unreadable")
	}
	defer file.Close()
	bytes, err := io.ReadAll(io.LimitReader(file, 64*1024+1))
	if err != nil || len(bytes) > 64*1024 {
		return cfg, errors.New("command configuration unreadable")
	}
	if err = json.Unmarshal(bytes, &cfg); err != nil {
		return cfg, errors.New("invalid command configuration")
	}
	cfg.ServerURL, cfg.AppURL = daemonSetupURLsFromEnv()
	if !machineSafeURL(cfg.ServerURL) || !machineSafeURL(cfg.AppURL) || !machineSafeURL(cfg.InstallURL) || strings.ContainsAny(cfg.Runtimes+cfg.Shims, "\r\n\x00") {
		return cfg, errors.New("invalid command configuration")
	}
	cfg.InstallURL = strings.TrimSpace(cfg.InstallURL)
	cfg.Runtimes = machineRuntimeDefaults(cfg.Runtimes)
	cfg.Shims = machineRuntimeDefaults(cfg.Shims)
	return cfg, nil
}

func machineRuntimeDefaults(raw string) string {
	values := []string{}
	seen := map[string]bool{}
	for _, value := range strings.Fields(strings.ReplaceAll(raw, ",", " ")) {
		if !seen[value] {
			values = append(values, value)
			seen[value] = true
		}
	}
	return strings.Join(values, ",")
}

// MachineProvisionCommand is an operator-only delegation endpoint, not user
// authentication. It never runs a shell, writes a CLI profile, or starts a daemon.
func (h *Handler) MachineProvisionCommand(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, 405, "POST required")
		return
	}
	for _, key := range []string{"X-User-ID", "X-User-Email", "X-Agent-ID", "X-Task-ID", "X-Actor-Source", "X-Workspace-ID"} {
		r.Header.Del(key)
	}
	serviceToken := os.Getenv("MULTICA_MACHINE_PROVISION_TOKEN")
	if len(serviceToken) < 32 || strings.ContainsAny(serviceToken, " \t\r\n\x00") {
		writeError(w, 503, "machine provisioning unavailable")
		return
	}
	header := r.Header.Get("Authorization")
	want, got := sha256.Sum256([]byte(serviceToken)), sha256.Sum256([]byte(strings.TrimPrefix(header, "Bearer ")))
	if len(r.Header.Values("Authorization")) != 1 || !strings.HasPrefix(header, "Bearer ") || subtle.ConstantTimeCompare(want[:], got[:]) != 1 {
		w.Header().Set("WWW-Authenticate", `Bearer realm="machine-provision"`)
		writeError(w, 401, "unauthorized")
		return
	}
	if r.Header.Get("Origin") != "" || r.URL.RawQuery != "" {
		writeError(w, 400, "backend JSON requests only")
		return
	}
	if strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]) != "application/json" {
		writeError(w, 415, "application/json required")
		return
	}
	var input machineProvisionInput
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, 400, "invalid request")
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		writeError(w, 400, "invalid request")
		return
	}
	username := strings.ToLower(input.Username)
	if !machineUsername.MatchString(username) {
		writeError(w, 400, "invalid username")
		return
	}
	if err := input.validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	domain := os.Getenv("MULTICA_MACHINE_PROVISION_EMAIL_DOMAIN")
	if domain != strings.ToLower(domain) || len(domain) > 253 || !machineDomain.MatchString(domain) {
		writeError(w, 503, "machine provisioning unavailable")
		return
	}
	cfg, err := loadMachineCommandConfig()
	if err != nil {
		writeError(w, 503, "machine command configuration unavailable")
		return
	}
	key, err := secretbox.LoadKey("MULTICA_PLUGIN_SECRET_KEY")
	if err != nil {
		writeError(w, 503, "token encryption unavailable")
		return
	}
	box, err := secretbox.New(key)
	if err != nil {
		writeError(w, 503, "token encryption unavailable")
		return
	}
	email, identityOK, identityErr := h.resolveCompanyAccountEmail(r.Context(), username, domain)
	if errors.Is(identityErr, errAmbiguousCompanyAccount) {
		writeErrorCode(w, 409, "ambiguous_account", "More than one account matches this username; use the Web interface or contact the administrator")
		return
	}
	if identityErr != nil {
		writeError(w, 500, "Unable to resolve account identity")
		return
	}
	if !identityOK {
		writeError(w, 400, "ambiguous username")
		return
	}
	if auth.IsTemporarilyDisabledUserEmail(email) {
		writeError(w, 403, "account unavailable")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "provisioning failed")
		return
	}
	defer tx.Rollback(r.Context())
	user, created, err := h.machineProvisionUser(r.Context(), tx, username, email)
	if err != nil {
		if errors.Is(err, auth.ErrTemporarilyDisabledUser) || errors.Is(err, ErrSignupProhibited) || errors.Is(err, ErrEmailNotAllowed) {
			writeError(w, 403, "account unavailable")
			return
		}
		writeError(w, 500, "provisioning failed")
		return
	}
	if created {
		if _, err = tx.Exec(r.Context(), `UPDATE "user" SET onboarded_at=clock_timestamp() WHERE id=$1`, user.ID); err != nil {
			writeError(w, 500, "account initialization failed")
			return
		}
	}
	raw, _, err := h.daemonBootstrapTokenInTx(r.Context(), tx, user.ID, box)
	if err != nil {
		writeError(w, 500, "token issuance failed")
		return
	}
	command := machineOnboardingCommand(cfg, email, raw, username, input)
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "provisioning commit failed")
		return
	}
	slog.Info("machine provisioning command issued", "user_id", uuidToString(user.ID), "user_created", created)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, command)
}

func (h *Handler) machineProvisionUser(ctx context.Context, tx pgx.Tx, username, email string) (db.User, bool, error) {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "machine-provision:"+email); err != nil {
		return db.User{}, false, err
	}
	q := h.Queries.WithTx(tx)
	user, err := q.GetUserByEmail(ctx, email)
	created := false
	if errors.Is(err, pgx.ErrNoRows) {
		// Invitation-aware policy checks must use this transaction as well:
		// parallel requests must not exhaust the pool waiting for another slot.
		scoped := *h
		scoped.Queries = q
		if err = scoped.checkSignupAllowed(ctx, email, true); err != nil {
			return db.User{}, false, err
		}
		// ON CONFLICT also serializes concurrent public sign-up for this email.
		var id string
		err = tx.QueryRow(ctx, `INSERT INTO "user"(name,email) VALUES($1,$2) ON CONFLICT DO NOTHING RETURNING id::text`, username, email).Scan(&id)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return db.User{}, false, err
		}
		created = err == nil
	}
	user, err = q.GetUserByEmail(ctx, email)
	if err != nil {
		return db.User{}, false, err
	}
	// Match token issuance's user-row lock before reading or creating membership.
	if _, err = tx.Exec(ctx, `SELECT id FROM "user" WHERE id=$1 FOR UPDATE`, user.ID); err != nil {
		return db.User{}, false, err
	}
	if auth.IsTemporarilyDisabledUser(uuidToString(user.ID), user.Email) {
		return db.User{}, false, auth.ErrTemporarilyDisabledUser
	}
	return user, created, nil
}

func machineShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

// Keep byte-for-byte parity with connectionCommand's Linux variant. The parity
// test imports the actual Web helper with fake inputs; it never executes output.
func machineLinuxCommand(cfg machineCommandConfig, email, token string) string {
	profile := daemonProfileSlug(email)
	cli := "multica --profile " + profile
	defaults := []string{}
	if cfg.Runtimes != "" {
		defaults = append(defaults, cli+" config set daemon_runtimes "+machineShellQuote(cfg.Runtimes))
	}
	if cfg.Shims != "" {
		defaults = append(defaults, cli+" config set runtime_shims "+machineShellQuote(cfg.Shims))
	}
	setup := ""
	if len(defaults) > 0 {
		setup = strings.Join(defaults, "\n") + "\n\n"
	}
	return fmt.Sprintf(`# 1. 配置网络并安装 Multica CLI
export no_proxy=".bcebos.com,.duckdns.org,.ai-chat.host,.baidu-int.com,10.0.0.0/8${no_proxy:+,$no_proxy}"
export NO_PROXY="$no_proxy"
export https_proxy="http://agent.baidu.com:8188"
export http_proxy="$https_proxy"

curl -fsSL %s | bash

# 2. 配置平台地址并登录
%s config set server_url %s
%s config set app_url %s
%s login --token %s

# 3. 配置运行时并准备 ducc、ducx
%s%s ducc setup
export PATH="$HOME/.baidu-cc/baidu-cc/bin:$HOME/.comate/baidu-cc/bin:$HOME/.baidu-cx/baidu-cx/bin:$HOME/.comate/baidu-cx/bin:$PATH"
ducc config model 'Opus 4.8'

# 4. 启动守护进程，连接平台
%s daemon restart`, machineShellQuote(cfg.InstallURL), cli, machineShellQuote(cfg.ServerURL), cli, machineShellQuote(cfg.AppURL), cli, machineShellQuote(token), setup, cli, cli)
}
