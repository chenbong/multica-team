package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestCompanyIdentityPreservesExistingUnderscoreAccountsAndFailsAmbiguity(t *testing.T) {
	prefix := "v_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	rootEmail := prefix + "@baidu.com"
	dbfx.User(t, "Legacy account", rootEmail)
	got, valid, err := testHandler.resolveCompanyAccountEmail(context.Background(), prefix, "baidu.com")
	if err != nil || !valid || got != rootEmail {
		t.Fatal("existing root account was reinterpreted")
	}
	parts := strings.Split(prefix, "_")
	childEmail := parts[0] + "@" + parts[1] + ".baidu.com"
	dbfx.User(t, "Separate child account", childEmail)
	if _, _, err = testHandler.resolveCompanyAccountEmail(context.Background(), prefix, "baidu.com"); !errors.Is(err, errAmbiguousCompanyAccount) {
		t.Fatal("ambiguous identity was accepted")
	}
	unseen := "new" + strings.ReplaceAll(uuid.NewString(), "-", "") + "_sz"
	got, valid, err = testHandler.resolveCompanyAccountEmail(context.Background(), unseen, "baidu.com")
	if err != nil || !valid || got != strings.TrimSuffix(unseen, "_sz")+"@sz.baidu.com" {
		t.Fatal("new child mapping changed")
	}
	compound := "v_" + strings.ReplaceAll(uuid.NewString(), "-", "") + "_hk"
	if got, valid, err = testHandler.resolveCompanyAccountEmail(context.Background(), compound, "baidu.com"); err != nil || !valid || got != compound+"@baidu.com" {
		t.Fatal("new contractor name did not retain the root domain")
	}
	compoundEmail := strings.TrimSuffix(compound, "_hk") + "@hk.baidu.com"
	dbfx.User(t, "Verified compound child account", compoundEmail)
	got, valid, err = testHandler.resolveCompanyAccountEmail(context.Background(), compound, "baidu.com")
	if err != nil || !valid || got != compoundEmail {
		t.Fatal("existing verified child account was lost")
	}
	unknownContractor := "v_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if got, valid, err = testHandler.resolveCompanyAccountEmail(context.Background(), unknownContractor, "baidu.com"); err != nil || !valid || got != unknownContractor+"@baidu.com" {
		t.Fatal("new contractor identity did not use the root domain")
	}
	for _, invalid := range []string{"v_", "v_bad!", "v_" + strings.Repeat("a", 64)} {
		if _, valid, err = testHandler.resolveCompanyAccountEmail(context.Background(), invalid, "baidu.com"); err != nil || valid {
			t.Fatal("invalid contractor name accepted")
		}
	}
}
func TestCompanyIdentityProvisionLegacyTokenAndRejectCollision(t *testing.T) {
	username := "v_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	email := username + "@baidu.com"
	owner := dbfx.User(t, "Existing contractor fixture", email)
	workspace := dbfx.Workspace(t, "Legacy identity fixture", "legacy-"+uuid.NewString())
	f := testutil.New(testPool, workspace, owner)
	f.Member(t, workspace, owner, "owner")
	f.Cleanup(t, `DELETE FROM personal_access_token WHERE user_id=$1`, owner)
	h := machineTestConfig(t, username)
	h.cfg.AllowedEmailDomains = []string{"baidu.com"}
	t.Setenv("MULTICA_MACHINE_PROVISION_EMAIL_DOMAIN", "baidu.com")
	t.Setenv("INFOFLOW_ISSUE_IDENTITY_DOMAIN", "baidu.com")
	response := machineCall(t, h, machineBody(username), provisionServiceToken)
	if response.Code != 200 {
		t.Fatalf("legacy command status %d", response.Code)
	}
	pat, err := h.Queries.GetPersonalAccessTokenByHash(context.Background(), auth.HashToken(machineIssuedToken(t, response.Body.String())))
	if err != nil || uuidToString(pat.UserID) != owner {
		t.Fatal("credential issued for the wrong identity")
	}
	resolved, role, err := h.infoFlowIdentity(context.Background(), username, workspace)
	if err != nil || uuidToString(resolved.ID) != owner || role != "owner" {
		t.Fatal("legacy InfoFlow sender no longer resolves")
	}
	parts := strings.Split(username, "_")
	childEmail := parts[0] + "@" + parts[1] + ".baidu.com"
	dbfx.User(t, "Separate child fixture", childEmail)
	response = machineCall(t, h, machineBody(username), provisionServiceToken)
	if response.Code != 409 || strings.Contains(response.Body.String(), "mul_") {
		t.Fatal("collision returned a credential")
	}
	allowed, err := h.companyEmailLoginAllowed(context.Background(), email)
	if err != nil || !allowed {
		t.Fatal("existing root login was blocked")
	}
	allowed, err = h.companyEmailLoginAllowed(context.Background(), childEmail)
	if err != nil || allowed {
		t.Fatal("conflicting child login was accepted")
	}
	for _, handler := range []func(http.ResponseWriter, *http.Request){h.SendCode, h.VerifyCode} {
		req := testutil.JSONRequest("POST", "/auth/check", map[string]string{"email": childEmail, "code": "123456"})
		testutil.Call(t, handler, req).Want(409)
	}
	if f.Count(t, `SELECT count(*) FROM verification_code WHERE email=$1`, childEmail) != 0 {
		t.Fatal("OTP generated for a conflicting identity")
	}
	if _, _, err = h.infoFlowIdentity(context.Background(), username, workspace); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("ambiguous sender accepted")
	}
}

func TestCompanyIdentityProvisionNewContractorUsesRoot(t *testing.T) {
	username := "v_" + strings.ReplaceAll(uuid.NewString(), "-", "") + "_test"
	email := username + "@baidu.com"
	h := machineTestConfig(t, username)
	h.cfg.AllowedEmailDomains = []string{"baidu.com"}
	t.Setenv("MULTICA_MACHINE_PROVISION_EMAIL_DOMAIN", "baidu.com")
	t.Cleanup(func() {
		ctx := context.Background()
		for _, query := range []string{
			`DELETE FROM personal_access_token WHERE user_id IN (SELECT id FROM "user" WHERE email=$1)`,
			`DELETE FROM issue_status WHERE workspace_id IN (SELECT workspace_id FROM member WHERE user_id IN (SELECT id FROM "user" WHERE email=$1))`,
			`DELETE FROM workspace WHERE id IN (SELECT workspace_id FROM member WHERE user_id IN (SELECT id FROM "user" WHERE email=$1))`,
			`DELETE FROM member WHERE user_id IN (SELECT id FROM "user" WHERE email=$1)`,
			`DELETE FROM "user" WHERE email=$1`,
		} {
			if _, err := testPool.Exec(ctx, query, email); err != nil {
				t.Errorf("contractor fixture cleanup failed: %v", err)
			}
		}
	})
	one := machineCall(t, h, machineBody(username), provisionServiceToken)
	if one.Code != 200 {
		t.Fatalf("new contractor command status %d", one.Code)
	}
	two := machineCall(t, h, machineBody(username), provisionServiceToken)
	if two.Code != 200 || one.Body.String() != two.Body.String() {
		t.Fatal("contractor command was not idempotent")
	}
	user, err := h.Queries.GetUserByEmail(context.Background(), email)
	if err != nil {
		t.Fatal("contractor root account not created")
	}
	pat, err := h.Queries.GetPersonalAccessTokenByHash(context.Background(), auth.HashToken(machineIssuedToken(t, one.Body.String())))
	if err != nil || pat.UserID != user.ID {
		t.Fatal("contractor credential owner mismatch")
	}
	child := strings.TrimSuffix(username, "_test") + "@test.baidu.com"
	if _, err := h.Queries.GetUserByEmail(context.Background(), child); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("unexpected inferred child account")
	}
}
