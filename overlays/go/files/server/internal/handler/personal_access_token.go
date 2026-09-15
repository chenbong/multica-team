package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// GetOrCreateDaemonBootstrapToken atomically issues or reuses a user-owned PAT.
// Locking the existing user row serializes first-use requests across servers.
func (h *Handler) GetOrCreateDaemonBootstrapToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	if isMachineCredentialActor(r) {
		writeError(w, http.StatusForbidden, "human authentication required")
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	owner, ok := parseUUIDOrBadRequest(w, userID, "user id")
	if !ok {
		return
	}
	raw, pat, err := h.getOrCreateDaemonBootstrapToken(r.Context(), owner)
	if err != nil {
		if errors.Is(err, errDaemonTokenEncryptionUnavailable) {
			writeError(w, http.StatusServiceUnavailable, "token encryption unavailable")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to issue daemon token")
		return
	}
	writeJSON(w, http.StatusOK, CreatePATResponse{PersonalAccessTokenResponse: patToResponse(pat), Token: raw})
}

// errDaemonTokenEncryptionUnavailable separates "this deployment has no usable
// plugin secret" (503, an operator problem) from real failures (500). The
// login-time provisioning in daemon_provision.go shares this core, so both
// callers have to tell the two apart.
var errDaemonTokenEncryptionUnavailable = errors.New("token encryption unavailable")

// getOrCreateDaemonBootstrapToken is the shared core behind
// POST /api/daemon-bootstrap-token and login-time provisioning: it returns the
// caller's single long-lived PAT, creating it on first use. Two callers can
// race — the row lock below serializes them, so a user ends up with exactly one
// token however many tabs, dialogs or logins ask for it.
func (h *Handler) getOrCreateDaemonBootstrapToken(ctx context.Context, owner pgtype.UUID) (string, db.PersonalAccessToken, error) {
	key, err := secretbox.LoadKey("MULTICA_PLUGIN_SECRET_KEY")
	if err != nil {
		return "", db.PersonalAccessToken{}, errDaemonTokenEncryptionUnavailable
	}
	box, err := secretbox.New(key)
	if err != nil {
		return "", db.PersonalAccessToken{}, errDaemonTokenEncryptionUnavailable
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return "", db.PersonalAccessToken{}, fmt.Errorf("begin token transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	var encrypted []byte
	if err := tx.QueryRow(ctx, `SELECT daemon_bootstrap_token_encrypted FROM "user" WHERE id=$1 FOR UPDATE`, owner).Scan(&encrypted); err != nil {
		return "", db.PersonalAccessToken{}, fmt.Errorf("load daemon token: %w", err)
	}
	q := h.Queries.WithTx(tx)
	var raw string
	var pat db.PersonalAccessToken
	if len(encrypted) > 0 {
		plain, err := box.Open(encrypted)
		if err != nil {
			return "", db.PersonalAccessToken{}, fmt.Errorf("decrypt daemon token: %w", err)
		}
		raw = string(plain)
		// Validate both the encrypted payload and its PAT owner before returning it.
		pat, err = q.GetPersonalAccessTokenByHash(ctx, auth.HashToken(raw))
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return "", db.PersonalAccessToken{}, fmt.Errorf("validate daemon token: %w", err)
		}
		if err == nil && (pat.UserID != owner || !strings.HasPrefix(raw, "mul_")) {
			return "", db.PersonalAccessToken{}, errors.New("daemon token owner mismatch")
		}
		if errors.Is(err, pgx.ErrNoRows) {
			raw = ""
		}
	}
	if raw == "" {
		raw, err = auth.GeneratePATToken()
		if err != nil {
			return "", db.PersonalAccessToken{}, fmt.Errorf("generate daemon token: %w", err)
		}
		sealed, err := box.Seal([]byte(raw))
		if err != nil {
			return "", db.PersonalAccessToken{}, fmt.Errorf("encrypt daemon token: %w", err)
		}
		pat, err = q.CreatePersonalAccessToken(ctx, db.CreatePersonalAccessTokenParams{
			UserID: owner, Name: "Multica daemon (shared)", TokenHash: auth.HashToken(raw), TokenPrefix: raw[:12],
			ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(PATRenewExtension), Valid: true},
		})
		if err != nil {
			return "", db.PersonalAccessToken{}, fmt.Errorf("create daemon token: %w", err)
		}
		if _, err = tx.Exec(ctx, `UPDATE "user" SET daemon_bootstrap_token_encrypted=$1 WHERE id=$2`, sealed, owner); err != nil {
			return "", db.PersonalAccessToken{}, fmt.Errorf("store daemon token: %w", err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return "", db.PersonalAccessToken{}, fmt.Errorf("commit daemon token: %w", err)
	}
	return raw, pat, nil
}

// --- login-time provisioning of the machine's daemon profile ----------------
//
// This deployment runs one API + Web + Bridge per host, and the Bridge reads
// the Multica accounts it may act as from the CLI profiles under ~/.multica. A
// user who had only ever signed into the web app used to be invisible there:
// the binding page told them to install the CLI and log in from a terminal.
//
// Logging in is already the strongest proof of identity this deployment has —
// the code is delivered through the IM robot to the address that owns it — so
// the API does that work itself the moment /auth/verify-code succeeds: it issues
// the same per-user PAT the "add a computer" dialog hands out, and writes the
// profile both the Bridge and the CLI read. Nothing else in this repository
// writes that file, and a profile is only ever written for the address that has
// just proved itself in that request.

// ProvisionDaemonAccessAfterLogin wraps a login handler and, once it has
// answered 2xx, issues or reuses the caller's daemon token and writes the
// server-side CLI profile. A failure here never changes the login result: the
// user is signed in either way, and the Bridge keeps reporting the account as
// not linked on this machine until the next successful login.
func (h *Handler) ProvisionDaemonAccessAfterLogin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// The login body is a few hundred bytes. The cap only stops a caller
		// from streaming something huge at a public route.
		body, readErr := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if readErr == nil {
			// The wrapped handler still has to read it.
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next(recorder, r)
		if readErr != nil || recorder.status < 200 || recorder.status >= 300 {
			return
		}
		email := loginBodyEmail(body)
		if email == "" {
			return
		}
		if err := h.provisionDaemonAccess(r.Context(), email); err != nil {
			slog.Warn("daemon profile provisioning failed", "email", email, "error", err)
		}
	}
}

// statusRecorder remembers the status code without buffering the body: the
// login response has to reach the browser unchanged.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	return s.ResponseWriter.Write(b)
}

func loginBodyEmail(body []byte) string {
	var payload struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(payload.Email))
}

// provisionDaemonAccess issues the user's single PAT and records it in the CLI
// profile the Bridge reads.
func (h *Handler) provisionDaemonAccess(ctx context.Context, email string) error {
	user, err := h.Queries.GetUserByEmail(ctx, email)
	if err != nil {
		return fmt.Errorf("load user %s: %w", email, err)
	}
	token, _, err := h.getOrCreateDaemonBootstrapToken(ctx, user.ID)
	if err != nil {
		return fmt.Errorf("issue daemon token for %s: %w", email, err)
	}
	// A first-time user has no workspace yet, so an empty id here is expected
	// rather than broken: the Bridge adopts the first workspace it sees once the
	// account has one, and writes it back into this same profile.
	workspaceID := ""
	if workspaces, err := h.Queries.ListWorkspaces(ctx, user.ID); err != nil {
		slog.Warn("could not list workspaces for the daemon profile", "email", email, "error", err)
	} else if len(workspaces) > 0 {
		workspaceID = uuidToString(workspaces[0].ID)
	}
	if err := writeDaemonCLIProfile(email, token, workspaceID); err != nil {
		return err
	}
	slog.Info("daemon CLI profile provisioned", "email", email, "profile", daemonProfileSlug(email), "workspace", workspaceID)
	return nil
}

// writeDaemonCLIProfile writes the profile the CLI (and therefore the Bridge)
// runs as. It merges into an existing file instead of replacing it, so
// operator-set fields — device_name, workspaces_root, daemon_runtimes — survive
// a later login. The file holds a credential, so directory and file keep the
// 0700/0600 modes `multica login` leaves behind.
func writeDaemonCLIProfile(email, token, workspaceID string) error {
	serverURL := strings.TrimSpace(os.Getenv("MULTICA_DAEMON_SERVER_URL"))
	if serverURL == "" {
		serverURL = strings.TrimSpace(os.Getenv("MULTICA_PUBLIC_URL"))
	}
	appURL := strings.TrimSpace(os.Getenv("MULTICA_APP_URL"))
	if serverURL == "" || appURL == "" {
		return errors.New("MULTICA_DAEMON_SERVER_URL / MULTICA_APP_URL are not set; skipping the daemon profile")
	}

	dir := filepath.Join(daemonHomeDir(), "profiles", daemonProfileSlug(email))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create profile directory: %w", err)
	}
	path := filepath.Join(dir, "config.json")
	config := map[string]any{}
	if existing, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(existing, &config)
	}
	config["server_url"] = serverURL
	config["app_url"] = appURL
	config["token"] = token
	if workspaceID != "" {
		config["workspace_id"] = workspaceID
	}
	out, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// daemonHomeDir mirrors the CLI's own resolution: MULTICA_HOME when the
// deployment sets it, ~/.multica otherwise.
func daemonHomeDir() string {
	if home := strings.TrimSpace(os.Getenv("MULTICA_HOME")); home != "" {
		return home
	}
	dir, err := os.UserHomeDir()
	if err != nil || dir == "" {
		return ".multica"
	}
	return filepath.Join(dir, ".multica")
}

// daemonProfileSlug names one profile per account. It has to agree with the
// Bridge's own slugging, otherwise one account would end up with two rows.
func daemonProfileSlug(email string) string {
	local := email
	if at := strings.Index(local, "@"); at >= 0 {
		local = local[:at]
	}
	var b strings.Builder
	for _, r := range strings.ToLower(local) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	slug := strings.Trim(b.String(), "-._")
	if slug == "" {
		return "account"
	}
	return slug
}

// PATRenewThreshold is the remaining-lifetime window at which a PAT becomes
// eligible for an in-place renewal. The daemon polls every ~3 days, so a 7-day
// threshold guarantees at least one renewal attempt while the token still has
// ≥ 4 days of validity left — enough margin to absorb a transient network
// failure before the user actually has to re-run `multica login`.
const PATRenewThreshold = 7 * 24 * time.Hour

// PATRenewExtension is how far into the future a renewed PAT's expires_at is
// pushed. Matches the initial issuance window in CreatePersonalAccessToken
// (90 days) so renewed tokens converge on the same lifetime as freshly minted
// ones — no second-class renewed tokens.
const PATRenewExtension = 90 * 24 * time.Hour

type PersonalAccessTokenResponse struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"token_prefix"`
	ExpiresAt  *string `json:"expires_at"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
}

type CreatePATResponse struct {
	PersonalAccessTokenResponse
	Token string `json:"token"`
}

func patToResponse(pat db.PersonalAccessToken) PersonalAccessTokenResponse {
	return PersonalAccessTokenResponse{
		ID:         uuidToString(pat.ID),
		Name:       pat.Name,
		Prefix:     pat.TokenPrefix,
		ExpiresAt:  timestampToPtr(pat.ExpiresAt),
		LastUsedAt: timestampToPtr(pat.LastUsedAt),
		CreatedAt:  timestampToString(pat.CreatedAt),
	}
}

type CreatePATRequest struct {
	Name          string `json:"name"`
	ExpiresInDays *int   `json:"expires_in_days"`
}

func (h *Handler) CreatePersonalAccessToken(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var req CreatePATRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	rawToken, err := auth.GeneratePATToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	var expiresAt pgtype.Timestamptz
	if req.ExpiresInDays != nil && *req.ExpiresInDays > 0 {
		expiresAt = pgtype.Timestamptz{
			Time:  time.Now().Add(time.Duration(*req.ExpiresInDays) * 24 * time.Hour),
			Valid: true,
		}
	}

	prefix := rawToken
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}

	pat, err := h.Queries.CreatePersonalAccessToken(r.Context(), db.CreatePersonalAccessTokenParams{
		UserID:      parseUUID(userID),
		Name:        req.Name,
		TokenHash:   auth.HashToken(rawToken),
		TokenPrefix: prefix,
		ExpiresAt:   expiresAt,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create token")
		return
	}

	writeJSON(w, http.StatusCreated, CreatePATResponse{
		PersonalAccessTokenResponse: patToResponse(pat),
		Token:                       rawToken,
	})
}

func (h *Handler) ListPersonalAccessTokens(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	pats, err := h.Queries.ListPersonalAccessTokensByUser(r.Context(), parseUUID(userID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list tokens")
		return
	}

	resp := make([]PersonalAccessTokenResponse, len(pats))
	for i, pat := range pats {
		resp[i] = patToResponse(pat)
	}
	writeJSON(w, http.StatusOK, resp)
}

// RenewPATResponse is the body returned by RenewCurrentPersonalAccessToken.
//
// Renewed=false is a no-op, not an error — it just means the caller polled
// before the token entered the renewal window. Callers should always read
// ExpiresAt for the authoritative expiry rather than assuming the old value
// is still current.
type RenewPATResponse struct {
	ExpiresAt string `json:"expires_at"`
	Renewed   bool   `json:"renewed"`
}

// RenewCurrentPersonalAccessToken extends the expires_at of the PAT used to
// authenticate this request, in-place, when it is inside the renewal window.
//
// The endpoint deliberately does NOT mint a new token — that would require
// either rotating the raw secret (breaks the CLI/daemon multi-process model,
// where a single PAT is shared by every process started from the same CLI
// config) or returning the raw token over the wire on every poll (a needless
// exposure since the daemon already holds it). Instead we extend the row's
// expires_at atomically; the cached PAT entry's TTL is short enough
// (auth.AuthCacheTTL ≤ 10m) that the cache catches up to the new expiry on
// the next cache miss without an explicit invalidation.
//
// Only mul_ PATs may be renewed: a cookie/JWT session has no PAT row to
// extend, and an mat_ task token is single-purpose and short-lived. mcn_
// cloud-node PATs are owned by Multica Cloud Fleet, not us — we don't even
// see the expiry locally.
func (h *Handler) RenewCurrentPersonalAccessToken(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	// Re-read the raw token from the Authorization header — the upstream Auth
	// middleware resolves it to a userID but doesn't pass the hash forward,
	// and we need the row, not just the user.
	authHeader := r.Header.Get("Authorization")
	rawToken := strings.TrimPrefix(authHeader, "Bearer ")
	if rawToken == "" || rawToken == authHeader || !strings.HasPrefix(rawToken, "mul_") {
		writeError(w, http.StatusBadRequest, "only personal access tokens can be renewed")
		return
	}

	hash := auth.HashToken(rawToken)
	pat, err := h.Queries.GetPersonalAccessTokenByHash(r.Context(), hash)
	if err != nil {
		// The Auth middleware already validated the token, so reaching here
		// with no row means the PAT was revoked or expired in the gap between
		// the middleware's cache hit and this DB read. Surface a 401 so the
		// daemon's 401 branch fires the same "please re-login" message it
		// would for any other auth failure, instead of a generic 500.
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusUnauthorized, "token is no longer valid")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to look up token")
		return
	}

	// Defense in depth: the middleware already set X-User-ID from the same
	// PAT row, so this mismatch should be impossible. If it ever fires, it
	// means a header was forged past the middleware and we MUST refuse to
	// renew on someone else's behalf — fail loudly.
	if uuidToString(pat.UserID) != userID {
		writeError(w, http.StatusUnauthorized, "token does not belong to caller")
		return
	}

	// PATs minted before this code existed may have a NULL expires_at (the
	// "never expires" case). There is nothing to extend — return the current
	// (absent) expiry and let the caller treat this as a permanent token.
	if !pat.ExpiresAt.Valid {
		writeJSON(w, http.StatusOK, RenewPATResponse{ExpiresAt: "", Renewed: false})
		return
	}

	now := time.Now()
	remaining := pat.ExpiresAt.Time.Sub(now)
	if remaining > PATRenewThreshold {
		writeJSON(w, http.StatusOK, RenewPATResponse{
			ExpiresAt: timestampToString(pat.ExpiresAt),
			Renewed:   false,
		})
		return
	}

	newExpiresAt := pgtype.Timestamptz{Time: now.Add(PATRenewExtension), Valid: true}
	// Pass the renewal threshold as the CAS predicate: only update if the
	// row's existing expires_at is still inside this window. After the
	// first writer succeeds the row sits at now+90d, which is well past
	// now+7d, so any concurrent renewer hits the WHERE and sees ErrNoRows.
	renewThreshold := pgtype.Timestamptz{Time: now.Add(PATRenewThreshold), Valid: true}
	updated, err := h.Queries.ExtendPersonalAccessTokenExpiry(r.Context(), db.ExtendPersonalAccessTokenExpiryParams{
		ID:               pat.ID,
		NewExpiresAt:     newExpiresAt,
		RenewThresholdAt: renewThreshold,
	})
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, RenewPATResponse{
			ExpiresAt: timestampToString(updated),
			Renewed:   true,
		})
	case errors.Is(err, pgx.ErrNoRows):
		// A concurrent renew (or revoke) won the race. Re-read the current
		// row and report what's there now — the daemon's only correctness
		// guarantee is "after a successful call, expires_at is fresh enough
		// to last until the next poll", and a parallel writer already
		// satisfied that, so this is success from the caller's POV.
		current, getErr := h.Queries.GetPersonalAccessTokenByHash(r.Context(), hash)
		if getErr != nil {
			writeError(w, http.StatusUnauthorized, "token is no longer valid")
			return
		}
		writeJSON(w, http.StatusOK, RenewPATResponse{
			ExpiresAt: timestampToString(current.ExpiresAt),
			Renewed:   false,
		})
	default:
		writeError(w, http.StatusInternalServerError, "failed to renew token")
	}
}

func (h *Handler) RevokePersonalAccessToken(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	id := chi.URLParam(r, "id")
	idUUID, ok := parseUUIDOrBadRequest(w, id, "token id")
	if !ok {
		return
	}
	hash, err := h.Queries.RevokePersonalAccessToken(r.Context(), db.RevokePersonalAccessTokenParams{
		ID:     idUUID,
		UserID: parseUUID(userID),
	})
	switch {
	case err == nil:
		// Drop the cache entry immediately so the revocation takes effect
		// before the TTL would otherwise expire the cached lookup.
		h.PATCache.Invalidate(r.Context(), hash)
	case errors.Is(err, pgx.ErrNoRows):
		// Token doesn't exist or doesn't belong to this user. Preserve the
		// pre-existing idempotent 204 behavior — no cache entry to clear.
	default:
		writeError(w, http.StatusInternalServerError, "failed to revoke token")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
