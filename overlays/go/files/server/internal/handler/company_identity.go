package handler

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
)

var errAmbiguousCompanyAccount = errors.New("ambiguous company account")

// Only the company root opts into exactly one child label. Other deployments
// retain their original exact domain allowlist behavior.
func allowedCompanyDomain(allowed []string, domain string) bool {
	for _, root := range allowed {
		root = strings.ToLower(root)
		if domain == root {
			return true
		}
		if root == "baidu.com" && strings.HasSuffix(domain, "."+root) {
			label := strings.TrimSuffix(domain, "."+root)
			if companyLabel(label) {
				return true
			}
		}
	}
	return false
}

func companyLabel(label string) bool {
	if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for _, c := range label {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

func machineIdentityEmail(username, domain string) (string, bool) {
	if !machineUsername.MatchString(username) {
		return "", false
	}
	if domain != "baidu.com" {
		return username + "@" + domain, true
	}
	parts := strings.Split(username, "_")
	if len(parts) == 1 {
		return username + "@" + domain, true
	}
	if len(parts) != 2 || !machineUsername.MatchString(parts[0]) || !companyLabel(parts[1]) {
		return "", false
	}
	return parts[0] + "@" + parts[1] + "." + domain, true
}

func companyEmailAccount(email, root string) string {
	parts := strings.Split(strings.ToLower(email), "@")
	if len(parts) != 2 || !machineUsername.MatchString(parts[0]) {
		return ""
	}
	if parts[1] == root {
		return parts[0]
	}
	if root != "baidu.com" || !strings.HasSuffix(parts[1], "."+root) {
		return ""
	}
	label := strings.TrimSuffix(parts[1], "."+root)
	if !companyLabel(label) {
		return ""
	}
	return parts[0] + "_" + label
}

// Existing verified email accounts take precedence over new-name inference.
// Never merge identities or issue a credential when two candidates exist.
func (h *Handler) resolveCompanyAccountEmail(ctx context.Context, username, domain string) (string, bool, error) {
	inferred, valid := machineIdentityEmail(username, domain)
	if domain != "baidu.com" || !strings.Contains(username, "_") {
		return inferred, valid, nil
	}
	candidates := []string{username + "@" + domain}
	at := strings.LastIndex(username, "_")
	if at > 0 && companyLabel(username[at+1:]) && machineUsername.MatchString(username[:at]) {
		candidates = append(candidates, username[:at]+"@"+username[at+1:]+"."+domain)
	}
	found := ""
	for _, email := range candidates {
		user, err := h.Queries.GetUserByEmail(ctx, email)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return "", false, err
		}
		if found != "" && found != user.Email {
			return "", false, errAmbiguousCompanyAccount
		}
		found = user.Email
	}
	if found != "" {
		return found, true, nil
	}
	// Reserve contractor-style names for the root domain, but only after
	// resolving existing verified identities and rejecting collisions.
	if strings.HasPrefix(username, "v_") && machineUsername.MatchString(username) {
		return username + "@" + domain, true, nil
	}
	return inferred, valid, nil
}

// Email-code login is backed by InfoFlow in this deployment. Refuse a known
// cross-email recipient collision before issuing or consuming an OTP.
func (h *Handler) companyEmailLoginAllowed(ctx context.Context, email string) (bool, error) {
	if strings.ToLower(os.Getenv("INFOFLOW_ISSUE_IDENTITY_DOMAIN")) != "baidu.com" {
		return true, nil
	}
	parts := strings.Split(strings.ToLower(email), "@")
	if len(parts) != 2 || !allowedCompanyDomain([]string{"baidu.com"}, parts[1]) {
		return true, nil
	}
	recipient := companyEmailAccount(email, "baidu.com")
	if recipient == "" {
		return false, nil
	}
	if !strings.Contains(recipient, "_") {
		return true, nil
	}
	if parts[1] == "baidu.com" {
		if _, err := h.Queries.GetUserByEmail(ctx, email); err == nil {
			return true, nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return false, err
		}
	}
	counterpart := recipient + "@baidu.com"
	if parts[1] == "baidu.com" {
		at := strings.LastIndex(recipient, "_")
		if at <= 0 || !companyLabel(recipient[at+1:]) {
			return true, nil
		}
		counterpart = recipient[:at] + "@" + recipient[at+1:] + ".baidu.com"
	}
	if counterpart == email {
		return true, nil
	}
	_, err := h.Queries.GetUserByEmail(ctx, counterpart)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, nil
}
