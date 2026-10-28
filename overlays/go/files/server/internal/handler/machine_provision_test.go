package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const provisionServiceToken = "fixture-service-credential-32-characters"

func machineTestConfig(t *testing.T, username string) *Handler {
	t.Helper()
	t.Setenv("MULTICA_MACHINE_PROVISION_TOKEN", provisionServiceToken)
	t.Setenv("MULTICA_MACHINE_PROVISION_EMAIL_DOMAIN", "example.test")
	t.Setenv("MULTICA_PLUGIN_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("f", 32))))
	t.Setenv("MULTICA_DAEMON_SERVER_URL", "http://api.example.test:8006")
	t.Setenv("MULTICA_APP_URL", "http://app.example.test:8004")
	t.Setenv("FRONTEND_ORIGIN", "")
	t.Setenv("MULTICA_PUBLIC_URL", "")
	p := filepath.Join(t.TempDir(), "web-build.json")
	if err := os.WriteFile(p, []byte(`{"MULTICA_CLI_INSTALL_URL":"https://example.test/install.sh","MULTICA_DAEMON_RUNTIME_DEFAULTS":"ducc,ducx","MULTICA_DAEMON_RUNTIME_SHIM_DEFAULTS":"ducc,ducx"}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MULTICA_WEB_BUILD_CONFIG", p)
	t.Cleanup(func() {
		ctx := context.Background()
		email := username + "@example.test"
		var ids []string
		rows, err := testPool.Query(ctx, `SELECT w.id::text FROM workspace w JOIN member m ON m.workspace_id=w.id JOIN "user" u ON u.id=m.user_id WHERE u.email=$1 AND m.role='owner' AND (w.name=$2 OR w.slug LIKE 'personal-%')`, email, username)
		if err == nil {
			for rows.Next() {
				var id string
				_ = rows.Scan(&id)
				ids = append(ids, id)
			}
			rows.Close()
		}
		_, _ = testPool.Exec(ctx, `DELETE FROM personal_access_token WHERE user_id IN (SELECT id FROM "user" WHERE email=$1)`, email)
		_, _ = testPool.Exec(ctx, `DELETE FROM member WHERE user_id IN (SELECT id FROM "user" WHERE email=$1)`, email)
		for _, id := range ids {
			_, _ = testPool.Exec(ctx, `DELETE FROM issue_status WHERE workspace_id=$1`, id)
			_, _ = testPool.Exec(ctx, `DELETE FROM workspace WHERE id=$1`, id)
		}
		_, _ = testPool.Exec(ctx, `DELETE FROM "user" WHERE email=$1`, email)
	})
	h := *testHandler
	h.cfg.AllowSignup = true
	h.cfg.AllowedEmails = nil
	h.cfg.AllowedEmailDomains = []string{"example.test"}
	return &h
}

func machineTestName() string { return "test" + strings.ReplaceAll(uuid.NewString(), "-", "") }

func machineCall(t *testing.T, h *Handler, body, token string) *httptest.ResponseRecorder {
	r := testutil.JSONRequest("POST", "/api/integrations/machine-provision/command", body)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return testutil.Call(t, h.MachineProvisionCommand, r).ResponseRecorder
}

func machineBody(username string) string {
	b, _ := json.Marshal(map[string]string{"username": username})
	return string(b)
}

func machineIssuedToken(t *testing.T, body string) string {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if _, value, ok := strings.Cut(line, " login --tmesh-noninteractive --token '"); ok {
			return strings.TrimSuffix(value, "'")
		}
	}
	t.Fatal("no login token in command")
	return ""
}

func TestMachineProvisionNewUserIdempotentAndAtomic(t *testing.T) {
	username := machineTestName()
	h := machineTestConfig(t, username)
	one := machineCall(t, h, machineBody(username), provisionServiceToken)
	if one.Code != 200 {
		t.Fatalf("provision status %d", one.Code)
	}
	two := machineCall(t, h, machineBody(strings.ToUpper(username)), provisionServiceToken)
	if two.Code != 200 || one.Body.String() != two.Body.String() {
		t.Fatal("repeat did not reuse command/token")
	}
	if one.Header().Get("Content-Type") != "text/plain; charset=utf-8" || !strings.Contains(one.Header().Get("Cache-Control"), "no-store") {
		t.Fatal("unsafe response headers")
	}
	user, err := h.Queries.GetUserByEmail(context.Background(), username+"@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if !user.OnboardedAt.Valid {
		t.Fatal("new user not initialized")
	}
	ws, err := h.Queries.ListWorkspaces(context.Background(), user.ID)
	if err != nil || len(ws) != 0 {
		t.Fatal("requesting a command must not create a workspace")
	}
	var pats int
	_ = testPool.QueryRow(context.Background(), `SELECT count(*) FROM personal_access_token WHERE user_id=$1`, user.ID).Scan(&pats)
	if pats != 1 {
		t.Fatalf("tokens=%d", pats)
	}
	pat, err := h.Queries.GetPersonalAccessTokenByHash(context.Background(), auth.HashToken(machineIssuedToken(t, one.Body.String())))
	if err != nil || pat.UserID != user.ID {
		t.Fatal("incorrect token owner")
	}
	raw, _, err := h.getOrCreateDaemonBootstrapToken(context.Background(), user.ID)
	if err != nil || raw != machineIssuedToken(t, one.Body.String()) {
		t.Fatal("website token was not reused")
	}
	website := testutil.WithHeaders(testutil.JSONRequest("POST", "/api/daemon-bootstrap-token", nil), "X-User-ID", uuidToString(user.ID))
	response := testutil.Call(t, h.GetOrCreateDaemonBootstrapToken, website).Want(200)
	var existing CreatePATResponse
	response.JSON(&existing)
	if existing.Token != raw {
		t.Fatal("website bootstrap endpoint changed token")
	}
}

func TestMachineProvisionExistingUserKeepsWorkspacesAndRoles(t *testing.T) {
	username := machineTestName()
	user := dbfx.User(t, "Existing User", username+"@example.test")
	ws := dbfx.Workspace(t, "Existing Shared", "shared-"+uuid.NewString())
	dbfx.Member(t, ws, user, "member")
	h := machineTestConfig(t, username)
	one := machineCall(t, h, machineBody(username), provisionServiceToken)
	if one.Code != 200 {
		t.Fatalf("status %d", one.Code)
	}
	rows, err := h.Queries.ListWorkspaces(context.Background(), parseUUID(user))
	if err != nil || len(rows) != 1 {
		t.Fatal("expected only the existing shared workspace")
	}
	kept := false
	for _, row := range rows {
		if uuidToString(row.ID) == ws {
			kept = true
		}
	}
	if !kept {
		t.Fatal("existing workspace replaced")
	}
	var role string
	_ = testPool.QueryRow(context.Background(), `SELECT role FROM member WHERE user_id=$1 AND workspace_id=$2`, user, ws).Scan(&role)
	if role != "member" {
		t.Fatal("existing role escalated")
	}
}

func TestMachineProvisionConcurrentRetryReusesResources(t *testing.T) {
	username := machineTestName()
	h := machineTestConfig(t, username)
	results := make(chan *httptest.ResponseRecorder, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- machineCall(t, h, machineBody(username), provisionServiceToken) }()
	}
	wg.Wait()
	close(results)
	body := ""
	for r := range results {
		if r.Code != 200 {
			t.Fatalf("concurrent status %d", r.Code)
		}
		if body != "" && body != r.Body.String() {
			t.Fatal("different concurrent commands")
		}
		body = r.Body.String()
	}
	user, err := h.Queries.GetUserByEmail(context.Background(), username+"@example.test")
	if err != nil {
		t.Fatal(err)
	}
	ws, err := h.Queries.ListWorkspaces(context.Background(), user.ID)
	if err != nil || len(ws) != 0 {
		t.Fatal("API must not create a workspace")
	}
}

func TestMachineProvisionAuthenticationAndValidation(t *testing.T) {
	username := machineTestName()
	h := machineTestConfig(t, username)
	for _, token := range []string{"", "wrong", "mul_fixture-user-token", provisionServiceToken + " "} {
		if r := machineCall(t, h, machineBody(username), token); r.Code != 401 {
			t.Fatalf("bad credential accepted: %d", r.Code)
		}
	}
	for _, body := range []string{`{}`, `null`, `{"username":3}`, `{"username":"../root"}`, `{"username":"a@example.test"}`, `{"username":" x"}`, `{"username":"a","role":"owner"}`, `{"username":"a","workspace_id":"x"}`, `{"username":"a"} {}`, `{"username":"` + strings.Repeat("a", 5000) + `"}`} {
		if r := machineCall(t, h, body, provisionServiceToken); r.Code != 400 {
			t.Fatalf("invalid body status %d", r.Code)
		}
	}
	_, err := h.Queries.GetUserByEmail(context.Background(), username+"@example.test")
	if err == nil {
		t.Fatal("rejected request created user")
	}
	t.Setenv("MULTICA_MACHINE_PROVISION_TOKEN", "")
	if r := machineCall(t, h, machineBody(username), provisionServiceToken); r.Code != 503 {
		t.Fatal("unconfigured endpoint not closed")
	}
}

func TestMachineProvisionDoesNotRequireWorkspaceCreation(t *testing.T) {
	username := machineTestName()
	h := machineTestConfig(t, username)
	h.cfg.DisableWorkspaceCreation = true
	if r := machineCall(t, h, machineBody(username), provisionServiceToken); r.Code != 200 {
		t.Fatalf("status %d", r.Code)
	}
	if _, err := h.Queries.GetUserByEmail(context.Background(), username+"@example.test"); err != nil {
		t.Fatal("account should still be prepared")
	}
}

func TestMachineProvisionMissingEncryptionAndSettingsAreFailClosed(t *testing.T) {
	username := machineTestName()
	h := machineTestConfig(t, username)
	for _, key := range []string{"MULTICA_PLUGIN_SECRET_KEY", "MULTICA_WEB_BUILD_CONFIG", "MULTICA_MACHINE_PROVISION_EMAIL_DOMAIN", "MULTICA_APP_URL"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "")
			if r := machineCall(t, h, machineBody(username), provisionServiceToken); r.Code != 503 {
				t.Fatalf("status %d", r.Code)
			}
		})
	}
	if _, err := h.Queries.GetUserByEmail(context.Background(), username+"@example.test"); err == nil {
		t.Fatal("configuration failure created account")
	}
}

func TestMachineProvisionRealHTTPOnlyReturnsPlainCommand(t *testing.T) {
	username := machineTestName()
	h := machineTestConfig(t, username)
	server := httptest.NewServer(http.HandlerFunc(h.MachineProvisionCommand))
	defer server.Close()
	r, _ := http.NewRequest("POST", server.URL, strings.NewReader(machineBody(username)))
	r.Header.Set("Authorization", "Bearer "+provisionServiceToken)
	r.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatal("HTTP contract mismatch")
	}
}

func TestMachineProvisionInvalidSharedTokenIsReplaced(t *testing.T) {
	for _, revoked := range []bool{false, true} {
		t.Run(fmt.Sprint(revoked), func(t *testing.T) {
			username := machineTestName()
			h := machineTestConfig(t, username)
			one := machineCall(t, h, machineBody(username), provisionServiceToken)
			if one.Code != 200 {
				t.Fatal("initial issuance failed")
			}
			raw := machineIssuedToken(t, one.Body.String())
			if revoked {
				dbfx.Exec(t, `UPDATE personal_access_token SET revoked=true WHERE token_hash=$1`, auth.HashToken(raw))
			} else {
				dbfx.Exec(t, `UPDATE personal_access_token SET expires_at=clock_timestamp()-interval '1 minute' WHERE token_hash=$1`, auth.HashToken(raw))
			}
			two := machineCall(t, h, machineBody(username), provisionServiceToken)
			if two.Code != 200 || machineIssuedToken(t, two.Body.String()) == raw {
				t.Fatal("invalid token was returned again")
			}
		})
	}
}

func TestMachineProvisionTokenFailureRollsBackWorkspace(t *testing.T) {
	username := machineTestName()
	user := dbfx.User(t, "Existing", username+"@example.test", testutil.Cols{"daemon_bootstrap_token_encrypted": []byte("broken ciphertext")})
	h := machineTestConfig(t, username)
	response := machineCall(t, h, machineBody(username), provisionServiceToken)
	if response.Code != 500 || strings.Contains(response.Body.String(), "broken ciphertext") {
		t.Fatal("unsafe token error")
	}
	ws, err := h.Queries.ListWorkspaces(context.Background(), parseUUID(user))
	if err != nil || len(ws) != 0 {
		t.Fatal("partial workspace survived token failure")
	}
}

func TestMachineProvisionNeverReturnsAnotherUsersToken(t *testing.T) {
	username := machineTestName()
	user := dbfx.User(t, "Target", username+"@example.test")
	other := dbfx.User(t, "Other", machineTestName()+"@example.test")
	h := machineTestConfig(t, username)
	dbfx.Cleanup(t, `DELETE FROM personal_access_token WHERE user_id=$1`, other)
	raw, _, err := h.getOrCreateDaemonBootstrapToken(context.Background(), parseUUID(other))
	if err != nil {
		t.Fatal(err)
	}
	key, _ := secretbox.LoadKey("MULTICA_PLUGIN_SECRET_KEY")
	box, _ := secretbox.New(key)
	sealed, _ := box.Seal([]byte(raw))
	dbfx.Exec(t, `UPDATE "user" SET daemon_bootstrap_token_encrypted=$1 WHERE id=$2`, sealed, user)
	response := machineCall(t, h, machineBody(username), provisionServiceToken)
	if response.Code != 500 || strings.Contains(response.Body.String(), raw) {
		t.Fatal("foreign owner token escaped")
	}
}

func TestMachineProvisionRejectsBrowserAndIgnoresIdentityHeaders(t *testing.T) {
	username := machineTestName()
	h := machineTestConfig(t, username)
	r := testutil.WithHeaders(testutil.JSONRequest("POST", "/command", machineBody(username)), "Authorization", "Bearer "+provisionServiceToken, "Origin", "http://app.example.test")
	testutil.Call(t, h.MachineProvisionCommand, r).Want(400)
	r = testutil.WithHeaders(testutil.JSONRequest("POST", "/command", machineBody(username)), "X-User-ID", testUserID, "Cookie", "session=fixture")
	testutil.Call(t, h.MachineProvisionCommand, r).Want(401)
	r = testutil.WithHeaders(testutil.JSONRequest("POST", "/command", machineBody(username)), "Authorization", "Bearer "+provisionServiceToken, "X-User-ID", testUserID, "X-Workspace-ID", testWorkspaceID)
	response := testutil.Call(t, h.MachineProvisionCommand, r).Want(200)
	raw := machineIssuedToken(t, response.Text())
	pat, err := h.Queries.GetPersonalAccessTokenByHash(context.Background(), auth.HashToken(raw))
	if err != nil || uuidToString(pat.UserID) == testUserID {
		t.Fatal("actor headers selected owner")
	}
}

func TestMachineProvisionAuditLogContainsNoCredentials(t *testing.T) {
	username := machineTestName()
	h := machineTestConfig(t, username)
	var output bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(original) })
	response := machineCall(t, h, machineBody(username), provisionServiceToken)
	if response.Code != 200 {
		t.Fatal("provision failed")
	}
	raw := machineIssuedToken(t, response.Body.String())
	if !strings.Contains(output.String(), "user_id=") || strings.Contains(output.String(), raw) || strings.Contains(output.String(), provisionServiceToken) || strings.Contains(output.String(), "login --token") {
		t.Fatal("unsafe audit log")
	}
}

func TestMachineProvisionRespectsSignupAndDisabledAccountPolicies(t *testing.T) {
	username := machineTestName()
	h := machineTestConfig(t, username)
	h.cfg.AllowSignup = false
	h.cfg.AllowedEmailDomains = []string{"different.example"}
	if r := machineCall(t, h, machineBody(username), provisionServiceToken); r.Code != 403 {
		t.Fatal("signup policy bypassed")
	}
	t.Setenv("MULTICA_MACHINE_PROVISION_EMAIL_DOMAIN", "embassybase.com")
	if r := machineCall(t, h, machineBody("pdzzer68"), provisionServiceToken); r.Code != 403 {
		t.Fatal("disabled account bypassed")
	}
}

func TestMachineProvisionNormalizesWebRuntimeDefaults(t *testing.T) {
	if got := machineRuntimeDefaults("ducc, ducx ducc ,,ducx"); got != "ducc,ducx" {
		t.Fatalf("unexpected defaults: %s", got)
	}
}

func TestMachineProvisionSignupPolicyUsesTheExistingTransaction(t *testing.T) {
	username := machineTestName()
	h := machineTestConfig(t, username)
	h.cfg.AllowSignup = false
	h.cfg.AllowedEmailDomains = []string{"different.example"}
	cfg := testPool.Config()
	cfg.MaxConns = 1
	cfg.MinConns = 0
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	h.DB = pool
	h.TxStarter = pool
	h.Queries = db.New(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r := testutil.WithHeaders(testutil.JSONRequest("POST", "/command", machineBody(username)), "Authorization", "Bearer "+provisionServiceToken).WithContext(ctx)
	testutil.Call(t, h.MachineProvisionCommand, r).Want(403)
	if ctx.Err() != nil {
		t.Fatal("signup check exhausted the connection pool")
	}
}

func TestMachineProvisionLinuxCommandMatchesWeb(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node 22+ required for Web parity test")
	}
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "overlays/web-direct/packages/views/runtimes/components/connection-commands.ts")
	for _, defaults := range []string{"", "ducc,ducx"} {
		cfg := machineCommandConfig{ServerURL: "http://api.example.test:8006", AppURL: "http://app.example.test:8004", InstallURL: "https://example.test/a'b.sh", Runtimes: defaults, Shims: defaults}
		for _, username := range []string{"test.user", "a_b-c"} {
			opts, _ := json.Marshal(map[string]string{"serverUrl": cfg.ServerURL, "appUrl": cfg.AppURL, "installUrl": cfg.InstallURL, "runtimeDefaults": cfg.Runtimes, "shimDefaults": cfg.Shims, "email": username + "@example.test", "token": "mul_public-fixture-token"})
			program := `import {connectionCommand} from ` + strconvQuote("file://"+source) + `;process.stdout.write(JSON.stringify(connectionCommand("unused","macosLinux",` + string(opts) + `)));`
			output, err := exec.Command(node, "--experimental-strip-types", "--input-type=module", "-e", program).Output()
			if err != nil {
				t.Fatal("Web helper runner failed: ", err)
			}
			var expected string
			if err = json.Unmarshal(output, &expected); err != nil {
				t.Fatal(err)
			}
			if machineLinuxCommand(cfg, username+"@example.test", "mul_public-fixture-token") != expected {
				t.Fatal("API command differs from actual Web helper")
			}
		}
	}
}

func strconvQuote(v string) string { b, _ := json.Marshal(v); return string(b) }
